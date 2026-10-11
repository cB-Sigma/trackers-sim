// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package osmandsim_test

import (
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers-sim/sim/osmandsim"
	"github.com/cB-Sigma/trackers/osmand"
)

// The phone's form POST and query GET decode with the library's codec,
// which answers 200.
func TestPhoneAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, osmand.NewCodec, nil)
	ph, err := osmandsim.Dial(addr, "phone-demo-1")
	if err != nil {
		t.Fatal(err)
	}
	defer ph.Close()
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	pos := osmandsim.Position{Time: at, Lat: 12.9716, Lon: 77.5946, SpeedKnots: 10, Bearing: 90, Accuracy: 5, Battery: 80}
	for _, send := range []func(osmandsim.Position) (int, error){ph.Send, ph.SendQuery} {
		code, err := send(pos)
		if err != nil || code != 200 {
			t.Fatalf("status %d %v", code, err)
		}
		m, f := codectest.NextFix(t, msgs)
		if m.IMEI != "phone-demo-1" || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) {
			t.Fatalf("%s %+v", m.IMEI, f)
		}
	}
}
