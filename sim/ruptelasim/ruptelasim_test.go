// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package ruptelasim_test

import (
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
