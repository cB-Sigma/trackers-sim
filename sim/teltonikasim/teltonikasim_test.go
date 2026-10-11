// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package teltonikasim_test

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers-sim/sim/teltonikasim"
	"github.com/cB-Sigma/trackers/teltonika"
)

// The IMEI handshake is accepted and Codec 8 and 8E packets decode with the
// library's codec, which acknowledges the record count.
func TestTrackerAgainstCodec(t *testing.T) {
	at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	rec := teltonikasim.Record{Time: at, Lat: 12.9716, Lon: 77.5946, Alt: 900, Angle: 90, Sats: 8, Speed: 36,
		IO1: map[uint16]uint8{239: 1}, IO2: map[uint16]uint16{66: 12400}}
	// Four zero bytes, the data length, the data, a CRC-16 in four bytes.
	data := teltonikasim.AVL(false, []teltonikasim.Record{rec})
	pkt := teltonikasim.TCPPacket(data)
	if len(pkt) != len(data)+12 || binary.BigEndian.Uint32(pkt[4:]) != uint32(len(data)) || data[0] != teltonika.Codec8 {
		t.Fatalf("packet % x", pkt)
	}
	addr, msgs := codectest.Serve(t, teltonika.NewCodec, nil)
	tr, ok, err := teltonikasim.Dial(addr, "860000000000011")
	if err != nil || !ok {
		t.Fatalf("handshake %v %v", ok, err)
	}
	defer tr.Close()
	for _, ext := range []bool{false, true} {
		n, err := tr.Send(ext, rec)
		if err != nil || n != 1 {
			t.Fatalf("ext %v: ack %d %v", ext, n, err)
		}
		m, f := codectest.NextFix(t, msgs)
		if m.IMEI != "860000000000011" || !f.Time.Equal(at) || !codectest.Near(f.Lat, 12.9716) || !codectest.Near(f.Lon, 77.5946) ||
			f.Speed == nil || *f.Speed != 36 {
			t.Fatalf("ext %v: %s %+v", ext, m.IMEI, f)
		}
	}
}
