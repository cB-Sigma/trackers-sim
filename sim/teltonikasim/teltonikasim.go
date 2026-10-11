// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package teltonikasim is a Teltonika tracker for tests and demos: IMEI
// handshake, Codec 8 / 8E / 16 AVL packets over TCP or UDP, Codec 13 and
// Codec 15 device messages, and answers to Codec 12 commands (in Codec
// 12, or Codec 13 / 15 when set) and Codec 14 commands (an ACK for its own
// IMEI, the "IMEI does not match" nACK otherwise). It keeps the values of
// setparam commands, answers getparam, and takes its reporting period from
// the data acquisition parameters (Interval).
package teltonikasim

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

	"github.com/cB-Sigma/trackers/teltonika"
	"github.com/cB-Sigma/trackers/track"
)

// Record is one AVL record to send.
type Record struct {
	Time     time.Time
	Priority byte
	Lat, Lon float64
	Alt      int16
	Angle    uint16
	Sats     byte
	Speed    uint16
	Event    uint16
	IO1      map[uint16]uint8
	IO2      map[uint16]uint16
	IO4      map[uint16]uint32
	IO8      map[uint16]uint64
	// Generation is Codec 16's record generation type (7 periodical).
	Generation byte
}

// AVL encodes records as an AVL data array (codec..count2) in Codec 8 or 8E.
func AVL(ext bool, recs []Record) []byte {
	codec := byte(teltonika.Codec8)
	if ext {
		codec = teltonika.Codec8E
	}
	b, _ := AVLCodec(codec, recs)
	return b
}

// AVLCodec encodes records as an AVL data array in Codec 8, 8E or 16.
func AVLCodec(codec byte, recs []Record) ([]byte, error) {
	if codec != teltonika.Codec8 && codec != teltonika.Codec8E && codec != teltonika.Codec16 {
		return nil, fmt.Errorf("teltonikasim: codec 0x%02X is not an AVL codec", codec)
	}
	ext := codec == teltonika.Codec8E
	b := []byte{codec, byte(len(recs))}
	// IDs: 1 byte in Codec 8, 2 in 8E and 16; counts: 2 bytes in 8E only.
	id := func(v uint16) {
		if codec == teltonika.Codec8 {
			b = append(b, byte(v))
		} else {
			b = binary.BigEndian.AppendUint16(b, v)
		}
	}
	count := func(v int) {
		if ext {
			b = binary.BigEndian.AppendUint16(b, uint16(v))
		} else {
			b = append(b, byte(v))
		}
	}
	for _, r := range recs {
		b = binary.BigEndian.AppendUint64(b, uint64(r.Time.UnixMilli()))
		b = append(b, r.Priority)
		b = binary.BigEndian.AppendUint32(b, uint32(int32(math.Round(r.Lon*1e7))))
		b = binary.BigEndian.AppendUint32(b, uint32(int32(math.Round(r.Lat*1e7))))
		b = binary.BigEndian.AppendUint16(b, uint16(r.Alt))
		b = binary.BigEndian.AppendUint16(b, r.Angle)
		b = append(b, r.Sats)
		b = binary.BigEndian.AppendUint16(b, r.Speed)
		id(r.Event)
		if codec == teltonika.Codec16 {
			b = append(b, r.Generation)
		}
		count(len(r.IO1) + len(r.IO2) + len(r.IO4) + len(r.IO8))
		count(len(r.IO1))
		for k, v := range r.IO1 {
			id(k)
			b = append(b, v)
		}
		count(len(r.IO2))
		for k, v := range r.IO2 {
			id(k)
			b = binary.BigEndian.AppendUint16(b, v)
		}
		count(len(r.IO4))
		for k, v := range r.IO4 {
			id(k)
			b = binary.BigEndian.AppendUint32(b, v)
		}
		count(len(r.IO8))
		for k, v := range r.IO8 {
			id(k)
			b = binary.BigEndian.AppendUint64(b, v)
		}
		if ext {
			b = binary.BigEndian.AppendUint16(b, 0)
		}
	}
	return append(b, byte(len(recs))), nil
}

// TCPPacket wraps an AVL data array with preamble, length and CRC.
func TCPPacket(data []byte) []byte {
	out := make([]byte, 4, len(data)+12)
	out = binary.BigEndian.AppendUint32(out, uint32(len(data)))
	out = append(out, data...)
	return binary.BigEndian.AppendUint32(out, uint32(track.CRC16IBM(data)))
}

