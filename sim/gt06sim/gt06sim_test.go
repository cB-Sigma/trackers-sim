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

// The time request is answered with UTC; information, heartbeat and 4G
// frames decode with the library's codec.
func TestTimeInfoAnd4G(t *testing.T) {
	addr, msgs := codectest.Serve(t, gt06.NewCodec, nil)
	tr, err := gt06sim.Dial(addr, "860000000000012")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.Login(); err != nil {
		t.Fatal(err)
	}
	<-msgs
	got, err := tr.TimeRequest()
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(got); d < -2*time.Second || d > 2*time.Second {
		t.Fatalf("server time %v", got)
	}
	<-msgs
	next := func() map[string]any {
		t.Helper()
		select {
		case m := <-msgs:
			if len(m.Records) != 1 {
				t.Fatalf("%+v", m)
			}
			return m.Records[0].Status
		case <-time.After(5 * time.Second):
			t.Fatal("nothing decoded")
		}
		return nil
	}
	if err := tr.ExternalVoltage(12.6); err != nil {
		t.Fatal(err)
	}
	if st := next(); st["externalVoltage"] != 12.6 {
		t.Fatalf("voltage %v", st)
	}
	if err := tr.StatusText("ALM1=C4;DYD=01;"); err != nil {
		t.Fatal(err)
	}
	if st := next(); st["terminalStatus"] != "ALM1=C4;DYD=01;" {
		t.Fatalf("status text %v", st)
	}
	if err := tr.Door(true); err != nil {
		t.Fatal(err)
	}
	if st := next(); st["doorOpen"] != true {
		t.Fatalf("door %v", st)
	}
	if err := tr.IDs("404450123456789", "89914501234567890123"); err != nil {
		t.Fatal(err)
	}
	if st := next(); st["imsi"] != "404450123456789" || st["iccid"] != "89914501234567890123" {
		t.Fatalf("ids %v", st)
	}
	if err := tr.Heartbeat(0x46, 4.15, 4); err != nil {
		t.Fatal(err)
	}
	if st := next(); st["batteryVoltage"] != 4.15 || st["ignition"] != true || st["gsm"] != 4 {
		t.Fatalf("heartbeat %v", st)
	}
	at := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	if err := tr.Location4G(gt06sim.Pos{Time: at, Lat: 28.6139, Lon: 77.209, Speed: 40, Sats: 9, Valid: true}, 404, 10, gt06sim.Cell4G{LAC: 0x1234, CID: 0x0ABCDEF1}, true); err != nil {
		t.Fatal(err)
	}
	m, f := codectest.NextFix(t, msgs)
	r := m.Records[0]
	if !f.Time.Equal(at) || !codectest.Near(f.Lat, 28.6139) || !r.History || r.Network.Cells[0].CID != 0x0ABCDEF1 || r.Network.Cells[0].MCC != 404 {
		t.Fatalf("0xA0 %+v %+v", f, r)
	}
	if err := tr.LBS4G(at, 404, 10, []gt06sim.Cell4G{{LAC: 1, CID: 2, RSSI: -70}, {LAC: 1, CID: 3, RSSI: -80}}); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-msgs:
		if n := m.Records[0].Network; len(n.Cells) != 2 || *n.Cells[1].RSSI != -80 {
			t.Fatalf("0xA1 %+v", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("0xA1 not decoded")
	}
}
