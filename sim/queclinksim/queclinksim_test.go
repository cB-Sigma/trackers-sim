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

// In HEX mode the heartbeat is answered with +SACK:GTHBD and every frame
// decodes with the library's codec: reports with their positions and
// items, events with their alerts, acknowledgements correlated.
func TestHexTrackerAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, queclink.NewCodec, nil)
	tr, err := queclinksim.Dial(addr, "860000000000011", "gv300")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.Hex = true
	if err := tr.Heartbeat(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)
	p := queclinksim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36.5, Heading: 90, Alt: 900, HDOP: 1, Mileage: 1234.5}
	if err := tr.Report("GTSOS", "0", p, false); err != nil {
		t.Fatal(err)
	}
	m, f := codectest.NextFix(t, msgs)
	if m.IMEI != "860000000000011" || m.Kind != "emergency" || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) ||
		*f.Speed != 36.5 || *f.Alt != 900 || m.Details["format"] != "hex" {
		t.Fatalf("%s %s %+v %v", m.IMEI, m.Kind, f, m.Details)
	}
	p2 := p
	p2.Lat, p2.Time = 12.98, at.Add(30*time.Second)
	if err := tr.Fixed([]queclinksim.Pos{p, p2}, 12400, 80, "220100"); err != nil {
		t.Fatal(err)
	}
	m, _ = codectest.NextFix(t, msgs)
	st := m.Records[len(m.Records)-1].Status
	if len(m.Records) != 2 || m.Details["report"] != "GTFRI" || st["odometer"] != 1234.5 || st["batteryLevel"] != 80 || st["ignition"] != true || st["externalVoltage"] != 12.4 {
		t.Fatalf("GTFRI %v %v", m.Details, st)
	}
	if err := tr.Event("GTBPL", "3.52", p); err != nil {
		t.Fatal(err)
	}
	m, _ = codectest.NextFix(t, msgs)
	if r := m.Records[0]; len(r.Alerts) != 1 || r.Alerts[0].Type != "low-battery" || r.Status["batteryVoltage"] != 3.52 {
		t.Fatalf("GTBPL %+v", r)
	}
	if err := tr.Ack("GTRTO", "RTL", "0004"); err != nil {
		t.Fatal(err)
	}
	select {
	case m = <-msgs:
	case <-time.After(5 * time.Second):
		t.Fatal("no acknowledgement decoded")
	}
	if m.Correlation != "GTRTO/0004" || m.Details["subCommand"] != "RTL" {
		t.Fatalf("ack %q %v", m.Correlation, m.Details)
	}
}
