// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package queclinksim_test

import (
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers-sim/sim/queclinksim"
	"github.com/cB-Sigma/trackers/queclink"
)

// A heartbeat is acknowledged and a +RESP report decodes with the
// library's codec.
func TestTrackerAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, queclink.NewCodec, nil)
	tr, err := queclinksim.Dial(addr, "860000000000011", "gv300")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Heartbeat(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	if err := tr.Report("GTSOS", "0", queclinksim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36, Heading: 90, Alt: 900, HDOP: 1}, false); err != nil {
		t.Fatal(err)
	}
	m, f := codectest.NextFix(t, msgs)
	if m.IMEI != "860000000000011" || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) {
		t.Fatalf("%s %+v", m.IMEI, f)
	}
}
