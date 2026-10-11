// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package sim drives one simulated tracker of any protocol in this module
// against a server: it connects, logs in the way the protocol does, sends a
// position report at a fixed interval while the vehicle drives a slow
// circle, and reports the commands the server sends. The per-protocol
// simulators (gt06sim, teltonikasim, … ais140sim in the subdirectories)
// answer commands as their devices do; this package only gives them one
// shape, for cmd/trackersim and for tests that want "a tracker of kind X".
package sim

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
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

// Pos is the simulated vehicle's state at one report.
type Pos struct {
	Time     time.Time // UTC
	Lat, Lon float64
	Speed    float64 // km/h
	Heading  float64 // degrees from north
}

// Config describes one simulated tracker.
type Config struct {
	// Protocol is one of Protocols.
	Protocol string
	// Server is the host:port the tracker connects to (TCP; HTTP for
	// osmand).
	Server string
	// IMEI identifies the device (the terminal phone number for jt808, the
	// device ID for h02 and osmand).
	IMEI string
	// Password is the device's command password where the protocol has one
	// (ais140, queclink).
	Password string
	// Every is the reporting interval (default 10s); Count stops after this
	// many reports (0 runs until the context ends).
	Every time.Duration
	Count int
	// Start is where the vehicle starts (default central Bengaluru, 40
	// km/h, heading north).
	Start Pos
	// OnReport and OnCommand, when set, see each report sent and each
	// command received (as the protocol's text, or a description).
	OnReport  func(Pos)
	OnCommand func(string)
}

// Protocols are the protocols Run can simulate.
var Protocols = sortedKeys(dialers)

// device is one connected, logged-in simulator.
type device interface {
	report(Pos) error
	commands() []string
	close() error
}

var dialers = map[string]func(Config) (device, error){
	"ais140": func(c Config) (device, error) {
		d, err := ais140sim.Dial(c.Server, c.IMEI, "KA01SIM"+tail(c.IMEI, 4))
		if err != nil {
			return nil, err
		}
		d.Password = c.Password
		if err := d.Send(d.Login()); err != nil {
			_ = d.Close()
			return nil, err
		}
		return ais140Dev{d}, nil
	},
	"gt06": func(c Config) (device, error) {
		t, err := gt06sim.Dial(c.Server, c.IMEI)
		if err != nil {
			return nil, err
		}
		if err := t.Login(); err != nil {
			_ = t.Close()
			return nil, err
		}
		return gt06Dev{t}, nil
	},
	"h02": func(c Config) (device, error) {
		t, err := h02sim.Dial(c.Server, c.IMEI)
		if err != nil {
			return nil, err
		}
		return h02Dev{t}, nil
	},
	"jt808": func(c Config) (device, error) {
		t, err := jt808sim.Dial(c.Server, tail(c.IMEI, 12), false)
		if err != nil {
			return nil, err
		}
		if err := t.Login(); err != nil {
			_ = t.Close()
			return nil, err
		}
		return jt808Dev{t}, nil
	},
	"meitrack": func(c Config) (device, error) {
		t, err := meitracksim.Dial(c.Server, c.IMEI)
		if err != nil {
			return nil, err
		}
		return meitrackDev{t}, nil
	},
	"osmand": func(c Config) (device, error) {
		p, err := osmandsim.Dial(c.Server, c.IMEI)
		if err != nil {
			return nil, err
		}
		return osmandDev{p}, nil
	},
	"queclink": func(c Config) (device, error) {
		t, err := queclinksim.Dial(c.Server, c.IMEI, c.Password)
		if err != nil {
			return nil, err
		}
		return queclinkDev{t}, nil
	},
	"ruptela": func(c Config) (device, error) {
		imei, err := strconv.ParseUint(c.IMEI, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("ruptela: the IMEI must be digits: %w", err)
		}
		t, err := ruptelasim.Dial(c.Server, imei)
		if err != nil {
			return nil, err
		}
		return ruptelaDev{t}, nil
	},
	"teltonika": func(c Config) (device, error) {
		t, ok, err := teltonikasim.Dial(c.Server, c.IMEI)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("teltonika: the server refused the IMEI")
		}
		return teltonikaDev{t}, nil
	},
	"tk103": func(c Config) (device, error) {
		t, err := tk103sim.Dial(c.Server, c.IMEI)
		if err != nil {
			return nil, err
		}
		if err := t.Login(); err != nil {
			_ = t.Close()
			return nil, err
		}
		return tk103Dev{t}, nil
	},
}

