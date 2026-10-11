// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package galileoskysim_test

import (
	"bufio"
	"net"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/galileoskysim"
	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers/galileosky"
	"github.com/cB-Sigma/trackers/track"
)

// The head packet and records decode with the library's codec, and each
// packet is confirmed.
func TestTrackerAgainstCodec(t *testing.T) {
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	addr, msgs := codectest.Serve(t, galileosky.NewCodec, nil)
	tr, err := galileoskysim.Dial(addr, "868345032042426")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Head(); err != nil {
		t.Fatal(err)
	}
	r := galileoskysim.Rec{Time: at, Lat: 12.9716, Lon: -77.5946, Speed: 36.5, Heading: 90, Alt: 920, Sats: 9, HDOP: 0.9,
		Status: 1 << 9, Power: 12.6, Battery: 4.1, Odometer: 1234}
	r2 := r
	r2.Time = at.Add(time.Minute)
	if err := tr.Records(true, r, r2); err != nil {
		t.Fatal(err)
	}
	m, f := codectest.NextFix(t, msgs)
	if m.IMEI != "868345032042426" || len(m.Records) != 2 || !m.Records[0].History || !f.Time.Equal(at) ||
		!codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, -77.5946) || *f.Speed != 36.5 || *f.Sats != 9 ||
		m.Records[0].Status["ignition"] != true || m.Records[0].Status["externalVoltage"] != 12.6 {
		t.Fatalf("%+v %+v", m, f)
	}
}

// Commands from the codec are answered with their number, which the codec
// reads as the correlation.
func TestCommands(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	nc, err := galileosky.NewCodec(nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan *track.Message, 16)
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
			_, _ = c.Write(m.Reply)
			if m.Login {
				for i, cmd := range []any{map[string]any{"action": "engineStop"}, map[string]any{"action": "locate"}, "Bogus"} {
					b, _, err := codec.Command(m.IMEI, cmd, uint32(100+i))
					if err == nil {
						_, _ = c.Write(b)
					}
				}
			}
			got <- m
		}
	}()
	tr, err := galileoskysim.Dial(ln.Addr().String(), "868345032042426")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Head(); err != nil {
		t.Fatal(err)
	}
	answers := map[string]*track.Message{}
	deadline := time.After(5 * time.Second)
	for len(answers) < 3 {
		select {
		case m := <-got:
			if m.Response != nil {
				answers[m.Correlation] = m
			}
		case <-deadline:
			t.Fatalf("answers %v", answers)
		}
	}
	if a := answers["100"]; *a.Response != "OUT(3..0) = 0000" || a.Failed {
		t.Fatalf("%+v", a)
	}
	if a := answers["101"]; a.Failed || a.Response == nil {
		t.Fatalf("%+v", a)
	}
	if a := answers["102"]; !a.Failed {
		t.Fatalf("%+v", a)
	}
	if c := tr.Commands(); len(c) != 3 || c[0] != "Out 0,0" || c[1] != "Status" {
		t.Fatalf("%q", c)
	}
	// A record after the answers is still confirmed.
	if err := tr.Records(false, galileoskysim.Rec{Time: time.Now(), Lat: 1, Lon: 1, Sats: 5}); err != nil {
		t.Fatal(err)
	}
}
