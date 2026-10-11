// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package interop

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/ais140sim"
	"github.com/cB-Sigma/trackers-sim/sim/gt06sim"
	"github.com/cB-Sigma/trackers-sim/sim/h02sim"
	"github.com/cB-Sigma/trackers-sim/sim/jt808sim"
	"github.com/cB-Sigma/trackers-sim/sim/meitracksim"
	"github.com/cB-Sigma/trackers-sim/sim/osmandsim"
	"github.com/cB-Sigma/trackers-sim/sim/queclinksim"
	"github.com/cB-Sigma/trackers-sim/sim/ruptelasim"
	"github.com/cB-Sigma/trackers-sim/sim/teltonikasim"
	"github.com/cB-Sigma/trackers-sim/sim/tk103sim"
)

// track is the drive every simulator reports: three points around
// Bengaluru with different speeds and courses, a few seconds apart (in the
// past, so no server takes them for future fixes).
func drive() ([]sent, []time.Time) {
	base := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	return []sent{
		{Lat: 12.971600, Lon: 77.594600, KmH: 36, Course: 90},
		{Lat: 12.972100, Lon: 77.595300, KmH: 54, Course: 135},
		{Lat: -12.972700, Lon: -77.596100, KmH: 0, Course: 270}, // the other hemispheres
	}, []time.Time{base, base.Add(5 * time.Second), base.Add(10 * time.Second)}
}

// reported runs one simulator against Traccar: report sends the drive and
// alarm (if any), and the test checks Traccar's positions, then the alarm
// event.
func reported(t *testing.T, uniqueID string, tol float64, alarm string, report func(ps []sent, at []time.Time)) {
	t.Helper()
	needTraccar(t)
	id := device(t, uniqueID)
	after := lastID(t, id)
	ps, at := drive()
	report(ps, at)
	n := len(ps)
	if alarm != "" {
		n++
	}
	got := waitPositions(t, id, after, n)
	checkPositions(t, got[:len(ps)], ps, tol)
	if alarm != "" {
		waitAlarm(t, id, alarm)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestPositionsGT06(t *testing.T) {
	const imei = "359710049090001"
	reported(t, imei, 1e-5, "sos", func(ps []sent, at []time.Time) {
		tr, err := gt06sim.Dial(tcpAddr["gt06"], imei)
		must(t, err)
		defer tr.Close()
		must(t, tr.Login())
		for i, p := range ps {
			must(t, tr.Location(gt06sim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: int(p.KmH), Course: int(p.Course), Sats: 9, Valid: true}, false))
		}
		must(t, tr.Alarm(gt06sim.Pos{Time: at[2].Add(time.Second), Lat: ps[2].Lat, Lon: ps[2].Lon, Sats: 9, Valid: true}, 0x01))
	})
}

func TestPositionsTeltonika(t *testing.T) {
	const imei = "352093081420001"
	reported(t, imei, 1e-6, "", func(ps []sent, at []time.Time) {
		tr, ok, err := teltonikasim.Dial(tcpAddr["teltonika"], imei)
		must(t, err)
		defer tr.Close()
		if !ok {
			t.Fatal("Traccar refused the IMEI")
		}
		for i, p := range ps {
			_, err := tr.Send(i == 1, teltonikasim.Record{Time: at[i], Lat: p.Lat, Lon: p.Lon, Alt: 900, Angle: uint16(p.Course), Sats: 9, Speed: uint16(p.KmH)})
			must(t, err)
		}
	})
}

// Teltonika over UDP: each datagram carries the IMEI; Traccar answers with
// the packet ID, AVL ID and the number of records accepted.
func TestPositionsTeltonikaUDP(t *testing.T) {
	const imei = "352093081420002"
	reported(t, imei, 1e-6, "", func(ps []sent, at []time.Time) {
		c, err := net.Dial("udp", udpAddr["teltonika"])
		must(t, err)
		defer c.Close()
		for i, p := range ps {
			data := teltonikasim.AVL(false, []teltonikasim.Record{{Time: at[i], Lat: p.Lat, Lon: p.Lon, Alt: 900, Angle: uint16(p.Course), Sats: 9, Speed: uint16(p.KmH)}})
			_, err := c.Write(teltonikasim.UDPPacket(uint16(0xCAFE+i), byte(i+1), imei, data))
			must(t, err)
			ack := make([]byte, 64)
			must(t, c.SetReadDeadline(time.Now().Add(5*time.Second)))
			n, err := c.Read(ack)
			must(t, err)
			// length, packet ID, 0x01, AVL ID, records accepted
			if n != 7 || ack[5] != byte(i+1) || ack[6] != 1 {
				t.Fatalf("Traccar's UDP answer % X", ack[:n])
			}
		}
	})
}

// H02 over UDP.
func TestPositionsH02UDP(t *testing.T) {
	const id = "4210000002"
	reported(t, id, 1e-4, "", func(ps []sent, at []time.Time) {
		tr, err := h02sim.DialUDP(udpAddr["h02"], id)
		must(t, err)
		defer tr.Close()
		for i, p := range ps {
			must(t, tr.Position(h02sim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH / 1.852, Course: int(p.Course), Valid: true}))
		}
	})
}

func TestPositionsTK103(t *testing.T) {
	const imei = "359586015820001"
	reported(t, imei, 1e-4, "sos", func(ps []sent, at []time.Time) {
		tr, err := tk103sim.Dial(tcpAddr["tk103"], imei)
		must(t, err)
		defer tr.Close()
		must(t, tr.Login())
		for i, p := range ps {
			must(t, tr.Report("tracker", tk103sim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH, Heading: p.Course, Valid: true}))
		}
		must(t, tr.Report("help me", tk103sim.Pos{Time: at[2].Add(time.Second), Lat: ps[2].Lat, Lon: ps[2].Lon, Valid: true}))
		time.Sleep(500 * time.Millisecond)
	})
}

