// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package suntechsim_test

import (
	"bufio"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers-sim/sim/suntechsim"
	"github.com/cB-Sigma/trackers/suntech"
	"github.com/cB-Sigma/trackers/track"
)

var at = time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)

// Status reports, alerts and keep-alives of both protocols decode with the
// library's codec.
func TestTrackerAgainstCodec(t *testing.T) {
	for _, model := range []string{"", "ST300", "SA200"} {
		addr, msgs := codectest.Serve(t, suntech.NewCodec, nil)
		tr, err := suntechsim.Dial(addr, "0560001616", model)
		if err != nil {
			t.Fatal(err)
		}
		p := suntechsim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36, Course: 90, Sats: 9, Valid: true}
		if err := tr.Status(p); err != nil {
			t.Fatal(err)
		}
		m, f := codectest.NextFix(t, msgs)
		if m.IMEI != "0560001616" || !m.Login || !f.Valid || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) ||
			*f.Speed != 36 || *f.Heading != 90 || *f.Sats != 9 || m.Records[0].Status["ignition"] != true {
			t.Fatalf("%q: %s %+v %v", model, m.IMEI, f, m.Records[0].Status)
		}
		if err := tr.Emergency(p); err != nil {
			t.Fatal(err)
		}
		m, _ = codectest.NextFix(t, msgs)
		if len(m.Records[0].Alerts) != 1 || m.Records[0].Alerts[0].Type != "emergency" {
			t.Fatalf("%q: emergency %+v", model, m.Records[0])
		}
		if err := tr.Alert(p, 41); err != nil {
			t.Fatal(err)
		}
		m, _ = codectest.NextFix(t, msgs)
		if len(m.Records[0].Alerts) != 1 || m.Records[0].Alerts[0].Type != "main-power-removed" {
			t.Fatalf("%q: alert %+v", model, m.Records[0])
		}
		if err := tr.Alive(); err != nil {
			t.Fatal(err)
		}
		select {
		case m := <-msgs:
			if m.Kind != "heartbeat" {
				t.Fatalf("%q: %+v", model, m)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no keep-alive")
		}
		_ = tr.Close()
	}
}

// The library's named commands reach the simulator, which carries them
// out and answers with the command's name, so each answer correlates.
func TestCommandsAgainstCodec(t *testing.T) {
	for _, model := range []string{"", "ST300"} {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		tr, err := suntechsim.Dial(ln.Addr().String(), "0560001616", model)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		nc, _ := suntech.NewCodec(nil)
		c := nc()
		br := bufio.NewReader(conn)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if err := tr.Status(suntechsim.Pos{Time: at, Lat: 1, Lon: 2, Valid: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Decode(br); err != nil {
			t.Fatal(err)
		}
		answer := func(content map[string]any) *track.Message {
			f, answers, err := c.Command("0560001616", content, 1)
			if err != nil || !answers {
				t.Fatalf("%v: %v", content, err)
			}
			if _, err := conn.Write(f); err != nil {
				t.Fatal(err)
			}
			want, _ := c.(track.Correlator).Correlation(content, 1)
			for {
				m, err := c.Decode(br)
				if errors.Is(err, track.ErrSkip) {
					continue
				}
				if err != nil {
					t.Fatalf("%q %v: %v", model, content, err)
				}
				if m.Response != nil {
					if m.Correlation != want || m.Failed {
						t.Fatalf("%q %v: answer %+v, want %q", model, content, m, want)
					}
					return m
				}
			}
		}
		answer(map[string]any{"action": "engineStop"})
		if !tr.Output1() {
			t.Errorf("%q: output 1 off after engineStop", model)
		}
		answer(map[string]any{"action": "engineResume"})
		if tr.Output1() {
			t.Errorf("%q: output 1 on after engineResume", model)
		}
		answer(map[string]any{"action": "reboot"})
		answer(map[string]any{"action": "locate"})
		if m, err := c.Decode(br); err != nil || len(m.Records) != 1 || m.Records[0].Fix == nil {
			t.Errorf("%q: no report after locate: %v %v", model, m, err)
		}
		if tr.Reboots() != 1 || len(tr.Commands()) != 4 {
			t.Errorf("%q: %d reboots, commands %q", model, tr.Reboots(), tr.Commands())
		}
		_ = tr.Close()
		_ = conn.Close()
		_ = ln.Close()
	}
}
