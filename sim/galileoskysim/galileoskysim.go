// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package galileoskysim is a Galileosky terminal for tests and demos: it
// sends its head packet (hardware and firmware version, IMEI, device
// identifier) and main packets of records (time, coordinates, speed and
// direction, altitude, HDOP, status, voltages, inputs, odometer), waits
// for each confirmation (0x02 and the packet's checksum), and answers command packets (tags 0xE0 and 0xE1) with
// the same command number, in the forms of the manual's command list.
package galileoskysim

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

	"github.com/cB-Sigma/trackers/galileosky"
)

// Tracker is one simulated device.
type Tracker struct {
	IMEI     string
	DeviceID uint16
	// Answer produces the answer to a command; nil answers as Answer.
	Answer func(cmd string) string
	// Hold keeps commands unanswered.
	Hold bool

	c       net.Conn
	wmu     sync.Mutex
	confirm chan []byte
	done    chan struct{}

	mu   sync.Mutex
	cmds []string
	out  uint16 // output states, for Out answers
	rec  uint16
}

// Rec is one simulated record.
type Rec struct {
	Time     time.Time
	Lat, Lon float64
	Speed    float64 // km/h
	Heading  float64
	Alt      float64
	Sats     int
	HDOP     float64
	Status   uint16 // the device status word (tag 0x40)
	Power    float64
	Battery  float64 // V
	Inputs   uint16
	Odometer uint32 // m
}

// Dial connects to the server.
func Dial(addr, imei string) (*Tracker, error) {
	if len(imei) != 15 {
		return nil, errors.New("galileoskysim: the IMEI has 15 digits")
	}
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{IMEI: imei, DeviceID: 1, c: c, confirm: make(chan []byte, 16), done: make(chan struct{})}
	go t.read()
	return t, nil
}

// Close drops the connection.
func (t *Tracker) Close() error { return t.c.Close() }

// Done is closed when the server closed the connection.
func (t *Tracker) Done() <-chan struct{} { return t.done }

// Commands lists the command texts received so far.
func (t *Tracker) Commands() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cmds...)
}

func (t *Tracker) read() {
	defer close(t.done)
	br := bufio.NewReader(t.c)
	for {
		head, body, err := galileosky.ReadFrame(br)
		if err != nil {
			return
		}
		switch head {
		case galileosky.HeadConfirm:
			select {
			case t.confirm <- body:
			default:
			}
		case galileosky.HeadMain:
			num, text, ok := command(body)
			if !ok {
				continue
			}
			t.mu.Lock()
			t.cmds = append(t.cmds, text)
			hold := t.Hold
			t.mu.Unlock()
			if hold {
				continue
			}
			ans := ""
			if t.Answer != nil {
				ans = t.Answer(text)
			} else {
				ans = t.answer(text)
			}
			go func() { _ = t.Reply(num, ans) }()
		}
	}
}

// command reads a command packet's number and text.
func command(b []byte) (uint32, string, bool) {
	var num uint32
	var text string
	var hasNum, hasText bool
	for len(b) > 0 {
		tag := b[0]
		b = b[1:]
		switch tag {
		case 0x03:
			if len(b) < 15 {
				return 0, "", false
			}
			b = b[15:]
		case 0x04:
			if len(b) < 2 {
				return 0, "", false
			}
			b = b[2:]
		case 0xE0:
			if len(b) < 4 {
				return 0, "", false
			}
			num, hasNum, b = binary.LittleEndian.Uint32(b), true, b[4:]
		case 0xE1:
			if len(b) < 1 || len(b) < 1+int(b[0]) {
				return 0, "", false
			}
			text, hasText, b = string(b[1:1+int(b[0])]), true, b[1+int(b[0]):]
		default:
			return 0, "", false
		}
	}
	return num, text, hasNum && hasText
}