func TestPositionsQueclink(t *testing.T) {
	const imei = "862106020000001"
	reported(t, imei, 1e-5, "sos", func(ps []sent, at []time.Time) {
		tr, err := queclinksim.Dial(tcpAddr["queclink"], imei, "gv300")
		must(t, err)
		defer tr.Close()
		for i, p := range ps {
			must(t, tr.Fixed([]queclinksim.Pos{{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH, Heading: int(p.Course), Alt: 900, HDOP: 1}}, 12400, 90, "220100"))
		}
		must(t, tr.Report("GTSOS", "0", queclinksim.Pos{Time: at[2].Add(time.Second), Lat: ps[2].Lat, Lon: ps[2].Lon, HDOP: 1}, false))
		time.Sleep(500 * time.Millisecond)
	})
}

func TestPositionsMeitrack(t *testing.T) {
	const imei = "864507030000001"
	reported(t, imei, 1e-5, "sos", func(ps []sent, at []time.Time) {
		tr, err := meitracksim.Dial(tcpAddr["meitrack"], imei)
		must(t, err)
		defer tr.Close()
		for i, p := range ps {
			must(t, tr.Report(35, meitracksim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: int(p.KmH), Course: int(p.Course), Sats: 9, Valid: true}))
		}
		must(t, tr.Report(1, meitracksim.Pos{Time: at[2].Add(time.Second), Lat: ps[2].Lat, Lon: ps[2].Lon, Sats: 9, Valid: true}))
		time.Sleep(500 * time.Millisecond)
	})
}

func TestPositionsRuptela(t *testing.T) {
	const imei = "863071010000001"
	reported(t, imei, 1e-6, "", func(ps []sent, at []time.Time) {
		n, _ := strconv.ParseUint(imei, 10, 64)
		tr, err := ruptelasim.Dial(tcpAddr["ruptela"], n)
		must(t, err)
		defer tr.Close()
		for i, p := range ps {
			must(t, tr.Records(i == 1, false, ruptelasim.Rec{Time: at[i], Lat: p.Lat, Lon: p.Lon, Alt: 900, Angle: p.Course, Speed: int(p.KmH), Sats: 9, HDOP: 0.9}))
		}
	})
}

func TestPositionsJT808(t *testing.T) {
	const phone = "013912340001"
	reported(t, phone, 1e-6, "sos", func(ps []sent, at []time.Time) {
		tr, err := jt808sim.Dial(tcpAddr["jt808"], phone, false)
		must(t, err)
		defer tr.Close()
		must(t, tr.Login())
		for i, p := range ps {
			must(t, tr.Location(jt808sim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH, Course: int(p.Course), Sats: 9, Valid: true, ACC: true}))
		}
		must(t, tr.Location(jt808sim.Pos{Time: at[2].Add(time.Second), Lat: ps[2].Lat, Lon: ps[2].Lon, Sats: 9, Valid: true, Alarm: 1}))
	})
}

func TestPositionsH02(t *testing.T) {
	const id = "4210000001"
	reported(t, id, 1e-4, "sos", func(ps []sent, at []time.Time) {
		tr, err := h02sim.Dial(tcpAddr["h02"], id)
		must(t, err)
		defer tr.Close()
		for i, p := range ps {
			must(t, tr.Position(h02sim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH / 1.852, Course: int(p.Course), Valid: true}))
		}
		must(t, tr.Position(h02sim.Pos{Time: at[2].Add(time.Second), Lat: ps[2].Lat, Lon: ps[2].Lon, Valid: true, Status: h02sim.StatusIdle &^ 0x02}))
		time.Sleep(500 * time.Millisecond)
	})
}

func TestPositionsOsmAnd(t *testing.T) {
	const id = "phone-interop-1"
	reported(t, id, 1e-6, "sos", func(ps []sent, at []time.Time) {
		p, err := osmandsim.Dial(tcpAddr["osmand"], id)
		must(t, err)
		defer p.Close()
		for i, s := range ps {
			send := p.Send
			if i == 1 {
				send = p.SendQuery
			}
			st, err := send(osmandsim.Position{Time: at[i], Lat: s.Lat, Lon: s.Lon, SpeedKnots: s.KmH / 1.852, Bearing: s.Course, Accuracy: 5, Battery: 80})
			must(t, err)
			if st != 200 {
				t.Fatalf("Traccar answered HTTP %d", st)
			}
		}
		st, err := p.Send(osmandsim.Position{Time: at[2].Add(time.Second), Lat: ps[2].Lat, Lon: ps[2].Lon, Alarm: "sos"})
		must(t, err)
		if st != 200 {
			t.Fatalf("Traccar answered HTTP %d", st)
		}
	})
}

func TestPositionsAIS140(t *testing.T) {
	const imei = "861693034600001"
	reported(t, imei, 1e-5, "sos", func(ps []sent, at []time.Time) {
		d, err := ais140sim.Dial(tcpAddr["ais140"], imei, "KA01SIM0001")
		must(t, err)
		defer d.Close()
		must(t, d.Send(d.Login()))
		for i, p := range ps {
			must(t, d.Send(d.Normal(ais140sim.Fix{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH, Heading: p.Course,
				Sats: 12, Valid: true, Ignition: true, Power: true, MainV: 12.4, BatteryV: 4.1, GSM: 25})))
		}
		must(t, d.Send(d.Emergency(true, at[2].Add(time.Second), ps[2].Lat, ps[2].Lon)))
		time.Sleep(500 * time.Millisecond)
	})
}
