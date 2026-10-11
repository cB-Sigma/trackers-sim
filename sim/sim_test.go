// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package sim_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim"
	"github.com/cB-Sigma/trackers/ais140"
	"github.com/cB-Sigma/trackers/atrack"
	"github.com/cB-Sigma/trackers/calamp"
	"github.com/cB-Sigma/trackers/eelink"
	"github.com/cB-Sigma/trackers/galileosky"
	"github.com/cB-Sigma/trackers/gt06"
	"github.com/cB-Sigma/trackers/h02"
	"github.com/cB-Sigma/trackers/jt808"
	"github.com/cB-Sigma/trackers/meitrack"
	"github.com/cB-Sigma/trackers/osmand"
	"github.com/cB-Sigma/trackers/queclink"
	"github.com/cB-Sigma/trackers/ruptela"
	"github.com/cB-Sigma/trackers/suntech"
	"github.com/cB-Sigma/trackers/teltonika"
	"github.com/cB-Sigma/trackers/tk103"
	"github.com/cB-Sigma/trackers/track"
	"github.com/cB-Sigma/trackers/watch"
	"github.com/cB-Sigma/trackers/wialon"
)

var codecs = map[string]track.NewCodec{
	"ais140": ais140.NewCodec, "atrack": atrack.NewCodec, "calamp": calamp.NewCodec, "eelink": eelink.NewCodec,
	"galileosky": galileosky.NewCodec, "gt06": gt06.NewCodec, "h02": h02.NewCodec, "jt808": jt808.NewCodec,
	"meitrack": meitrack.NewCodec, "osmand": osmand.NewCodec, "queclink": queclink.NewCodec,
	"ruptela": ruptela.NewCodec, "suntech": suntech.NewCodec, "teltonika": teltonika.NewCodec, "tk103": tk103.NewCodec,
	"watch": watch.NewCodec, "wialon": wialon.NewCodec,
}

// udpOnly are the protocols whose simulator speaks UDP only.
var udpOnly = map[string]bool{"calamp": true}

// serveUDP is server for a codec's datagram framing (track.DatagramCodec).
func serveUDP(t *testing.T, nc func() track.Codec, cmd any) (*server, string) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	s := &server{imeis: map[string]bool{}}
	codec := nc()
	dc, ok := codec.(track.DatagramCodec)
	if !ok {
		t.Fatal("the codec has no datagram framing")
	}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			ms, err := dc.DecodeDatagram(append([]byte(nil), buf[:n]...))
			if err != nil {
				continue
			}
			for _, m := range ms {
				if len(m.Reply) > 0 {
					_, _ = pc.WriteTo(m.Reply, from)
				}
				s.mu.Lock()
				if m.IMEI != "" {
					s.imeis[m.IMEI] = true
				}
				send := false
				for _, rec := range m.Records {
					if rec.Fix != nil {
						s.fixes = append(s.fixes, *rec.Fix)
						send = cmd != nil && !s.sent
					}
				}
				if send {
					s.sent = true
				}
				s.mu.Unlock()
				if send {
					b, _, err := codec.Command(m.IMEI, cmd, 7)
					s.mu.Lock()
					s.err = err
					s.mu.Unlock()
					if err == nil {
						_, _ = pc.WriteTo(b, from)
					}
				}
			}
		}
	}()
	return s, pc.LocalAddr().String()
}

// server is a minimal tracker server on a library codec: it answers what
// the codec says to answer, keeps the fixes, and sends one command after
// the first report.
type server struct {
	ln    net.Listener
	mu    sync.Mutex
	fixes []track.Fix
	imeis map[string]bool
	sent  bool
	err   error
}

func serve(t *testing.T, nc func() track.Codec, cmd any) *server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{ln: ln, imeis: map[string]bool{}}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.session(c, nc(), cmd)
		}
	}()
	return s
}

