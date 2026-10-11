// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package h02sim_test

import (
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/h02sim"
	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers/h02"
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