// Run connects the tracker and reports until ctx ends or Count reports
// were sent. It returns the first error that ends the session.
func Run(ctx context.Context, c Config) error {
	dial, ok := dialers[c.Protocol]
	if !ok {
		return fmt.Errorf("protocol %q is not one of %s", c.Protocol, strings.Join(Protocols, ", "))
	}
	if c.Server == "" || c.IMEI == "" {
		return errors.New("a server address and an IMEI are needed")
	}
	if c.Every <= 0 {
		c.Every = 10 * time.Second
	}
	p := c.Start
	if p.Lat == 0 && p.Lon == 0 {
		p = Pos{Lat: 12.9716, Lon: 77.5946, Speed: 40}
	}
	d, err := dial(c)
	if err != nil {
		return fmt.Errorf("%s: %w", c.Protocol, err)
	}
	defer d.close()
	seen := 0
	tick := time.NewTicker(c.Every)
	defer tick.Stop()
	for n := 0; c.Count == 0 || n < c.Count; n++ {
		if n > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C:
			}
			p = Drive(p, c.Every)
		}
		p.Time = time.Now().UTC().Truncate(time.Second)
		if err := d.report(p); err != nil {
			return fmt.Errorf("%s report: %w", c.Protocol, err)
		}
		if c.OnReport != nil {
			c.OnReport(p)
		}
		if cmds := d.commands(); len(cmds) > seen {
			for _, s := range cmds[seen:] {
				if c.OnCommand != nil {
					c.OnCommand(s)
				}
			}
			seen = len(cmds)
		}
	}
	// Leave a moment for answers to the last commands.
	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
	}
	if cmds := d.commands(); len(cmds) > seen && c.OnCommand != nil {
		for _, s := range cmds[seen:] {
			c.OnCommand(s)
		}
	}
	return nil
}

