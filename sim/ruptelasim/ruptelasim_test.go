// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package ruptelasim_test

import (
	"bufio"
	"bytes"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers-sim/sim/ruptelasim"
	"github.com/cB-Sigma/trackers/ruptela"
)

// The identification packet is accepted, and records and extended records
// decode with the library's codec.
func TestTrackerAgainstCodec(t *testing.T) {
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	rec := ruptelasim.Rec{Time: at, Lat: 12.9716, Lon: 77.5946, Alt: 900, Angle: 90, Speed: 36, Sats: 8, HDOP: 0.9,
		IO1: map[uint16]uint64{5: 1}, IO2: map[uint16]uint64{29: 12400}}
	// Count and "records left" flag, then one record.
	if p := ruptelasim.RecordsPayload(false, true, rec); len(p) < 25 || p[0] != 1 || p[1] != 1 {
		t.Fatalf("payload % x", p)
	}
	addr, msgs := codectest.Serve(t, ruptela.NewCodec, nil)
	tr, err := ruptelasim.Dial(addr, 860000000000011)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Identify(); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []bool{false, true} {
		if err := tr.Records(ext, false, rec); err != nil {
			t.Fatal(err)
		}
		m, f := codectest.NextFix(t, msgs)
		if m.IMEI != "860000000000011" || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) ||
			f.Speed == nil || *f.Speed != 36 {
			t.Fatalf("ext %v: %s %+v", ext, m.IMEI, f)
		}
	}
}

// Trouble codes decode one record each and are acknowledged.
func TestTroubleCodesAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, ruptela.NewCodec, nil)
	tr, err := ruptelasim.Dial(addr, 860000000000011)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	if err := tr.TroubleCodes(ruptelasim.DTC{Source: 0xFF, Time: at, Lat: 12.9716, Lon: 77.5946, Code: "P0301"},
		ruptelasim.DTC{Source: 0xFF, Time: at, History: true, Code: "P0420"}); err != nil {
		t.Fatal(err)
	}
	m, f := codectest.NextFix(t, msgs)
	if len(m.Records) != 2 || m.Records[0].Status["dtc"] != "P0301" || !m.Records[1].History || !f.Time.Equal(at) {
		t.Fatalf("%+v", m.Records)
	}
}

// A camera file is fetched packet by packet: the server asks for packet 0,
// the codec asks for each next one, and the last packet's message carries
// the file. The version request is answered with command 3.
func TestFilesAgainstCodec(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	tr, err := ruptelasim.Dial(ln.Addr().String(), 860000000000011)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	pic := []byte("\xFF\xD8 a picture of more than one packet \xFF\xD9")
	tr.PacketSize = 10
	tr.AddFile(0, "5DDA1FAD", pic)
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	nc, _ := ruptela.NewCodec(nil)
	codec := nc()
	frame, answers, err := codec.Command("860000000000011", map[string]any{"photo": map[string]any{"name": "5DDA1FAD"}}, 1)
	if err != nil || !answers {
		t.Fatal(err)
	}
	ver, _, _ := codec.Command("860000000000011", map[string]any{"getVersion": map[string]any{}}, 2)
	if _, err := c.Write(append(ver, frame...)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	gotVersion := false
	for {
		m, err := codec.Decode(br)
		if err != nil {
			t.Fatalf("%v (version %v)", err, gotVersion)
		}
		if m.Details["firmware"] == "00.02.24.00" {
			gotVersion = true
		}
		if len(m.Reply) > 0 {
			_, _ = c.Write(m.Reply)
		}
		if p, ok := ruptela.PictureOf(m); ok {
			if !bytes.Equal(p.Data, pic) || p.Name != "5DDA1FAD" || !gotVersion {
				t.Fatalf("%q %v", p.Data, gotVersion)
			}
			break
		}
	}
	if b := tr.Binaries(); len(b) != 1+(len(pic)+9)/10 || b[0].Cmd != ruptela.GetVersion {
		t.Fatalf("%d binary commands", len(b))
	}
}

// The interval template's setcfg command and the named commands are
// answered as the SMS command list shows.
func TestAnswers(t *testing.T) {
	for cmd, want := range map[string]string{"setcfg 1000 60": "Configuration parameter(s) was set!", "reset": "Resetting device.", "coords": "lat. 12.9716000"} {
		if got := ruptelasim.Answer(cmd, time.Now()); !strings.Contains(got, want) {
			t.Errorf("%s: %q", cmd, got)
		}
	}
}