func (s *server) session(c net.Conn, codec track.Codec, cmd any) {
	defer c.Close()
	r := bufio.NewReader(c)
	imei := ""
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
		if m.IMEI != "" {
			imei = m.IMEI
		}
		s.mu.Lock()
		if m.IMEI != "" {
			s.imeis[m.IMEI] = true
		}
		send := false
		for _, rec := range m.Records {
			if rec.Fix != nil {
				s.fixes = append(s.fixes, *rec.Fix)
				send = cmd != nil && !s.sent
			}
		}
		if send {
			s.sent = true
		}
		s.mu.Unlock()
		if send {
			var b []byte
			if pc, ok := codec.(track.PasswordCommander); ok {
				b, _, err = pc.CommandWithPassword(imei, "1234", cmd, 7)
			} else {
				b, _, err = codec.Command(imei, cmd, 7)
			}
			s.mu.Lock()
			s.err = err
			s.mu.Unlock()
			if err == nil {
				_, _ = c.Write(b)
			}
		}
	}
}

// Every protocol's simulator, driven by Run, logs in, reports positions
// the library's codec of that protocol decodes where the vehicle is, and
// receives a command the codec framed.
func TestRunEveryProtocol(t *testing.T) {
	if len(sim.Protocols) != len(codecs) {
		t.Fatalf("protocols %v", sim.Protocols)
	}
	for _, proto := range sim.Protocols {
		t.Run(proto, func(t *testing.T) {
			t.Parallel()
			mk, err := codecs[proto](json.RawMessage(nil))
			if err != nil {
				t.Fatal(err)
			}
			var cmd any = map[string]any{"action": "locate"}
			switch proto {
			case "osmand":
				cmd = nil // phone apps take no commands
			case "ais140":
				cmd = map[string]any{"op": "GET", "param": "001"}
			case "calamp", "wialon":
				// no named locate in the protocol: a text command
				cmd = map[string]any{"text": "getstatus"}
			}
			var s *server
			var addr string
			if udpOnly[proto] {
				s, addr = serveUDP(t, mk, cmd)
			} else {
				s = serve(t, mk, cmd)
				addr = s.ln.Addr().String()
			}
			imei := "359710049099999"
			var mu sync.Mutex
			var got []string
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			err = sim.Run(ctx, sim.Config{Protocol: proto, Server: addr, IMEI: imei, Password: "1234",
				Every: 150 * time.Millisecond, Count: 4,
				OnCommand: func(c string) { mu.Lock(); got = append(got, c); mu.Unlock() }})
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				s.mu.Lock()
				n := len(s.fixes)
				s.mu.Unlock()
				if n >= 4 || time.Now().After(deadline) {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.err != nil {
				t.Fatalf("command: %v", s.err)
			}
			if len(s.fixes) < 4 {
				t.Fatalf("%d fixes decoded, %v", len(s.fixes), s.imeis)
			}
			f := s.fixes[0]
			if f.Lat < 12.96 || f.Lat > 12.98 || f.Lon < 77.58 || f.Lon > 77.61 {
				t.Errorf("first fix %+v", f)
			}
			if last := s.fixes[len(s.fixes)-1]; last.Lat <= f.Lat {
				t.Errorf("the vehicle did not move north: %v then %v", f.Lat, last.Lat)
			}
			if len(s.imeis) == 0 {
				t.Errorf("no device identity decoded")
			}
			mu.Lock()
			defer mu.Unlock()
			if cmd != nil && len(got) == 0 {
				t.Errorf("no command reached the tracker")
			}
		})
	}
}

func TestRunRefusesUnknownProtocol(t *testing.T) {
	if err := sim.Run(context.Background(), sim.Config{Protocol: "nmea", Server: "127.0.0.1:1", IMEI: "1"}); err == nil {
		t.Fatal("an unknown protocol was accepted")
	}
}

// Example drives a simulated GT06 tracker against a server for three
// reports (here a listener that only counts connections).
func Example() {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	nc, _ := gt06.NewCodec(nil)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		codec, r := nc(), bufio.NewReader(c)
		for {
			m, err := codec.Decode(r)
			if err != nil {
				return
			}
			_, _ = c.Write(m.Reply)
		}
	}()
	n := 0
	err := sim.Run(context.Background(), sim.Config{Protocol: "gt06", Server: ln.Addr().String(), IMEI: "359710049099999",
		Every: 50 * time.Millisecond, Count: 3, OnReport: func(sim.Pos) { n++ }})
	fmt.Println(n, err)
	// Output: 3 <nil>
}