// Drive moves p for d at its speed and heading, turning 10 degrees per
// minute so the vehicle keeps to a circle.
func Drive(p Pos, d time.Duration) Pos {
	km := p.Speed * d.Hours()
	rad := p.Heading * math.Pi / 180
	p.Lat += km / 111.32 * math.Cos(rad)
	p.Lon += km / (111.32 * math.Cos(p.Lat*math.Pi/180)) * math.Sin(rad)
	p.Heading = math.Mod(p.Heading+10*d.Minutes(), 360)
	return p
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- adapters to the simulators ---------------------------------------------

type ais140Dev struct{ d *ais140sim.Device }

func (a ais140Dev) report(p Pos) error {
	return a.d.Send(a.d.Normal(ais140sim.Fix{Time: p.Time, Lat: p.Lat, Lon: p.Lon, Speed: p.Speed, Heading: p.Heading,
		Sats: 12, Valid: true, Ignition: true, Power: true, MainV: 12.4, BatteryV: 4.1, GSM: 25}))
}
func (a ais140Dev) commands() []string { return a.d.Commands() }
func (a ais140Dev) close() error       { return a.d.Close() }

type gt06Dev struct{ t *gt06sim.Tracker }

func (g gt06Dev) report(p Pos) error {
	return g.t.Location(gt06sim.Pos{Time: p.Time, Lat: p.Lat, Lon: p.Lon, Speed: int(p.Speed), Course: int(p.Heading), Sats: 9, Valid: true}, false)
}
func (g gt06Dev) commands() []string { return g.t.Commands() }
func (g gt06Dev) close() error       { return g.t.Close() }

type h02Dev struct{ t *h02sim.Tracker }

func (h h02Dev) report(p Pos) error {
	return h.t.Position(h02sim.Pos{Time: p.Time, Lat: p.Lat, Lon: p.Lon, Speed: p.Speed / 1.852, Course: int(p.Heading), Valid: true})
}
func (h h02Dev) commands() []string { return h.t.Commands() }
func (h h02Dev) close() error       { return h.t.Close() }

type jt808Dev struct{ t *jt808sim.Tracker }

func (j jt808Dev) report(p Pos) error {
	return j.t.Location(jt808sim.Pos{Time: p.Time, Lat: p.Lat, Lon: p.Lon, Speed: p.Speed, Course: int(p.Heading), Sats: 9, Valid: true, ACC: true})
}
func (j jt808Dev) commands() []string {
	var out []string
	for _, c := range j.t.Received() {
		if c.Text != "" {
			out = append(out, fmt.Sprintf("0x%04X %s", c.ID, c.Text))
		} else {
			out = append(out, fmt.Sprintf("0x%04X % X", c.ID, c.Body))
		}
	}
	return out
}
func (j jt808Dev) close() error { return j.t.Close() }

type meitrackDev struct{ t *meitracksim.Tracker }

func (m meitrackDev) report(p Pos) error {
	return m.t.Report(35, meitracksim.Pos{Time: p.Time, Lat: p.Lat, Lon: p.Lon, Speed: int(p.Speed), Course: int(p.Heading), Sats: 9, Valid: true})
}
func (m meitrackDev) commands() []string {
	var out []string
	for _, c := range m.t.Commands() {
		out = append(out, c.String())
	}
	return out
}
func (m meitrackDev) close() error { return m.t.Close() }

type osmandDev struct{ p *osmandsim.Phone }

func (o osmandDev) report(p Pos) error {
	st, err := o.p.Send(osmandsim.Position{Time: p.Time, Lat: p.Lat, Lon: p.Lon, SpeedKnots: p.Speed / 1.852, Bearing: p.Heading, Accuracy: 5, Battery: 80})
	if err != nil {
		return err
	}
	if st != 200 {
		return fmt.Errorf("HTTP status %d", st)
	}
	return nil
}
func (o osmandDev) commands() []string { return nil } // the protocol has none
func (o osmandDev) close() error       { return o.p.Close() }

type queclinkDev struct{ t *queclinksim.Tracker }

func (q queclinkDev) report(p Pos) error {
	return q.t.Fixed([]queclinksim.Pos{{Time: p.Time, Lat: p.Lat, Lon: p.Lon, Speed: p.Speed, Heading: int(p.Heading), Alt: 900, HDOP: 1}},
		12400, 90, "220100")
}
func (q queclinkDev) commands() []string { return q.t.Commands() }
func (q queclinkDev) close() error       { return q.t.Close() }

type ruptelaDev struct{ t *ruptelasim.Tracker }

func (r ruptelaDev) report(p Pos) error {
	return r.t.Records(false, false, ruptelasim.Rec{Time: p.Time, Lat: p.Lat, Lon: p.Lon, Alt: 900, Angle: p.Heading,
		Speed: int(p.Speed), Sats: 9, HDOP: 0.9})
}
func (r ruptelaDev) commands() []string { return r.t.Commands() }
func (r ruptelaDev) close() error       { return r.t.Close() }

type teltonikaDev struct{ t *teltonikasim.Tracker }

func (t teltonikaDev) report(p Pos) error {
	_, err := t.t.Send(false, teltonikasim.Record{Time: p.Time, Lat: p.Lat, Lon: p.Lon, Alt: 900, Angle: uint16(p.Heading),
		Sats: 9, Speed: uint16(p.Speed)})
	return err
}
func (t teltonikaDev) commands() []string { return t.t.Commands() }
func (t teltonikaDev) close() error       { return t.t.Close() }

type tk103Dev struct{ t *tk103sim.Tracker }

func (t tk103Dev) report(p Pos) error {
	return t.t.Report("tracker", tk103sim.Pos{Time: p.Time, Lat: p.Lat, Lon: p.Lon, Speed: p.Speed, Heading: p.Heading, Valid: true})
}
func (t tk103Dev) commands() []string { return t.t.Commands() }
func (t tk103Dev) close() error       { return t.t.Close() }
