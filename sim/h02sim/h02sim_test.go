// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package h02sim_test

import (
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/h02sim"
	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers/h02"
	"github.com/cB-Sigma/trackers/track"
)

// A V1 text frame and a 32-byte binary record both decode with the
// library's codec; the binary record takes 10-digit IDs only.
func TestFramesAgainstCodec(t *testing.T) {
	if h02sim.BinaryFrame("12345", h02sim.Pos{}) != nil {
		t.Fatal("a binary record for a 5-digit ID")
	}
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	p := h02sim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 20, Course: 90, Valid: true}
	if b := h02sim.BinaryFrame("4200000011", p); len(b) != 32 {
		t.Fatalf("binary record of %d bytes", len(b))
	}
	addr, msgs := codectest.Serve(t, h02.NewCodec, nil)
	tr, err := h02sim.Dial(addr, "4200000011")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	for _, send := range []func(h02sim.Pos) error{tr.Position, tr.Binary} {
		if err := send(p); err != nil {
			t.Fatal(err)
		}
		m, f := codectest.NextFix(t, msgs)
		if m.IMEI != "4200000011" || !f.Valid || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) {
			t.Fatalf("%s %+v", m.IMEI, f)
		}
	}
}

// VP1, cells (VP1 and V3), ALRM, BC history, the 45-byte binary record and
// the Y-model record decode with the library's codec.
func TestMoreFramesAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, h02.NewCodec, nil)
	tr, err := h02sim.Dial(addr, "4200000012")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	next := func() *track.Message {
		t.Helper()
		select {
		case m := <-msgs:
			return m
		case <-time.After(5 * time.Second):
			t.Fatal("nothing decoded")
		}
		return nil
	}
	now := time.Now().UTC().Truncate(time.Second)
	p := h02sim.Pos{Time: now, Lat: 19.076, Lon: 72.8777, Speed: 10, Course: 45, Valid: true}

	if err := tr.VP1(p); err != nil {
		t.Fatal(err)
	}
	if m := next(); m.Records[0].Fix == nil || !codectest.Near(m.Records[0].Fix.Lat, 19.076) {
		t.Fatalf("VP1 %+v", m)
	}
	if err := tr.VP1Cells(404, 20, []h02sim.Cell{{LAC: 9376, CID: 8532, Signal: 99}, {LAC: 9876, CID: 4357, Signal: 80}}); err != nil {
		t.Fatal(err)
	}
	if m := next(); len(m.Records[0].Network.Cells) != 2 || m.Records[0].Network.Cells[1].CID != 4357 {
		t.Fatalf("VP1 cells %+v", m.Records[0])
	}
	if err := tr.V3(404, 20, []h02sim.Cell{{LAC: 9350, CID: 4022, Signal: -88}}, 3.27); err != nil {
		t.Fatal(err)
	}
	if m := next(); len(m.Records[0].Network.Cells) != 1 || *m.Records[0].Network.Cells[0].RSSI != -88 || m.Records[0].Status["batteryVoltage"] != 3.27 {
		t.Fatalf("V3 %+v", m.Records[0])
	}
	if err := tr.Alarm(h02sim.AlarmSOS, p); err != nil {
		t.Fatal(err)
	}
	if m := next(); m.Kind != "emergency" || len(m.Records[0].Alerts) == 0 || m.Records[0].Alerts[0].Type != "emergency" {
		t.Fatalf("ALRM %+v", m.Records[0])
	}
	old := p
	old.Time = now.Add(-time.Hour)
	if err := tr.Blind([]h02sim.Pos{old, p}); err != nil {
		t.Fatal(err)
	}
	if m := next(); len(m.Records) != 2 || !m.Records[0].History || !m.Records[0].Fix.Time.Equal(old.Time) {
		t.Fatalf("BC %+v", m.Records)
	}
	if err := tr.LongBinary(p); err != nil {
		t.Fatal(err)
	}
	if m := next(); m.Records[0].Fix == nil || !m.Records[0].Fix.Time.Equal(now) {
		t.Fatalf("45-byte %+v", m)
	}
	if err := tr.Y(1601, 1024, 9, p); err != nil {
		t.Fatal(err)
	}
	if m := next(); m.IMEI != "4200000012" || m.Records[0].Status["analog1"] != 1601 || m.Records[0].Status["recordNumber"] != 9 ||
		!codectest.Near(m.Records[0].Fix.Lon, 72.8777) {
		t.Fatalf("Y %+v", m.Records[0])
	}
	// The connection stays in step after them.
	if err := tr.Heartbeat(3 * time.Second); err != nil {
		t.Fatal(err)
	}
}