// answer is the device's answer, in the forms of the manual's examples.
func (t *Tracker) answer(cmd string) string {
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return "INCORRECT COMMAND"
	}
	switch strings.ToLower(f[0]) {
	case "status":
		t.mu.Lock()
		n := t.rec
		t.mu.Unlock()
		return fmt.Sprintf("Dev%d Soft=19 Pack=%d Nav=0 Lat=12.9716 Lon=77.5946 Speed=0 HDOP=0.9 SatCnt=9", t.DeviceID, n)
	case "imei":
		return "IMEI " + t.IMEI
	case "reset":
		return "Reset of device"
	case "out":
		var v, s int
		if len(f) != 2 {
			return "INCORRECT COMMAND"
		}
		if _, err := fmt.Sscanf(f[1], "%d,%d", &v, &s); err != nil || v < 0 || v > 3 || s < 0 || s > 1 {
			return "INCORRECT COMMAND"
		}
		t.mu.Lock()
		if s == 1 {
			t.out |= 1 << v
		} else {
			t.out &^= 1 << v
		}
		o := t.out
		t.mu.Unlock()
		return fmt.Sprintf("OUT(3..0) = %04b", o)
	case "wrperiod":
		var x, y int
		if len(f) != 2 {
			return "INCORRECT COMMAND"
		}
		if _, err := fmt.Sscanf(f[1], "%d,%d", &x, &y); err != nil {
			return "INCORRECT COMMAND"
		}
		return fmt.Sprintf("WRPERIOD move=%d parking=%d", x, y)
	}
	return "INCORRECT COMMAND"
}

func (t *Tracker) write(b []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.c.Write(b)
	return err
}

// Reply sends an answer packet with this command number.
func (t *Tracker) Reply(num uint32, text string) error {
	if len(text) > 255 {
		text = text[:255]
	}
	return t.write(galileosky.CommandFrame(t.IMEI, t.DeviceID, num, text))
}

// Send sends a packet and waits for the confirmation with its checksum
// (confirmations of other packets, such as command answers, are passed
// over).
func (t *Tracker) Send(packet []byte) error {
	if err := t.write(packet); err != nil {
		return err
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case c := <-t.confirm:
			if len(c) == 2 && c[0] == packet[len(packet)-2] && c[1] == packet[len(packet)-1] {
				return nil
			}
		case <-t.done:
			return errors.New("galileoskysim: the server closed the connection")
		case <-timeout:
			return errors.New("galileoskysim: no confirmation")
		}
	}
}

// HeadTags are the head packet's tags.
func (t *Tracker) HeadTags() []byte {
	b := []byte{0x01, 0x82, 0x02, 0x13, 0x03}
	b = append(b, t.IMEI...)
	b = append(b, 0x04)
	return binary.LittleEndian.AppendUint16(b, t.DeviceID)
}

// Head sends the head packet and waits for its confirmation.
func (t *Tracker) Head() error { return t.Send(galileosky.Frame(t.HeadTags(), false)) }

// RecordTags are the tags of one record.
func (t *Tracker) RecordTags(r Rec) []byte {
	t.mu.Lock()
	t.rec++
	n := t.rec
	t.mu.Unlock()
	b := []byte{0x10}
	b = binary.LittleEndian.AppendUint16(b, n)
	b = append(b, 0x20)
	b = binary.LittleEndian.AppendUint32(b, uint32(r.Time.Unix()))
	sats := r.Sats
	if sats > 15 {
		sats = 15
	}
	b = append(b, 0x30, byte(sats))
	b = binary.LittleEndian.AppendUint32(b, uint32(int32(math.Round(r.Lat*1e6))))
	b = binary.LittleEndian.AppendUint32(b, uint32(int32(math.Round(r.Lon*1e6))))
	b = append(b, 0x33)
	b = binary.LittleEndian.AppendUint16(b, uint16(math.Round(r.Speed*10)))
	b = binary.LittleEndian.AppendUint16(b, uint16(math.Round(r.Heading*10)))
	b = append(b, 0x34)
	b = binary.LittleEndian.AppendUint16(b, uint16(int16(math.Round(r.Alt))))
	b = append(b, 0x35, byte(math.Round(r.HDOP*10)))
	b = append(b, 0x40)
	b = binary.LittleEndian.AppendUint16(b, r.Status)
	b = append(b, 0x41)
	b = binary.LittleEndian.AppendUint16(b, uint16(math.Round(r.Power*1000)))
	b = append(b, 0x42)
	b = binary.LittleEndian.AppendUint16(b, uint16(math.Round(r.Battery*1000)))
	b = append(b, 0x46)
	b = binary.LittleEndian.AppendUint16(b, r.Inputs)
	b = append(b, 0xD4)
	return binary.LittleEndian.AppendUint32(b, r.Odometer)
}

// Records sends a main packet of these records; archive sets the "archive
// holds more" flag.
func (t *Tracker) Records(archive bool, recs ...Rec) error {
	var tags []byte
	for _, r := range recs {
		tags = append(tags, t.RecordTags(r)...)
	}
	return t.Send(galileosky.Frame(tags, archive))
}
