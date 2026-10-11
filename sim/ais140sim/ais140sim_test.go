// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package ais140sim_test

import (
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/ais140sim"
	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers/ais140"
)

// The checksum is the XOR of the characters, two upper-case hex digits.
func TestChecksum(t *testing.T) {
	if got := ais140sim.Checksum("860000000000011,1234,GET,001"); got != "71" {
		t.Fatalf("checksum %s, want 71", got)
	}
}

// The device's login and data packets decode with the library's codec,
// checksums verified.
func TestDeviceAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, ais140.NewCodec, []byte(`{"checksum":"xor"}`))
	d, err := ais140sim.Dial(addr, "860000000000011", "MH12AB3456")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Send(d.Login()); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	if err := d.Send(d.Normal(ais140sim.Fix{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36, Heading: 90, Sats: 8, Valid: true, Ignition: true, Power: true})); err != nil {
		t.Fatal(err)
	}
	m, f := codectest.NextFix(t, msgs)
	if m.IMEI != "860000000000011" || !f.Valid || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) {
		t.Fatalf("%s %+v", m.IMEI, f)
	}
}

// The vendor layouts decode with the library's codec, odometer included.
func TestVendorLayoutsAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, ais140.NewCodec, []byte(`{"checksum":"xor"}`))
	d, err := ais140sim.Dial(addr, "860000000000011", "MH12AB3456")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	for _, v := range ais140sim.Vendors {
		if err := d.Send(d.NormalVendor(v, ais140sim.Fix{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36, Sats: 8, Valid: true, Power: true, MainV: 12.6, BatteryV: 4.1, GSM: 20}, 1234.5)); err != nil {
			t.Fatal(err)
		}
		m, f := codectest.NextFix(t, msgs)
		if m.IMEI != "860000000000011" || !f.Valid || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || m.Records[0].Status["odometer"] != 1234500.0 {
			t.Fatalf("%s: %s %+v %v", v, m.IMEI, f, m.Records[0].Status)
		}
	}
}
