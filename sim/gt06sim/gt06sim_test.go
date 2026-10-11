// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package gt06sim_test

import (
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/gt06sim"
	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers/gt06"
)

// The tracker logs in and its location frame decodes with the library's
// codec.
func TestTrackerAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, gt06.NewCodec, nil)
	tr, err := gt06sim.Dial(addr, "860000000000011")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Login(); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	if err := tr.Location(gt06sim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36, Course: 90, Sats: 8, Valid: true}, false); err != nil {
		t.Fatal(err)
	}
	m, f := codectest.NextFix(t, msgs)
	if m.IMEI != "860000000000011" || !f.Valid || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) ||
		f.Speed == nil || *f.Speed != 36 {
		t.Fatalf("%s %+v", m.IMEI, f)
	}
}
