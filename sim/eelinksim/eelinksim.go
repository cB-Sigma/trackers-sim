// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package eelinksim is an Eelink tracker (device protocol 2.0) for tests
// and demos: it logs in (0x01, waiting for the server's answer), sends
// heartbeats (0x03, answered), locations (0x12, not answered over TCP)
// and warnings (0x14, answered), and answers 0x80 instructions with the
// UID they carry, as the device does ("Relay enable OK", a WHERE
// position, …).
package eelinksim

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/cB-Sigma/trackers/eelink"
)

// Pos is one position report.
type Pos struct {
	Time     time.Time
	Lat, Lon float64
	Speed    float64 // km/h
	Heading  float64
	Sats     int
	Valid    bool
}

// Tracker is one simulated device on a TCP connection.
type Tracker struct {
	IMEI string
	// Answer produces the result text of a command; nil answers as the
	// device does.
	Answer func(cmd string) string

	c    net.Conn
	seq  uint16
	wmu  sync.Mutex
	acks chan *eelink.Package
	mu   sync.Mutex
	cmds []string
	last Pos
}

// Dial connects a tracker with this IMEI to addr.
func Dial(addr, imei string) (*Tracker, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{IMEI: imei, c: c, acks: make(chan *eelink.Package, 16)}
	go t.read()
	return t, nil
}

// Close closes the connection.
func (t *Tracker) Close() error { return t.c.Close() }

// Commands returns the command texts received so far.
func (t *Tracker) Commands() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cmds...)
}

func (t *Tracker) read() {
	defer close(t.acks)
	r := bufio.NewReader(t.c)
	for {
		p, err := eelink.ReadPackage(r)
		if err != nil {
			return
		}
		if p.PID == eelink.PInstruction && len(p.Content) >= 5 {
			cmd := string(p.Content[5:])
			t.mu.Lock()
			t.cmds = append(t.cmds, cmd)
			t.mu.Unlock()
			ans := t.answer(cmd)
			b := append([]byte{p.Content[0]}, p.Content[1:5]...)
			_ = t.write(eelink.Encode(eelink.PInstruction, p.Seq, append(b, ans...)))
			continue
		}
		t.acks <- p
	}
}

func (t *Tracker) answer(cmd string) string {
	if t.Answer != nil {
		return t.Answer(cmd)
	}
	c := strings.ToUpper(strings.TrimSpace(cmd))
	switch {
	case c == "RELAY,1#":
		return "Relay enable OK"
	case c == "RELAY,0#":
		return "Relay disable OK"
	case c == "RESET#":
		return "Reset OK"
	case strings.HasPrefix(c, "COLLECT,"):
		return "SET COLLECT OK"
	case c == "WHERE#":
		t.mu.Lock()
		p := t.last
		t.mu.Unlock()
		ns, ew := "N", "E"
		if p.Lat < 0 {
			ns = "S"
		}
		if p.Lon < 0 {
			ew = "W"
		}
		return fmt.Sprintf("Lat:%s%.6f\nLon:%s%.6f\nCourse:%.2f\nSpeed:%.2fkm/h\nDateTime:%s", ns, math.Abs(p.Lat), ew, math.Abs(p.Lon),
			p.Heading, p.Speed, p.Time.UTC().Format("2006-01-02 15:04:05"))
	}
	return "OK"
}

func (t *Tracker) write(b []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.c.Write(b)
	return err
}

func (t *Tracker) send(pid byte, content []byte, wait bool) error {
	t.wmu.Lock()
	t.seq++
	s := t.seq
	_, err := t.c.Write(eelink.Encode(pid, s, content))
	t.wmu.Unlock()
	if err != nil || !wait {
		return err
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case p, ok := <-t.acks:
			if !ok {
				return errors.New("eelinksim: the server closed the connection")
			}
			if p.PID == pid && p.Seq == s {
				return nil
			}
		case <-timeout:
			return fmt.Errorf("eelinksim: no answer to package 0x%02X", pid)
		}
	}
}

// Login sends the 2.0 login (English, UTC+5:30, version 2.0.5) and waits
// for its answer.
func (t *Tracker) Login() error {
	id := strings.Repeat("0", max(0, 16-len(t.IMEI))) + t.IMEI
	var c []byte
	for i := 0; i+1 < len(id); i += 2 {
		c = append(c, (id[i]-'0')<<4|(id[i+1]-'0'))
	}
	c = append(c, 0x01, 22, 0x02, 0x05, 0x02, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0)
	return t.send(eelink.PLogin, c, true)
}

// Heartbeat sends the status word and waits for the answer.
func (t *Tracker) Heartbeat(status uint16) error {
	return t.send(eelink.PHeartbeat, binary.BigEndian.AppendUint16(nil, status), true)
}

func position(p Pos) []byte {
	tm := p.Time.UTC()
	if tm.IsZero() {
		tm = time.Now().UTC()
	}
	b := binary.BigEndian.AppendUint32(nil, uint32(tm.Unix()))
	b = append(b, 0x03) // GPS and home cell
	b = binary.BigEndian.AppendUint32(b, uint32(int32(math.Round(p.Lat*1800000))))
	b = binary.BigEndian.AppendUint32(b, uint32(int32(math.Round(p.Lon*1800000))))
	b = binary.BigEndian.AppendUint16(b, 920) // altitude
	b = binary.BigEndian.AppendUint16(b, uint16(math.Round(p.Speed)))
	b = binary.BigEndian.AppendUint16(b, uint16(p.Heading))
	b = append(b, byte(p.Sats))
	b = binary.BigEndian.AppendUint16(b, 404) // MCC
	b = binary.BigEndian.AppendUint16(b, 45)  // MNC
	b = binary.BigEndian.AppendUint16(b, 0x1234)
	b = binary.BigEndian.AppendUint32(b, 0x00ABCDEF)
	return append(b, 80) // signal: -30 dBm
}

// status is the 2.0 status word: fixed, car with ignition on, charging.
func status(valid bool) []byte {
	s := uint16(0x0006 | 0x0180)
	if valid {
		s |= 1
	}
	return binary.BigEndian.AppendUint16(nil, s)
}

// Location sends a 0x12 location (not answered over TCP).
func (t *Tracker) Location(p Pos) error {
	t.mu.Lock()
	t.last = p
	t.mu.Unlock()
	b := append(position(p), status(p.Valid)...)
	b = binary.BigEndian.AppendUint16(b, 4100)    // battery mV
	b = append(b, 0, 0, 0, 0)                     // AIN0, AIN1
	b = binary.BigEndian.AppendUint32(b, 1234567) // mileage m
	b = append(b, make([]byte, 8+16)...)          // counters, pedometer, sensors
	return t.send(eelink.PLocation, b, false)
}

// Warning sends a 0x14 warning of this type (0x02 SOS, …) and waits for
// its answer.
func (t *Tracker) Warning(p Pos, typ byte) error {
	t.mu.Lock()
	t.last = p
	t.mu.Unlock()
	b := append(position(p), typ)
	return t.send(eelink.PWarning, append(b, status(p.Valid)...), true)
}
