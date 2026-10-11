// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package codectest is a minimal tracker server for the simulators' own
// tests: it decodes what a simulator sends with the library's codec of the
// same protocol, writes the replies the codec gives, and hands every
// decoded message to the test.
package codectest

import (
	"bufio"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers/track"
)

// Serve listens on a loopback TCP port and decodes each connection with a
// new codec from nc. It returns the address and the decoded messages.
func Serve(t *testing.T, nc track.NewCodec, profile []byte) (string, <-chan *track.Message) {
	t.Helper()
	newCodec, err := nc(profile)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	out := make(chan *track.Message, 64)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go session(c, newCodec(), out)
		}
	}()
	return ln.Addr().String(), out
}

func session(c net.Conn, codec track.Codec, out chan<- *track.Message) {
	defer c.Close()
	r := bufio.NewReader(c)
	for {
		m, err := codec.Decode(r)
		if errors.Is(err, track.ErrSkip) {
			var sk *track.Skip
			if errors.As(err, &sk) && len(sk.Reply) > 0 {
				_, _ = c.Write(sk.Reply)
			}
			continue
		}
		if err != nil {
			return
		}
		if len(m.Reply) > 0 {
			_, _ = c.Write(m.Reply)
		}
		select {
		case out <- m:
		default:
		}
	}
}

// NextFix waits up to five seconds for a decoded record with a fix.
func NextFix(t *testing.T, msgs <-chan *track.Message) (*track.Message, *track.Fix) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-msgs:
			for _, rec := range m.Records {
				if rec.Fix != nil {
					return m, rec.Fix
				}
			}
		case <-deadline:
			t.Fatal("no fix decoded")
			return nil, nil
		}
	}
}

// Near reports whether a and b differ by less than 1e-4 (about 10 m).
func Near(a, b float64) bool { return a-b < 1e-4 && b-a < 1e-4 }
