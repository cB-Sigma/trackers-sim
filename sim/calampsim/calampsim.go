// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package calampsim is a CalAmp LMU for tests and demos, speaking LM
// Direct over UDP: it sends event reports (type 2) and mini event reports
// (type 10) with an options header carrying its mobile ID, asks the server
// to acknowledge them and waits for that acknowledgement, and
// acknowledges the user data (type 4) the server sends it, as a unit does.
// Layouts follow the library's calamp package.
package calampsim

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"net"
	"strings"
	"sync"
	"time"
)

// Pos is one position.
type Pos struct {
	Time     time.Time
	Lat, Lon float64
	Alt      float64 // metres
	Speed    float64 // km/h
	Heading  int
	Sats     int
	Valid    bool
}

// Tracker is one simulated unit.
type Tracker struct {
	MobileID string
	// Inputs is the input byte reported (bit 0 the ignition).
	Inputs byte
	// Nak, when set, answers user data with this NAK code instead of ACK.
	Nak byte

	c    net.Conn
	acks chan uint16
	mu   sync.Mutex
	cmds []string
	seq  uint16
}

// Dial opens the UDP association with the server.
func Dial(addr, mobileID string) (*Tracker, error) {
	if mobileID == "" || strings.Trim(mobileID, "0123456789") != "" || len(mobileID) > 30 {
		return nil, errors.New("calampsim: a mobile ID is 1 to 30 digits")
	}
	c, err := net.DialTimeout("udp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{MobileID: mobileID, Inputs: 1, c: c, acks: make(chan uint16, 16)}
	go t.read()
	return t, nil
}

// Close ends the association.
func (t *Tracker) Close() error { return t.c.Close() }

// Commands lists the user data texts received.
func (t *Tracker) Commands() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cmds...)
}

// options is the options header with the mobile ID (BCD, padded with F)
// and its type (1, the ESN-style ID).
func (t *Tracker) options() []byte {
	id := t.MobileID
	if len(id)%2 == 1 {
		id += "f"
	}
	b, _ := hex.DecodeString(id)
	out := []byte{0x83, byte(len(b))}
	out = append(out, b...)
	return append(out, 1, 1)
}

func (t *Tracker) read() {
	buf := make([]byte, 2048)
	for {
		n, err := t.c.Read(buf)
		if err != nil {
			close(t.acks)
			return
		}
		b := buf[:n]
		if len(b) > 0 && b[0]&0x80 != 0 {
			i := 1
			for bit := 0; bit < 6 && i < len(b); bit++ {
				if b[0]>>bit&1 == 1 {
					i += 1 + int(b[i])
				}
			}
			if i > len(b) {
				continue
			}
			b = b[i:]
		}
		if len(b) < 4 {
			continue
		}
		service, typ, seq := b[0], b[1], binary.BigEndian.Uint16(b[2:4])
		switch {
		case typ == 1 && service == 2:
			select {
			case t.acks <- seq:
			default:
			}
		case typ == 4 && len(b) >= 8:
			n := int(binary.BigEndian.Uint16(b[6:8]))
			if 8+n > len(b) {
				continue
			}
			t.mu.Lock()
			t.cmds = append(t.cmds, string(b[8:8+n]))
			nak := t.Nak
			t.mu.Unlock()
			if service == 1 {
				ack := append(t.options(), 2, 1, b[2], b[3], 4, nak, 0, 0, 0, 0)
				_, _ = t.c.Write(ack)
			}
		}
	}
}

func (t *Tracker) next() uint16 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	return t.seq
}

// send writes an acknowledged message and waits for the server's ACK.
func (t *Tracker) send(typ byte, body []byte) error {
	seq := t.next()
	msg := append(t.options(), 1, typ, byte(seq>>8), byte(seq))
	if _, err := t.c.Write(append(msg, body...)); err != nil {
		return err
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case s, ok := <-t.acks:
			if !ok {
				return errors.New("calampsim: connection closed")
			}
			if s == seq {
				return nil
			}
		case <-deadline:
			return errors.New("calampsim: no acknowledgement from the server")
		}
	}
}

func deg(v float64) uint32 { return uint32(int32(math.Round(v * 1e7))) }

// Event sends an event report with an event code and two accumulators.
func (t *Tracker) Event(p Pos, code byte) error {
	b := binary.BigEndian.AppendUint32(nil, uint32(p.Time.Unix()))
	b = binary.BigEndian.AppendUint32(b, uint32(p.Time.Unix()))
	b = binary.BigEndian.AppendUint32(b, deg(p.Lat))
	b = binary.BigEndian.AppendUint32(b, deg(p.Lon))
	b = binary.BigEndian.AppendUint32(b, uint32(int32(math.Round(p.Alt*100))))
	b = binary.BigEndian.AppendUint32(b, uint32(math.Round(p.Speed/0.036)))
	b = binary.BigEndian.AppendUint16(b, uint16(p.Heading))
	fix := byte(0x08)
	if p.Valid {
		fix = 0
	}
	b = append(b, byte(p.Sats), fix)
	b = binary.BigEndian.AppendUint16(b, 405) // carrier
	rssi := int16(-71)
	b = binary.BigEndian.AppendUint16(b, uint16(rssi))         // RSSI dBm
	b = append(b, 0x0F, 10, t.Inputs, 0, 1, code, 2, 0)        // comm state, HDOP, inputs, unit status, event index, code, accumulator count, append
	b = binary.BigEndian.AppendUint32(b, 1200)                 // accumulator 0
	b = binary.BigEndian.AppendUint32(b, uint32(p.Speed*1000)) // accumulator 1
	return t.send(2, b)
}

// Mini sends a mini event report.
func (t *Tracker) Mini(p Pos, code byte) error {
	b := binary.BigEndian.AppendUint32(nil, uint32(p.Time.Unix()))
	b = binary.BigEndian.AppendUint32(b, deg(p.Lat))
	b = binary.BigEndian.AppendUint32(b, deg(p.Lon))
	b = binary.BigEndian.AppendUint16(b, uint16(p.Heading))
	fs := byte(p.Sats & 0x0F)
	if !p.Valid {
		fs |= 0x20
	}
	b = append(b, byte(math.Round(p.Speed)), fs, 0x0F, t.Inputs, code, 0)
	return t.send(10, b)
}
