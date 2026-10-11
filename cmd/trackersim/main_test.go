// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/cB-Sigma/trackers/teltonika"
)

// The runner reports to a server on the library's Teltonika codec and
// prints the command the server sends.
func TestRunner(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	nc, err := teltonika.NewCodec(nil)
	if err != nil {
		t.Fatal(err)
	}
	fixes := make(chan float64, 8)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		codec, r := nc(), bufio.NewReader(c)
		sent := false
		for {
			m, err := codec.Decode(r)
			if err != nil {
				return
			}
			_, _ = c.Write(m.Reply)
			for _, rec := range m.Records {
				if rec.Fix != nil {
					fixes <- rec.Fix.Lat
					if !sent {
						sent = true
						b, _, _ := codec.Command(m.IMEI, map[string]any{"text": "getinfo"}, 1)
						_, _ = c.Write(b)
					}
				}
			}
		}
	}()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-protocol", "teltonika", "-server", ln.Addr().String(),
		"-every", "100ms", "-count", "3", "-lat", "28.6139", "-lon", "77.2090"}, &out); err != nil {
		t.Fatal(err)
	}
	if lat := <-fixes; lat < 28.613 || lat > 28.615 {
		t.Errorf("first latitude %v", lat)
	}
	s := out.String()
	if strings.Count(s, " report ") != 3 || !strings.Contains(s, "command getinfo") {
		t.Fatalf("output:\n%s", s)
	}
}

func TestRunnerFlags(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-list"}, &out); err != nil || !strings.Contains(out.String(), "ais140\n") {
		t.Fatalf("%v %q", err, out.String())
	}
	if err := run(context.Background(), []string{"-protocol", "nmea", "-count", "1"}, &out); err == nil {
		t.Error("an unknown protocol was accepted")
	}
	if err := run(context.Background(), []string{"stray"}, &out); err == nil {
		t.Error("a stray argument was accepted")
	}
}
