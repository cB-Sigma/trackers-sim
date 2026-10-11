// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package atracksim is an ATrack (AX/AK series) tracker for tests and
// demos: it sends a keep-alive and position packets, binary or ASCII,
// each waiting for the server's 0xFE02 answer, and answers AT$ commands
// as the device does ("$OK+tag", "$INFO+tag=…", a position report for
// AT$GPOS).
package atracksim

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cB-Sigma/trackers/atrack"
)

// Pos is one position report.
type Pos struct {
	Time     time.Time
	Lat, Lon float64
	Speed    float64 // km/h
	Heading  float64
	Sats     int
	// Report is the report ID (2 tracking; 101..164 user-defined events).
	Report int
}

// Tracker is one simulated device on a TCP connection.
type Tracker struct {
	IMEI string
	Unit uint64
	// ASCII sends positions in the ASCII form instead of binary.
	ASCII bool
	// Answer produces the answer to a command line ("AT$TRAC+5=1,30");
	// nil answers "$OK" with the command's tag (and $INFO to AT$INFO=?).
	Answer func(cmd string) string

	c    net.Conn
	seq  uint16
	wmu  sync.Mutex
	acks chan uint16
	mu   sync.Mutex
	cmds []string
	last Pos
}

// Dial connects a tracker with this IMEI (its unit ID) to addr.
func Dial(addr, imei string) (*Tracker, error) {
	unit, err := strconv.ParseUint(imei, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("atracksim: the unit ID %q is not a number", imei)
	}
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{IMEI: imei, Unit: unit, c: c, acks: make(chan uint16, 16)}
	go t.read()
	return t, nil
}

// Close closes the connection.
func (t *Tracker) Close() error { return t.c.Close() }

// Commands returns the command lines received so far.
func (t *Tracker) Commands() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cmds...)
}

func (t *Tracker) read() {
	defer close(t.acks)
	r := bufio.NewReader(t.c)
	for {
		b, err := r.Peek(1)
		if err != nil {
			return
		}
		if b[0] == 0xFE {
			var a [12]byte
			if _, err := io.ReadFull(r, a[:]); err != nil {
				return
			}
			t.acks <- binary.BigEndian.Uint16(a[10:])
			continue
		}
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		if cmd == "" {
			continue
		}
		t.mu.Lock()
		t.cmds = append(t.cmds, cmd)
		t.mu.Unlock()
		go t.respond(cmd)
	}
}

// tagOf reads the command name and tag of "AT$NAME+tag=…".
func tagOf(cmd string) (name, tag string) {
	head, _, _ := strings.Cut(cmd, "=")
	head = strings.TrimPrefix(strings.ToUpper(head), "AT$")
	name, tag, _ = strings.Cut(head, "+")
	return name, tag
}

func (t *Tracker) respond(cmd string) {
	name, tag := tagOf(cmd)
	plus := ""
	if tag != "" {
		plus = "+" + tag
	}
	if t.Answer != nil {
		_ = t.write([]byte(t.Answer(cmd) + "\r\n"))
		return
	}
	switch name {
	case "GPOS":
		t.mu.Lock()
		p := t.last
		t.mu.Unlock()
		p.Report = 0
		_ = t.write(t.frame(p))
	case "INFO":
		_ = t.write([]byte(fmt.Sprintf("$INFO%s=%d,AX7,SIM 1.0,%s,404450000000001,89914500000000000001,124,41,9,9,20,1,0\r\n", plus, t.Unit, t.IMEI)))
	default:
		_ = t.write([]byte("$OK" + plus + "\r\n"))
	}
}

func (t *Tracker) write(b []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.c.Write(b)
	return err
}

func (t *Tracker) next() uint16 {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	t.seq++
	return t.seq
}

func (t *Tracker) waitAck(seq uint16) error {
	timeout := time.After(5 * time.Second)
	var other []uint16
	for {
		select {
		case s, ok := <-t.acks:
			if !ok {
				return errors.New("atracksim: the server closed the connection")
			}
			if s == seq {
				return nil
			}
			other = append(other, s)
		case <-timeout:
			return fmt.Errorf("atracksim: no answer to sequence %d (answers to %v)", seq, other)
		}
	}
}

// Login sends a keep-alive (the protocol has no login) and waits for it
// to be echoed.
func (t *Tracker) Login() error {
	s := t.next()
	if err := t.write(atrack.Ack(t.Unit, s)); err != nil {
		return err
	}
	return t.waitAck(s)
}

// Report sends a position packet and waits for its answer.
func (t *Tracker) Report(p Pos) error {
	t.mu.Lock()
	t.last = p
	t.mu.Unlock()
	if p.Report == 0 {
		p.Report = 2
	}
	b := t.frame(p)
	if err := t.write(b); err != nil {
		return err
	}
	return t.waitAck(t.seqOf(b))
}

func (t *Tracker) seqOf(b []byte) uint16 {
	if t.ASCII {
		f := strings.SplitN(string(b), ",", 5)
		n, _ := strconv.Atoi(f[3])
		return uint16(n)
	}
	return binary.BigEndian.Uint16(b[6:8])
}

// frame builds a position packet with the custom fields %SA%MV%BV.
func (t *Tracker) frame(p Pos) []byte {
	s := t.next()
	tm := p.Time.UTC()
	if tm.IsZero() {
		tm = time.Now().UTC()
	}
	lon, lat := int32(math.Round(p.Lon*1e6)), int32(math.Round(p.Lat*1e6))
	if t.ASCII {
		rec := fmt.Sprintf("%d,%d,%d,%d,%d,%d,%d,0,10,1,%d,0,0,,2000,2000,\x1a,%%CI%%SA%%MV%%BV,%d,124,41\r\n",
			tm.Unix(), tm.Unix(), time.Now().Unix(), lon, lat, int(p.Heading), p.Report, int(math.Round(p.Speed)), p.Sats)
		return []byte(atrack.ASCIIFrame("@P", fmt.Sprintf("%d,%d,%s", s, t.Unit, rec)))
	}
	var r []byte
	r = binary.BigEndian.AppendUint32(r, uint32(tm.Unix()))
	r = binary.BigEndian.AppendUint32(r, uint32(tm.Unix()))
	r = binary.BigEndian.AppendUint32(r, uint32(time.Now().Unix()))
	r = binary.BigEndian.AppendUint32(r, uint32(lon))
	r = binary.BigEndian.AppendUint32(r, uint32(lat))
	r = binary.BigEndian.AppendUint16(r, uint16(p.Heading))
	r = append(r, byte(p.Report))
	r = binary.BigEndian.AppendUint32(r, 0)  // odometer
	r = binary.BigEndian.AppendUint16(r, 10) // HDOP 1.0
	r = append(r, 1)                         // inputs: ignition
	r = binary.BigEndian.AppendUint16(r, uint16(math.Round(p.Speed)))
	r = append(r, 0)                           // outputs
	r = binary.BigEndian.AppendUint16(r, 0)    // analogue input
	r = append(r, 0)                           // no driver ID
	r = binary.BigEndian.AppendUint16(r, 2000) // no temperature sensor
	r = binary.BigEndian.AppendUint16(r, 2000) // no temperature sensor
	r = append(r, 0)                           // no text message
	r = append(r, "%CI%SA%MV%BV"...)
	r = append(r, 0, byte(p.Sats))
	r = binary.BigEndian.AppendUint16(r, 124) // 12.4 V
	r = binary.BigEndian.AppendUint16(r, 41)  // 4.1 V
	return atrack.BinaryFrame("@P", s, t.Unit, r)
}
