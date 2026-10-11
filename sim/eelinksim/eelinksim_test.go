// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package eelinksim_test

import (
	"bufio"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/eelinksim"
	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers/eelink"
	"github.com/cB-Sigma/trackers/track"
)

// Login, heartbeat, location and warning decode with the library's codec
// and are answered where the protocol answers.
func TestTrackerAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, eelink.NewCodec, nil)
	tr, err := eelinksim.Dial(addr, "352544071677471")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Login(); err != nil {
		t.Fatal(err)
	}
	if err := tr.Heartbeat(0x0187); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	p := eelinksim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36, Heading: 90, Sats: 9, Valid: true}
	if err := tr.Location(p); err != nil {
		t.Fatal(err)
	}
	m, f := codectest.NextFix(t, msgs)
	if m.IMEI != "352544071677471" || !f.Valid || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) ||
		*f.Speed != 36 || *f.Sats != 9 || m.Records[0].Status["ignition"] != true || m.Records[0].Status["odometer"] != 1234567 {
		t.Fatalf("%+v %+v", m, f)
	}
	if err := tr.Warning(p, 0x02); err != nil {
		t.Fatal(err)
	}
	m, _ = codectest.NextFix(t, msgs)
	if a := m.Records[0].Alerts; len(a) != 1 || a[0].Type != "emergency" {
		t.Fatalf("%+v", a)
	}
}

// Commands framed by the codec reach the tracker; its answers carry the
// UID back and decode as responses, WHERE also as a position.
func TestCommandsAgainstCodec(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	nc, err := eelink.NewCodec(nil)
	if err != nil {
		t.Fatal(err)
	}
	codec := nc()
	got := make(chan *track.Message, 16)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		for {
			m, err := codec.Decode(r)
			if errors.Is(err, track.ErrSkip) {
				continue
			}
			if err != nil {
				close(got)
				return
			}
			if len(m.Reply) > 0 {
				_, _ = c.Write(m.Reply)
			}
			if m.Login {
				for i, cmd := range []any{map[string]any{"action": "engineStop"}, map[string]any{"action": "locate"}} {
					b, _, _ := codec.Command(m.IMEI, cmd, uint32(40+i))
					_, _ = c.Write(b)
				}
			}
			got <- m
		}
	}()
	tr, err := eelinksim.Dial(ln.Addr().String(), "352544071677471")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Location(eelinksim.Pos{Time: time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC), Lat: 12.5, Lon: 77.25, Valid: true}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Login(); err != nil {
		t.Fatal(err)
	}
	var answers []*track.Message
	deadline := time.After(5 * time.Second)
	for len(answers) < 2 {
		select {
		case m := <-got:
			if m.Response != nil {
				answers = append(answers, m)
			}
		case <-deadline:
			t.Fatalf("answers %d; commands %q", len(answers), tr.Commands())
		}
	}
	if *answers[0].Response != "Relay enable OK" || answers[0].Correlation != "40" || answers[0].Failed {
		t.Fatalf("%+v", answers[0])
	}
	if answers[1].Correlation != "41" || len(answers[1].Records) != 1 || !codectest.Near(answers[1].Records[0].Fix.Lat, 12.5) {
		t.Fatalf("%+v", answers[1])
	}
	if cmds := tr.Commands(); len(cmds) != 2 || cmds[0] != "RELAY,1#" || cmds[1] != "WHERE#" {
		t.Fatalf("%q", cmds)
	}
}
