// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package interop

import (
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/atracksim"
	"github.com/cB-Sigma/trackers-sim/sim/calampsim"
	"github.com/cB-Sigma/trackers-sim/sim/eelinksim"
	"github.com/cB-Sigma/trackers-sim/sim/galileoskysim"
	"github.com/cB-Sigma/trackers-sim/sim/suntechsim"
	"github.com/cB-Sigma/trackers-sim/sim/watchsim"
	"github.com/cB-Sigma/trackers-sim/sim/wialonsim"
)

// The protocols added in v0.2.0, against the same Traccar.

func TestPositionsWialon(t *testing.T) {
	const imei = "866000000000101"
	reported(t, imei, 1e-4, "", func(ps []sent, at []time.Time) {
		tr, err := wialonsim.Dial(tcpAddr["wialon"], imei)
		must(t, err)
		defer tr.Close()
		must(t, tr.Login())
		for i, p := range ps {
			pos := wialonsim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH, Heading: p.Course, Alt: 900, Sats: 9, HDOP: 0.9}
			if i == 1 {
				must(t, tr.Short(pos))
			} else {
				must(t, tr.Data(pos))
			}
		}
	})
}

// Wialon IPS 1.1 login and data.
func TestPositionsWialon11(t *testing.T) {
	const imei = "866000000000102"
	reported(t, imei, 1e-4, "", func(ps []sent, at []time.Time) {
		tr, err := wialonsim.Dial(tcpAddr["wialon"], imei)
		must(t, err)
		defer tr.Close()
		tr.V11 = true
		must(t, tr.Login())
		for i, p := range ps {
			must(t, tr.Data(wialonsim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH, Heading: p.Course, Alt: 900, Sats: 9, HDOP: 0.9}))
		}
	})
}

func TestPositionsGalileosky(t *testing.T) {
	const imei = "868204000000101"
	reported(t, imei, 1e-5, "", func(ps []sent, at []time.Time) {
		tr, err := galileoskysim.Dial(tcpAddr["galileosky"], imei)
		must(t, err)
		defer tr.Close()
		must(t, tr.Head())
		for i, p := range ps {
			must(t, tr.Records(false, galileoskysim.Rec{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH, Heading: p.Course, Alt: 900, Sats: 9, HDOP: 0.9,
				Power: 12.4, Battery: 4.1}))
		}
	})
}

func TestPositionsSuntechUniversal(t *testing.T) {
	const id = "907000101"
	reported(t, id, 1e-5, "sos", func(ps []sent, at []time.Time) {
		tr, err := suntechsim.Dial(tcpAddr["suntech"], id, "")
		must(t, err)
		defer tr.Close()
		for i, p := range ps {
			must(t, tr.Status(suntechsim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH, Course: p.Course, Sats: 9, Valid: true}))
		}
		must(t, tr.Emergency(suntechsim.Pos{Time: at[2].Add(time.Second), Lat: ps[2].Lat, Lon: ps[2].Lon, Sats: 9, Valid: true}))
		time.Sleep(500 * time.Millisecond)
	})
}

func TestPositionsSuntechST300(t *testing.T) {
	const id = "907000102"
	reported(t, id, 1e-5, "sos", func(ps []sent, at []time.Time) {
		tr, err := suntechsim.Dial(tcpAddr["suntech"], id, "ST300")
		must(t, err)
		defer tr.Close()
		for i, p := range ps {
			must(t, tr.Status(suntechsim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH, Course: p.Course, Sats: 9, Valid: true}))
		}
		must(t, tr.Emergency(suntechsim.Pos{Time: at[2].Add(time.Second), Lat: ps[2].Lat, Lon: ps[2].Lon, Sats: 9, Valid: true}))
		time.Sleep(500 * time.Millisecond)
	})
}

// CalAmp LM Direct over UDP: every event waits for Traccar's ACK.
func TestPositionsCalAmp(t *testing.T) {
	const id = "4634633000101"
	reported(t, id, 1e-6, "", func(ps []sent, at []time.Time) {
		tr, err := calampsim.Dial(udpAddr["calamp"], id)
		must(t, err)
		defer tr.Close()
		for i, p := range ps {
			pos := calampsim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Alt: 900, Speed: p.KmH, Heading: int(p.Course), Sats: 9, Valid: true}
			if i == 1 {
				must(t, tr.Mini(pos, 0))
			} else {
				must(t, tr.Event(pos, 0))
			}
		}
	})
}

func TestPositionsATrack(t *testing.T) {
	const imei = "352964050000101"
	reported(t, imei, 1e-5, "", func(ps []sent, at []time.Time) {
		tr, err := atracksim.Dial(tcpAddr["atrack"], imei)
		must(t, err)
		defer tr.Close()
		must(t, tr.Login())
		for i, p := range ps {
			must(t, tr.Report(atracksim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH, Heading: p.Course, Sats: 9}))
		}
	})
}

// ATrack's ASCII form.
func TestPositionsATrackASCII(t *testing.T) {
	const imei = "352964050000102"
	reported(t, imei, 1e-5, "", func(ps []sent, at []time.Time) {
		tr, err := atracksim.Dial(tcpAddr["atrack"], imei)
		must(t, err)
		defer tr.Close()
		tr.ASCII = true
		must(t, tr.Login())
		for i, p := range ps {
			must(t, tr.Report(atracksim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH, Heading: p.Course, Sats: 9}))
		}
	})
}

func TestPositionsEelink(t *testing.T) {
	const imei = "868204000000201"
	reported(t, imei, 1e-5, "", func(ps []sent, at []time.Time) {
		tr, err := eelinksim.Dial(tcpAddr["eelink"], imei)
		must(t, err)
		defer tr.Close()
		must(t, tr.Login())
		for i, p := range ps {
			must(t, tr.Location(eelinksim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH, Heading: p.Course, Sats: 9, Valid: true}))
		}
		time.Sleep(500 * time.Millisecond)
	})
}

func TestPositionsWatch(t *testing.T) {
	const id = "8800000101"
	reported(t, id, 1e-5, "", func(ps []sent, at []time.Time) {
		tr, err := watchsim.Dial(tcpAddr["watch"], id)
		must(t, err)
		defer tr.Close()
		must(t, tr.Login())
		for i, p := range ps {
			must(t, tr.Report(watchsim.Pos{Time: at[i], Lat: p.Lat, Lon: p.Lon, Speed: p.KmH, Heading: p.Course, Sats: 9, Valid: true}))
		}
		time.Sleep(500 * time.Millisecond)
	})
}