// UDPPacket wraps an AVL data array in the UDP channel header.
func UDPPacket(packetID uint16, avlID byte, imei string, data []byte) []byte {
	body := binary.BigEndian.AppendUint16(nil, packetID)
	body = append(body, 0x01, avlID)
	body = binary.BigEndian.AppendUint16(body, uint16(len(imei)))
	body = append(body, imei...)
	body = append(body, data...)
	return append(binary.BigEndian.AppendUint16(nil, uint16(len(body))), body...)
}

// Tracker is a TCP device.
type Tracker struct {
	IMEI string
	// Answer, when set, gives the answer text to a command; nil answers
	// setparam with "New value …", getparam with "Param ID:… Value:…" and
	// anything else with "OK".
	Answer func(cmd string) string
	// Delay, when set, holds the answer to a command this long (a slow
	// device, or one whose answer arrives after the server gave up).
	Delay func(cmd string) time.Duration

	c        net.Conn
	acks     chan []byte
	mu       sync.Mutex
	cmds     []string
	params   map[int]string
	answerIn byte
}

// Dial connects and performs the IMEI handshake; it reports whether the
// server accepted the IMEI.
func Dial(addr, imei string) (*Tracker, bool, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, false, err
	}
	return Accept(c, imei)
}

// Accept performs the IMEI handshake on a connection the caller opened
// (or a pipe in tests) and starts reading the server's frames. On a
// refusal the connection is closed and ok is false.
func Accept(c net.Conn, imei string) (t *Tracker, ok bool, err error) {
	t = &Tracker{IMEI: imei, c: c, acks: make(chan []byte, 16), params: map[int]string{}}
	hs := binary.BigEndian.AppendUint16(nil, uint16(len(imei)))
	if _, err := c.Write(append(hs, imei...)); err != nil {
		return nil, false, err
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var b [1]byte
	if _, err := io.ReadFull(c, b[:]); err != nil {
		_ = c.Close()
		return nil, false, nil // refused and closed
	}
	_ = c.SetReadDeadline(time.Time{})
	if b[0] != 1 {
		_ = c.Close()
		return nil, false, nil
	}
	go t.read()
	return t, true, nil
}

// SetAnswerCodec chooses how Codec 12 commands are answered: 0 or
// teltonika.Codec12 (the default), teltonika.Codec13 (a timestamped
// message, as with the "Message Timestamp" setting) or teltonika.Codec15
// (timestamp and IMEI). Codec 14 commands are always answered in Codec 14.
func (t *Tracker) SetAnswerCodec(codec byte) {
	t.mu.Lock()
	t.answerIn = codec
	t.mu.Unlock()
}

func (t *Tracker) read() {
	br := bufio.NewReader(t.c)
	for {
		p, err := br.Peek(4)
		if err != nil {
			close(t.acks)
			return
		}
		if binary.BigEndian.Uint32(p) != 0 { // a 4-byte acknowledgement
			var a [4]byte
			_, _ = io.ReadFull(br, a[:])
			t.acks <- a[:]
			continue
		}
		var hd [8]byte
		if _, err := io.ReadFull(br, hd[:]); err != nil {
			close(t.acks)
			return
		}
		n := binary.BigEndian.Uint32(hd[4:])
		if n == 0 { // an acknowledgement of zero records
			t.acks <- hd[4:]
			continue
		}
		d := make([]byte, n+4)
		if _, err := io.ReadFull(br, d); err != nil {
			close(t.acks)
			return
		}
		t.command(d[:n])
	}
}

// command handles one command frame's data field (codec..count2).
func (t *Tracker) command(d []byte) {
	var text string
	var typ byte
	var err error
	codec14 := len(d) > 0 && d[0] == teltonika.Codec14
	if codec14 {
		text, typ, err = teltonika.ParseCodec14(d)
	} else {
		text, typ, err = teltonika.ParseCodec12(d)
	}
	if err != nil || typ != teltonika.TypeCmd {
		return
	}
	t.mu.Lock()
	t.cmds = append(t.cmds, text)
	answerIn := t.answerIn
	t.mu.Unlock()
	if codec14 {
		to, err := teltonika.ParseCodec14IMEI(d)
		if err != nil || to != strings.TrimLeft(t.IMEI, "0") {
			// Not this device: not run, answered with the nACK type.
			if f, err := teltonika.Codec14Message(t.IMEI, teltonika.TypeNotMine, ""); err == nil {
				_, _ = t.c.Write(f)
			}
			return
		}
	}
	ans := t.run(text)
	if t.Answer != nil {
		ans = t.Answer(text)
	}
	var f []byte
	switch {
	case codec14:
		f, err = teltonika.Codec14Message(t.IMEI, teltonika.TypeResp, ans)
	case answerIn == teltonika.Codec13:
		f = teltonika.Codec13Frame(time.Now(), ans)
	case answerIn == teltonika.Codec15:
		f, err = teltonika.Codec15Frame(t.IMEI, codec15Type, time.Now(), ans)
	default:
		f = teltonika.Codec12Frame(teltonika.TypeResp, ans)
	}
	if err != nil {
		return
	}
	if t.Delay != nil {
		if dt := t.Delay(text); dt > 0 {
			go func() {
				time.Sleep(dt)
				_, _ = t.c.Write(f)
			}()
			return
		}
	}
	_, _ = t.c.Write(f)
}

// codec15Type is the message type of the Codec 15 messages sent (the
// value in Teltonika's example; devices make it a setting).
const codec15Type = 0x0B

// run carries out setparam and getparam and returns the device's answer.
func (t *Tracker) run(cmd string) string {
	verb, args, _ := strings.Cut(strings.TrimSpace(cmd), " ")
	switch strings.ToLower(verb) {
	case "setparam":
		var set []string
		t.mu.Lock()
		for _, pair := range strings.Split(args, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(pair), ":")
			id, err := strconv.Atoi(k)
			if !ok || err != nil {
				continue
			}
			t.params[id] = v
			set = append(set, k+":"+v)
		}
		t.mu.Unlock()
		return "New value " + strings.Join(set, ";")
	case "getparam":
		var out []string
		t.mu.Lock()
		for _, k := range strings.Split(args, ";") {
			k = strings.TrimSpace(k)
			id, err := strconv.Atoi(k)
			if err != nil {
				continue
			}
			out = append(out, "Param ID:"+k+" Value:"+t.params[id])
		}
		t.mu.Unlock()
		return strings.Join(out, "; ")
	}
	return "OK"
}

