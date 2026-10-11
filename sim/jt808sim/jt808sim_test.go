// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package jt808sim_test

import (
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers-sim/sim/jt808sim"
	"github.com/cB-Sigma/trackers/jt808"
)

// The terminal registers, authenticates and reports a location the
// library's codec decodes, in the 2013 and the 2019 header.
func TestTerminalAgainstCodec(t *testing.T) {
	for _, v2019 := range []bool{false, true} {
		addr, msgs := codectest.Serve(t, jt808.NewCodec, nil)
		tr, err := jt808sim.Dial(addr, "000000000011", v2019)
		if err != nil {
			t.Fatal(err)
		}
		if err := tr.Login(); err != nil {
			t.Fatalf("2019 %v: %v", v2019, err)
		}
		at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
		if err := tr.Location(jt808sim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36, Course: 90, Sats: 8, Valid: true, ACC: true}); err != nil {
			t.Fatal(err)
		}
		_, f := codectest.NextFix(t, msgs)
		if !f.Valid || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) {
			t.Fatalf("2019 %v: %+v", v2019, f)
		}
		_ = tr.Close()
	}
}
