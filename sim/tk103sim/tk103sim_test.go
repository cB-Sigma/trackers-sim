// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package tk103sim_test

import (
	"bufio"
	"net"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/tk103sim"
	"github.com/cB-Sigma/trackers/tk103"
	"github.com/cB-Sigma/trackers/track"
)

// The simulator against the library's codec: login, heartbeat and a report
// decode as sent, and a command reaches the tracker.
func TestTrackerAgainstCodec(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	nc, err := tk103.NewCodec(nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan *track.Message, 8)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		codec, r := nc(), bufio.NewReader(c)
		for {
			m, err := codec.Decode(r)
			if err != nil {
				close(got)
				return
			}
			if len(m.Reply) > 0 {
				_, _ = c.Write(m.Reply)
			}
			if len(m.Records) > 0 {
				b, _, err := codec.Command(m.IMEI, map[string]any{"action": "locate"}, 1)
				if err == nil {
					_, _ = c.Write(b)
				}
			}
			got <- m
		}
	}()
	tr, err := tk103sim.Dial(ln.Addr().String(), "359586015829802")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Login(); err != nil {
		t.Fatal(err)
	}
	if err := tr.Heartbeat(); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	if err := tr.Report("help me", tk103sim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36, Heading: 90, Valid: true}); err != nil {
		t.Fatal(err)
	}
	var rep *track.Message
	for m := range got {
		if len(m.Records) > 0 {
			rep = m
			break
		}
	}
	if rep == nil || rep.IMEI != "359586015829802" {
		t.Fatalf("report: %+v", rep)
	}
	r := rep.Records[0]
	if r.Fix == nil || !r.Fix.Valid || !r.Fix.Time.Equal(at) || r.Fix.Lat < 12.9715 || r.Fix.Lat > 12.9717 || r.Fix.Lon < 77.5945 || r.Fix.Lon > 77.5947 ||
		r.Fix.Speed == nil || *r.Fix.Speed < 35.9 || *r.Fix.Speed > 36.1 {
		t.Fatalf("fix %+v", r.Fix)
	}
	if len(r.Alerts) != 1 || r.Alerts[0].Type != "emergency" {
		t.Fatalf("alerts %+v", r.Alerts)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(tr.Commands()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cmds := tr.Commands(); len(cmds) != 1 || cmds[0] != "B" {
		t.Fatalf("commands %q", cmds)
	}
}