// Params returns the values setparam commands gave, by parameter ID.
func (t *Tracker) Params() map[int]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[int]string, len(t.params))
	for k, v := range t.params {
		out[k] = v
	}
	return out
}

// Interval is the reporting period the server set: the home network
// moving "Min Period" (parameter 10050), or 0 when none was set.
func (t *Tracker) Interval() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, err := strconv.Atoi(t.params[10050])
	if err != nil || s <= 0 {
		return 0
	}
	return time.Duration(s) * time.Second
}

// Commands lists the commands received.
func (t *Tracker) Commands() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cmds...)
}

// Send sends records and returns the number the server acknowledged.
func (t *Tracker) Send(ext bool, recs ...Record) (int, error) {
	if _, err := t.c.Write(TCPPacket(AVL(ext, recs))); err != nil {
		return 0, err
	}
	return t.ack()
}

// SendCodec sends records in Codec 8, 8E or 16 and returns the number the
// server acknowledged.
func (t *Tracker) SendCodec(codec byte, recs ...Record) (int, error) {
	data, err := AVLCodec(codec, recs)
	if err != nil {
		return 0, err
	}
	return t.SendRaw(TCPPacket(data))
}

// SendMessage sends a one-way device message in Codec 13 (with its time)
// or Codec 15 (with its time and the IMEI); the server does not answer.
func (t *Tracker) SendMessage(codec byte, at time.Time, text string) error {
	var f []byte
	var err error
	switch codec {
	case teltonika.Codec13:
		f = teltonika.Codec13Frame(at, text)
	case teltonika.Codec15:
		f, err = teltonika.Codec15Frame(t.IMEI, codec15Type, at, text)
	default:
		return fmt.Errorf("teltonikasim: codec 0x%02X is not a device message codec", codec)
	}
	if err != nil {
		return err
	}
	_, err = t.c.Write(f)
	return err
}

// SendRaw writes bytes and waits for an acknowledgement.
func (t *Tracker) SendRaw(b []byte) (int, error) {
	if _, err := t.c.Write(b); err != nil {
		return 0, err
	}
	return t.ack()
}

func (t *Tracker) ack() (int, error) {
	select {
	case a, ok := <-t.acks:
		if !ok {
			return 0, errors.New("connection closed")
		}
		return int(binary.BigEndian.Uint32(a)), nil
	case <-time.After(2 * time.Second):
		return 0, errors.New("no acknowledgement")
	}
}

// Close drops the connection.
func (t *Tracker) Close() error { return t.c.Close() }
