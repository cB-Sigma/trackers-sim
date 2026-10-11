// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package meitracksim_test

import (
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
