// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package atracksim_test

import (
	"bufio"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/atracksim"
	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers/atrack"
	"github.com/cB-Sigma/trackers/track"
)

// The tracker's keep-alive and positions, binary and ASCII, decode with
// the library's codec and are answered.
func TestTrackerAgainstCodec(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		addr, msgs := codectest.Serve(t, atrack.NewCodec, nil)
		tr, err := atracksim.Dial(addr, "352964052096642")
		if err != nil {
			t.Fatal(err)
		}
		tr.ASCII = ascii
		if err := tr.Login(); err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
		if err := tr.Report(atracksim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36, Heading: 90, Sats: 9}); err != nil {
			t.Fatal(err)
		}
		m, f := codectest.NextFix(t, msgs)
		if m.IMEI != "352964052096642" || !f.Valid || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) ||
			*f.Speed != 36 || *f.Sats != 9 || m.Records[0].Status["externalVoltage"] != 12.4 || len(m.Warnings) != 0 {
			t.Fatalf("ascii %v: %+v %+v", ascii, m, f)
		}
		_ = tr.Close()
	}
}

// A command sent with the codec reaches the tracker, whose tagged answer
// decodes as the response with the command's correlation; AT$GPOS is
// answered with a position.
func TestCommandsAgainstCodec(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	nc, err := atrack.NewCodec(nil)
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
		sent := false
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
			if len(m.Records) > 0 && !sent {
				sent = true
				for i, cmd := range []any{map[string]any{"action": "interval", "seconds": 30}, map[string]any{"text": "AT$INFO=?"}, map[string]any{"action": "locate"}} {
					b, _, _ := codec.Command(m.IMEI, cmd, uint32(7+i))
					_, _ = c.Write(b)
				}
			}
			got <- m
		}
	}()
	tr, err := atracksim.Dial(ln.Addr().String(), "352964052096642")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Report(atracksim.Pos{Time: time.Now(), Lat: 1, Lon: 2, Sats: 5}); err != nil {
		t.Fatal(err)
	}
	var answers []*track.Message
	positions := 0
	deadline := time.After(5 * time.Second)
	for len(answers) < 2 || positions < 2 {
		select {
		case m := <-got:
			if m.Response != nil {
				answers = append(answers, m)
			} else if len(m.Records) > 0 {
				positions++
			}
		case <-deadline:
			t.Fatalf("answers %d positions %d; commands %q", len(answers), positions, tr.Commands())
		}
	}
	if answers[0].Correlation != "7" || *answers[0].Response != "OK" || answers[1].Correlation != "8" || answers[1].Details["model"] != "AX7" {
		t.Fatalf("%+v %+v", answers[0], answers[1])
	}
	if cmds := tr.Commands(); len(cmds) != 3 || cmds[0] != "AT$TRAC+7=1,30" || cmds[2] != "AT$GPOS+9" {
		t.Fatalf("%q", cmds)
	}
}
