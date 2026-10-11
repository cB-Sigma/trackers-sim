// Package teltonikasim is a Teltonika tracker for tests and demos: IMEI
// handshake, Codec 8 / 8E AVL packets over TCP or UDP, and Codec 12
// command answers.
package teltonikasim

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
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
}

// AVL encodes records as an AVL data array (codec..count2) in Codec 8 or 8E.
func AVL(ext bool, recs []Record) []byte {
	codec := byte(teltonika.Codec8)
	if ext {
		codec = teltonika.Codec8E
	}
	b := []byte{codec, byte(len(recs))}
	id := func(v uint16) {
		if ext {
			b = binary.BigEndian.AppendUint16(b, v)
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
		id(uint16(len(r.IO1) + len(r.IO2) + len(r.IO4) + len(r.IO8)))
		id(uint16(len(r.IO1)))
		for k, v := range r.IO1 {
			id(k)
			b = append(b, v)
		}
		id(uint16(len(r.IO2)))
		for k, v := range r.IO2 {
			id(k)
			b = binary.BigEndian.AppendUint16(b, v)
		}
		id(uint16(len(r.IO4)))
		for k, v := range r.IO4 {
			id(k)
			b = binary.BigEndian.AppendUint32(b, v)
		}
		id(uint16(len(r.IO8)))
		for k, v := range r.IO8 {
			id(k)
			b = binary.BigEndian.AppendUint64(b, v)
		}
		if ext {
			b = binary.BigEndian.AppendUint16(b, 0)
		}
	}
	return append(b, byte(len(recs)))
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
	IMEI   string
	Answer func(cmd string) string // nil answers "OK"
	// Delay, when set, holds the answer to a command this long (a slow
	// device, or one whose answer arrives after the server gave up).
	Delay func(cmd string) time.Duration

	c    net.Conn
	acks chan []byte
	mu   sync.Mutex
	cmds []string
}

// Dial connects and performs the IMEI handshake; it reports whether the
// server accepted the IMEI.
func Dial(addr, imei string) (*Tracker, bool, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, false, err
	}
	t := &Tracker{IMEI: imei, c: c, acks: make(chan []byte, 16)}
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
		text, typ, err := teltonika.ParseCodec12(d[:n])
		if err != nil || typ != teltonika.TypeCmd {
			continue
		}
		t.mu.Lock()
		t.cmds = append(t.cmds, text)
		t.mu.Unlock()
		ans := "OK"
		if t.Answer != nil {
			ans = t.Answer(text)
		}
		if t.Delay != nil {
			if dt := t.Delay(text); dt > 0 {
				go func() {
					time.Sleep(dt)
					_, _ = t.c.Write(teltonika.Codec12Frame(teltonika.TypeResp, ans))
				}()
				continue
			}
		}
		_, _ = t.c.Write(teltonika.Codec12Frame(teltonika.TypeResp, ans))
	}
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
