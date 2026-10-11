// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package meitracksim_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers-sim/sim/meitracksim"
	"github.com/cB-Sigma/trackers/meitrack"
)

// An AAA report decodes with the library's codec.
func TestTrackerAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, meitrack.NewCodec, nil)
	tr, err := meitracksim.Dial(addr, "860000000000011")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	if err := tr.Report(35, meitracksim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36, Course: 90, Sats: 8, Alt: 900, Valid: true}); err != nil {
		t.Fatal(err)
	}
	m, f := codectest.NextFix(t, msgs)
	if m.IMEI != "860000000000011" || !f.Valid || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) {
		t.Fatalf("%s %+v", m.IMEI, f)
	}
}

// A picture taken (event 39) is fetched by the codec (fetchPictures): the
// tracker sends eight packets per D00 request and the codec reassembles
// the picture.
func TestPictureAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, meitrack.NewCodec, []byte(`{"fetchPictures": true}`))
	tr, err := meitracksim.Dial(addr, "860000000000011")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.PacketSize = 200
	pic := meitracksim.SamplePicture(3000) // 15 packets: two D00 requests
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	if err := tr.TakePicture("1006093000_C1E39.jpg", pic, meitracksim.Pos{Time: at, Lat: 12.97, Lon: 77.59, Valid: true}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-msgs:
			if p, ok := meitrack.PictureOf(m); ok {
				if p.Name != "1006093000_C1E39.jpg" || !bytes.Equal(p.Data, pic) {
					t.Fatalf("picture %q %d bytes", p.Name, len(p.Data))
				}
				if n := len(tr.Commands()); n != 2 {
					t.Fatalf("%d D00 requests: %v", n, tr.Commands())
				}
				return
			}
		case <-deadline:
			t.Fatalf("no picture; commands %v", tr.Commands())
		}
	}
}

// A CCC batch decodes and is confirmed with its count.
func TestCCCAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, meitrack.NewCodec, nil)
	tr, err := meitracksim.Dial(addr, "860000000000011")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	recs := []meitracksim.CCCRecord{
		{Event: 35, Pos: meitracksim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 30, Sats: 9, Valid: true}, Battery: 410, External: 1350, MCC: 404, MNC: 45, LAC: 0x1A2B, CID: 0x3C4D},
		{Event: 35, Pos: meitracksim.Pos{Time: at.Add(time.Minute), Lat: 12.9720, Lon: 77.5950, Sats: 9, Valid: true}, Battery: 410, External: 1350},
	}
	if err := tr.BatchCCC(recs); err != nil {
		t.Fatal(err)
	}
	m, f := codectest.NextFix(t, msgs)
	if len(m.Records) != 2 || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || m.Records[0].Status["externalVoltage"] != 13.5 {
		t.Fatalf("%+v %+v", m.Records, f)
	}
	for i := 0; i < 50 && len(tr.Commands()) == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if c := tr.Commands(); len(c) != 1 || c[0].Text != "CCC,2" {
		t.Fatalf("confirmation %v", c)
	}
}
