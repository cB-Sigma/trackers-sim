// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package calampsim_test

import (
	"net"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/calampsim"
	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers/calamp"
	"github.com/cB-Sigma/trackers/track"
)

type datagram struct {
	m    *track.Message
	from net.Addr
}

// serveUDP decodes each datagram with the library's codec and sends the
// acknowledgement the codec builds.
func serveUDP(t *testing.T) (net.PacketConn, track.Codec, <-chan datagram) {
	t.Helper()
	nc, err := calamp.NewCodec(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := nc()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	out := make(chan datagram, 64)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			ms, err := c.(track.DatagramCodec).DecodeDatagram(append([]byte(nil), buf[:n]...))
			if err != nil {
				continue
			}
			for _, m := range ms {
				if len(m.Reply) > 0 {
					_, _ = pc.WriteTo(m.Reply, from)
				}
				out <- datagram{m, from}
			}
		}
	}()
	return pc, c, out
}

func next(t *testing.T, ch <-chan datagram) datagram {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("no datagram decoded")
		return datagram{}
	}
}

// Event and mini event reports decode with the library's codec, whose
// acknowledgement the simulator waits for; user data sent with the
// codec's command framing is acknowledged by the unit and the ACK
// correlates with the command.
func TestTrackerAgainstCodec(t *testing.T) {
	pc, c, ch := serveUDP(t)
	tr, err := calampsim.Dial(pc.LocalAddr().String(), "4532104183")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	p := calampsim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Alt: 900, Speed: 36, Heading: 90, Sats: 9, Valid: true}
	if err := tr.Event(p, 13); err != nil {
		t.Fatal(err)
	}
	d := next(t, ch)
	r := d.m.Records[0]
	f := r.Fix
	if d.m.IMEI != "4532104183" || f == nil || !f.Valid || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) ||
		!codectest.Near(*f.Speed, 36) || *f.Alt != 900 || *f.Heading != 90 || r.Status["event"] != 13 || r.Status["ignition"] != true || r.Status["rssi"] != -71 {
		t.Fatalf("%q %+v %v", d.m.IMEI, f, r.Status)
	}
	if err := tr.Mini(p, 7); err != nil {
		t.Fatal(err)
	}
	d = next(t, ch)
	if f := d.m.Records[0].Fix; f == nil || *f.Speed != 36 || *f.Sats != 9 || d.m.Records[0].Status["event"] != 7 {
		t.Fatalf("mini %+v", d.m.Records[0])
	}
	frame, answers, err := c.Command(d.m.IMEI, map[string]any{"text": "HELLO"}, 42)
	if err != nil || !answers {
		t.Fatal(err)
	}
	if _, err := pc.WriteTo(frame, d.from); err != nil {
		t.Fatal(err)
	}
	ack := next(t, ch)
	want, _ := c.(track.Correlator).Correlation(map[string]any{"text": "HELLO"}, 42)
	if ack.m.Response == nil || ack.m.Correlation != want || ack.m.Failed || ack.m.IMEI != "4532104183" {
		t.Fatalf("ack %+v, want %q", ack.m, want)
	}
	if cmds := tr.Commands(); len(cmds) != 1 || cmds[0] != "HELLO" {
		t.Errorf("commands %q", cmds)
	}
}
