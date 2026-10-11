// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Command trackersim runs a simulated GPS tracker of any protocol in this
// module against a tracker server, so a server can be tried without a
// device: it connects, logs in the way the protocol does, reports a
// position every -every while the vehicle drives a slow circle, answers
// the server's commands as the device does, and prints what it sent and
// received.
//
//	trackersim -protocol gt06 -server 127.0.0.1:5024 -imei 359710049099999
//	trackersim -protocol ais140 -server 127.0.0.1:5023 -imei 861693034634154 -password 1234 -every 5s
//	trackersim -protocol osmand -server 127.0.0.1:5055 -imei phone-1 -count 10
//	trackersim -list
//
// -imei is the terminal phone number for jt808 (its last 12 digits) and
// the device ID for h02 and osmand. -password is the device's command
// password (ais140, queclink). All identities are whatever you give it;
// the defaults are demo values.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "trackersim:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("trackersim", flag.ContinueOnError)
	fs.SetOutput(out)
	var c sim.Config
	list := fs.Bool("list", false, "list the protocols and exit")
	fs.StringVar(&c.Protocol, "protocol", "gt06", "protocol: "+strings.Join(sim.Protocols, ", "))
	fs.StringVar(&c.Server, "server", "127.0.0.1:5024", "server address host:port")
	fs.StringVar(&c.IMEI, "imei", "359710049099999", "device IMEI (jt808: terminal phone number; h02, osmand: device ID)")
	fs.StringVar(&c.Password, "password", "", "the device's command password (ais140, queclink)")
	fs.DurationVar(&c.Every, "every", 10*time.Second, "reporting interval")
	fs.IntVar(&c.Count, "count", 0, "stop after this many reports (0: until interrupted)")
	fs.Float64Var(&c.Start.Lat, "lat", 12.9716, "start latitude")
	fs.Float64Var(&c.Start.Lon, "lon", 77.5946, "start longitude")
	fs.Float64Var(&c.Start.Speed, "speed", 40, "speed in km/h")
	fs.Float64Var(&c.Start.Heading, "heading", 0, "start heading in degrees")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments %q", fs.Args())
	}
	if *list {
		for _, p := range sim.Protocols {
			fmt.Fprintln(out, p)
		}
		return nil
	}
	c.OnReport = func(p sim.Pos) {
		fmt.Fprintf(out, "%s report %.6f,%.6f %.0f km/h %.0f°\n", p.Time.Format(time.RFC3339), p.Lat, p.Lon, p.Speed, p.Heading)
	}
	c.OnCommand = func(s string) { fmt.Fprintf(out, "%s command %s\n", time.Now().UTC().Format(time.RFC3339), s) }
	fmt.Fprintf(out, "%s tracker %s on %s\n", c.Protocol, c.IMEI, c.Server)
	return sim.Run(ctx, c)
}
