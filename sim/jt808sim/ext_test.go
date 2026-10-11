// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package jt808sim_test

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/jt808sim"
	"github.com/cB-Sigma/trackers/jt808"
	"github.com/cB-Sigma/trackers/track"
)

// server is one connection decoded by the library's codec, which can also
// send commands to the terminal.
type server struct {
	codec track.Codec
	conn  chan net.Conn
	msgs  chan *track.Message
	c     net.Conn
}

func serve(t *testing.T, profile string) (string, *server) {
	t.Helper()
	nc, err := jt808.NewCodec(json.RawMessage(profile))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &server{codec: nc(), conn: make(chan net.Conn, 1), msgs: make(chan *track.Message, 64)}
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		s.conn <- c
		r := bufio.NewReader(c)
		for {
			m, err := s.codec.Decode(r)
			if err != nil {
				if errors.Is(err, track.ErrSkip) {
					continue
				}
				close(s.msgs)
				return
			}
			if len(m.Reply) > 0 {
				_, _ = c.Write(m.Reply)
			}
			s.msgs <- m
		}
	}()
	return ln.Addr().String(), s
}

// command sends content and returns the answer that completes it.
func (s *server) command(t *testing.T, content any, seq uint32) *track.Message {
	t.Helper()
	if s.c == nil {
		s.c = <-s.conn
	}
	b, _, err := s.codec.Command("x", content, seq)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := s.codec.(track.Correlator).Correlation(content, seq)
	if _, err := s.c.Write(b); err != nil {
		t.Fatal(err)
	}
	return s.next(t, func(m *track.Message) bool { return m.Correlation == want })
}

func (s *server) next(t *testing.T, ok func(*track.Message) bool) *track.Message {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-s.msgs:
			if m != nil && ok(m) {
				return m
			}
		case <-deadline:
			t.Fatal("no such message")
			return nil
		}
	}
}

func dial(t *testing.T, addr string, v2019 bool) *jt808sim.Tracker {
	t.Helper()
	tr, err := jt808sim.Dial(addr, "000000000011", v2019)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

var at = time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)

// A 2011 registration, a batch in subpackages, reboot and interval
// answers, and an active safety alarm, all read by the library's codec.
func TestTerminalExtensions(t *testing.T) {
	addr, s := serve(t, "")
	tr := dial(t, addr, false)
	tr.V2011 = true
	if err := tr.Login(); err != nil {
		t.Fatal(err)
	}
	reg := s.next(t, func(m *track.Message) bool { return m.Login && len(m.Records) == 1 })
	if reg.Records[0].Status["protocolVersion"] != "2011" || reg.Records[0].Status["model"] != "VL103M" {
		t.Fatalf("2011 registration %v", reg.Records[0].Status)
	}
	var ps []jt808sim.Pos
	for i := range 5 {
		ps = append(ps, jt808sim.Pos{Time: at.Add(time.Duration(i) * time.Minute), Lat: 12.97, Lon: 77.59, Valid: true})
	}
	if err := tr.BatchParts(true, 60, ps...); err != nil {
		t.Fatal(err)
	}
	if b := s.next(t, func(m *track.Message) bool { return len(m.Records) > 0 }); len(b.Records) != 5 || !b.Records[4].Fix.Time.Equal(at.Add(4*time.Minute)) {
		t.Fatalf("batch %+v", b.Records)
	}
	if m := s.command(t, map[string]any{"action": "reboot"}, 21); m.Failed || tr.Reboots() != 1 {
		t.Fatalf("reboot %+v", m)
	}
	if m := s.command(t, map[string]any{"action": "interval", "seconds": 30}, 22); m.Failed || tr.Interval() != 30*time.Second {
		t.Fatalf("interval %+v %v", m, tr.Interval())
	}
	p := jt808sim.Pos{Time: at, Lat: 12.97, Lon: 77.59, Speed: 40, Valid: true}
	if err := tr.SafetyAlarm(p, jt808sim.Safety{DSM: true, ID: 7, Type: 2, Level: 2, State: 1, Attachments: 3}); err != nil {
		t.Fatal(err)
	}
	m := s.next(t, func(m *track.Message) bool { return len(m.Records) == 1 && len(m.Records[0].Alerts) > 0 })
	a := m.Records[0].Alerts[0]
	if a.Type != "phone-use" || a.Severity != track.Major || a.Code != "0x65-0x02#7" || m.Records[0].Status["dsmAttachments"] != 3 {
		t.Fatalf("safety alarm %+v %v", a, m.Records[0].Status)
	}
}

// JT/T 1078 queries answered with 0x1003 and 0x1205, and a live request
// with 0x0001, in the 2019 header.
func TestVideoAnswers(t *testing.T) {
	addr, s := serve(t, "")
	tr := dial(t, addr, true)
	if err := tr.Login(); err != nil {
		t.Fatal(err)
	}
	if m := s.command(t, map[string]any{"op": "avAttributes"}, 3); m.Details["maxVideoChannels"] != 4 {
		t.Fatalf("0x1003 %+v", m)
	}
	m := s.command(t, map[string]any{"op": "listResources", "channel": 2, "start": "2026-10-11T08:00:00Z", "end": "2026-10-11T09:00:00Z"}, 4)
	res, _ := m.Details["resources"].([]any)
	if len(res) != 1 || res[0].(map[string]any)["channel"] != 2 || res[0].(map[string]any)["start"] != "2026-10-11T08:00:00Z" {
		t.Fatalf("0x1205 %+v", m.Details)
	}
	if m := s.command(t, map[string]any{"op": "live", "server": "10.0.0.9", "tcpPort": 1078, "channel": 1}, 5); m.Failed {
		t.Fatalf("0x9101 %+v", m)
	}
}

// RSA: the terminal exchanges keys, sends an encrypted location the codec
// reads, and decrypts and answers an encrypted command.
func TestRSAExchange(t *testing.T) {
	k, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := json.Marshal(map[string]any{"rsaPrivateKey": string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))})
	addr, s := serve(t, string(profile))
	tr := dial(t, addr, false)
	if err := tr.Login(); err != nil {
		t.Fatal(err)
	}
	if err := tr.ExchangeKeys(); err != nil {
		t.Fatal(err)
	}
	if !tr.PlatformKey().Equal(&k.PublicKey) {
		t.Fatal("platform key")
	}
	tr.Encrypt = true
	if err := tr.Location(jt808sim.Pos{Time: at, Lat: 12.97, Lon: 77.59, Valid: true}); err != nil {
		t.Fatal(err)
	}
	if m := s.next(t, func(m *track.Message) bool { return len(m.Records) == 1 && m.Records[0].Fix != nil }); !m.Records[0].Fix.Time.Equal(at) {
		t.Fatalf("encrypted location %+v", m.Records[0].Fix)
	}
	if m := s.command(t, map[string]any{"action": "reboot"}, 9); m.Failed || tr.Reboots() != 1 {
		t.Fatalf("encrypted reboot %+v", m)
	}
	if got := tr.Received(); got[len(got)-1].ID != jt808.MTerminalControl {
		t.Fatalf("received %+v", got)
	}
}
