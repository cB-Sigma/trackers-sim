// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package wialonsim_test

import (
	"bufio"
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers-sim/sim/wialonsim"
	"github.com/cB-Sigma/trackers/track"
	"github.com/cB-Sigma/trackers/wialon"
)

// Version 2.0 and 1.1 trackers against the library's codec: login, ping,
// short, extended and black box data, and a driver message.
func TestTrackerAgainstCodec(t *testing.T) {
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	for _, v11 := range []bool{false, true} {
		addr, msgs := codectest.Serve(t, wialon.NewCodec, nil)
		tr, err := wialonsim.Dial(addr, "860000000000021")
		if err != nil {
			t.Fatal(err)
		}
		tr.V11 = v11
		if err := tr.Login(); err != nil {
			t.Fatal(v11, err)
		}
		if err := tr.Ping(); err != nil {
			t.Fatal(err)
		}
		p := wialonsim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36, Heading: 90, Alt: 920, Sats: 9, HDOP: 0.9,
			Params: map[string]any{"SOS": int64(0), "fuel": 41.5}}
		if err := tr.Short(p); err != nil {
			t.Fatal(err)
		}
		m, f := codectest.NextFix(t, msgs)
		if m.IMEI != "860000000000021" || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) || *f.Speed != 36 {
			t.Fatalf("v11 %v: %+v", v11, f)
		}
		if err := tr.Data(p); err != nil {
			t.Fatal(err)
		}
		m, _ = codectest.NextFix(t, msgs)
		if m.Records[0].Status["params"].(map[string]any)["fuel"] != 41.5 || *m.Records[0].Fix.HDOP != 0.9 {
			t.Fatalf("%+v", m.Records[0])
		}
		if err := tr.BlackBox(p, p); err != nil {
			t.Fatal(err)
		}
		if err := tr.Message("on my way"); err != nil {
			t.Fatal(err)
		}
		_ = tr.Close()
	}
}

// Commands from the codec reach the tracker; a custom command is answered
// with #M#, which the codec reads as the answer.
func TestCommands(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	nc, err := wialon.NewCodec(nil)
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
				for i, cmd := range []any{map[string]any{"message": "hello driver"}, map[string]any{"text": "getinfo"},
					map[string]any{"firmware": "AQID"}, map[string]any{"snapshot": true}} {
					b, _, err := codec.Command(m.IMEI, cmd, uint32(i))
					if err == nil {
						_, _ = c.Write(b)
					}
				}
			}
			got <- m
		}
	}()
	tr, err := wialonsim.Dial(ln.Addr().String(), "860000000000022")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Login(); err != nil {
		t.Fatal(err)
	}
	var answer, image bool
	deadline := time.After(5 * time.Second)
	for !answer || !image {
		select {
		case m := <-got:
			if m.Response != nil && *m.Response == "OK getinfo" {
				answer = true
			}
			if len(m.Records) > 0 && m.Records[0].Status["image"] != nil {
				image = bytes.Equal(m.Records[0].Status["image"].([]byte), tr.Picture)
			}
		case <-deadline:
			t.Fatalf("answer %v image %v", answer, image)
		}
	}
	if ms := tr.Messages(); len(ms) != 1 || ms[0] != "hello driver" {
		t.Fatalf("%q", ms)
	}
	if c := tr.Commands(); len(c) != 1 || c[0] != "getinfo" {
		t.Fatalf("%q", c)
	}
	if u := tr.Uploads(); len(u) != 1 || !bytes.Equal(u[0], []byte{1, 2, 3}) {
		t.Fatalf("%v", u)
	}
}
