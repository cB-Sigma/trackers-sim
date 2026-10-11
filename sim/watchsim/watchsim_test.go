// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package watchsim_test

import (
	"bufio"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers-sim/sim/watchsim"
	"github.com/cB-Sigma/trackers/track"
	"github.com/cB-Sigma/trackers/watch"
)

// Link, position and alarm decode with the library's codec; LK and AL
// are answered.
func TestTrackerAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, watch.NewCodec, nil)
	tr, err := watchsim.Dial(addr, "8800000015")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Login(); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	p := watchsim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36, Heading: 90, Sats: 9, Valid: true}
	if err := tr.Report(p); err != nil {
		t.Fatal(err)
	}
	m, f := codectest.NextFix(t, msgs)
	r := m.Records[0]
	if m.IMEI != "8800000015" || !f.Valid || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) ||
		*f.Speed != 36 || *f.Sats != 9 || r.Status["batteryLevel"] != 95 || r.Network == nil || r.Network.Cells[0].MCC != 404 {
		t.Fatalf("%+v %+v", m, f)
	}
	if err := tr.Alarm(p, 0x00010000); err != nil {
		t.Fatal(err)
	}
	m, _ = codectest.NextFix(t, msgs)
	if a := m.Records[0].Alerts; len(a) != 1 || a[0].Type != "emergency" {
		t.Fatalf("%+v", a)
	}
}

// Commands framed by the codec reach the watch; its answers repeat the
// keyword and complete the commands by correlation.
func TestCommandsAgainstCodec(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	nc, err := watch.NewCodec(nil)
	if err != nil {
		t.Fatal(err)
	}
	codec := nc()
	got := make(chan *track.Message, 16)
	cmds := []any{map[string]any{"action": "locate"}, map[string]any{"action": "interval", "seconds": 60}, "VERNO", map[string]any{"sos": []any{"9800000001"}}}
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
				for i, cmd := range cmds {
					b, _, _ := codec.Command(m.IMEI, cmd, uint32(i))
					_, _ = c.Write(b)
				}
			}
			got <- m
		}
	}()
	tr, err := watchsim.Dial(ln.Addr().String(), "8800000015")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Login(); err != nil {
		t.Fatal(err)
	}
	var answers []*track.Message
	deadline := time.After(5 * time.Second)
	for len(answers) < len(cmds) {
		select {
		case m := <-got:
			if m.Response != nil {
				answers = append(answers, m)
			}
		case <-deadline:
			t.Fatalf("answers %d; commands %q", len(answers), tr.Commands())
		}
	}
	for i, cmd := range cmds {
		want, _ := codec.(track.Correlator).Correlation(cmd, uint32(i))
		if answers[i].Correlation != want {
			t.Errorf("%v: answer %+v, want correlation %q", cmd, answers[i], want)
		}
	}
	if *answers[2].Response != "VERNO,SIM_WATCH_V1.0" {
		t.Fatalf("%q", *answers[2].Response)
	}
	if c := tr.Commands(); len(c) != 4 || c[0] != "CR" || c[1] != "UPLOAD,60" || c[3] != "SOS,9800000001" {
		t.Fatalf("%q", c)
	}
}
