// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package jt808sim

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/cB-Sigma/trackers/jt808"
)

// Resource is one recording the terminal lists in 0x1205.
type Resource struct {
	Channel    int
	Start, End time.Time
	Size       uint32
}

// Reboots counts the reboot commands (0x8105 command 4) answered.
func (t *Tracker) Reboots() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reboots
}

// Params are the parameters set by 0x8103, by ID.
func (t *Tracker) Params() map[uint32][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[uint32][]byte, len(t.params))
	for k, v := range t.params {
		out[k] = v
	}
	return out
}

// Interval is the report interval parameter 0x0029 last set (0 if never).
func (t *Tracker) Interval() time.Duration {
	v := t.Params()[jt808.ParamReportInterval]
	if len(v) != 4 {
		return 0
	}
	return time.Duration(binary.BigEndian.Uint32(v)) * time.Second
}

// PlatformKey is the platform's RSA public key once received (0x8A00).
func (t *Tracker) PlatformKey() *rsa.PublicKey {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.platKey
}

// seal encrypts a body for the platform when Encrypt is set and its key
// known (never the key exchange itself).
func (t *Tracker) seal(h *jt808.Header, body []byte) ([]byte, error) {
	k := t.PlatformKey()
	if !t.Encrypt || k == nil || h.ID == jt808.MTerminalRSA || len(body) == 0 {
		return body, nil
	}
	enc, err := jt808.EncryptBody(k, body)
	if err != nil {
		return nil, err
	}
	h.Props |= jt808.PropRSA
	return enc, nil
}

// platformKey keeps the key of a 0x8A00; it reports whether the frame
// answers ExchangeKeys (else it is a command, answered 0x0A00).
func (t *Tracker) platformKey(f *jt808.Frame) bool {
	k, err := jt808.ParsePublicKeyBody(f.Body)
	if err != nil {
		return false
	}
	t.mu.Lock()
	t.platKey = k
	w := t.keyWait
	t.keyWait = nil
	t.mu.Unlock()
	if w != nil {
		w <- k
		return true
	}
	return false
}

func (t *Tracker) key() (*rsa.PrivateKey, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Key == nil {
		k, err := rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			return nil, err
		}
		t.Key = k
	}
	return t.Key, nil
}

// ExchangeKeys sends the terminal's RSA public key (0x0A00, a 1024-bit
// key made when Key is nil) and waits for the platform's (0x8A00). A
// platform without a key answers 0x8001, an error here.
func (t *Tracker) ExchangeKeys() error {
	k, err := t.key()
	if err != nil {
		return err
	}
	w := make(chan *rsa.PublicKey, 1)
	t.mu.Lock()
	t.keyWait = w
	t.mu.Unlock()
	serial, err := t.Send(jt808.MTerminalRSA, jt808.PublicKeyBody(&k.PublicKey))
	if err != nil {
		return err
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-w:
			return nil
		case f, ok := <-t.acks:
			if !ok {
				return errors.New("connection closed")
			}
			if f.ID == jt808.MPlatformResponse && len(f.Body) >= 5 && binary.BigEndian.Uint16(f.Body) == serial {
				return fmt.Errorf("the platform answered 0x0A00 with result %d and no key of its own", f.Body[4])
			}
		case <-deadline:
			return errors.New("no 0x8A00 from the platform")
		}
	}
}

// waitAck waits for the platform's 0x8001 to the message with serial.
func (t *Tracker) waitAck(serial, id uint16) error {
	for {
		f, err := t.Ack(3 * time.Second)
		if err != nil {
			return err
		}
		if f.ID != jt808.MPlatformResponse || len(f.Body) < 5 || binary.BigEndian.Uint16(f.Body) != serial {
			continue
		}
		if got := binary.BigEndian.Uint16(f.Body[2:]); got != id {
			return fmt.Errorf("0x8001 answers message 0x%04X, not 0x%04X", got, id)
		}
		if f.Body[4] != 0 {
			return fmt.Errorf("refused with result %d", f.Body[4])
		}
		return nil
	}
}

// BatchParts sends a 0x0704 batch cut into subpackages of at most size
// body bytes, each waiting for the platform's answer, as terminals send a
// batch longer than one message holds.
func (t *Tracker) BatchParts(blind bool, size int, ps ...Pos) error {
	if size < 1 {
		return errors.New("a subpackage holds at least 1 byte")
	}
	b := binary.BigEndian.AppendUint16(nil, uint16(len(ps)))
	typ := byte(0)
	if blind {
		typ = 1
	}
	b = append(b, typ)
	for _, p := range ps {
		l := LocationBody(p, t.FuelCut(), t.Zone)
		b = binary.BigEndian.AppendUint16(b, uint16(len(l)))
		b = append(b, l...)
	}
	total := (len(b) + size - 1) / size
	for i := range total {
		h := t.header(jt808.MBatch)
		h.Total, h.Index = uint16(total), uint16(i+1)
		piece, err := t.seal(&h, b[i*size:min(len(b), (i+1)*size)])
		if err != nil {
			return err
		}
		if err := t.SendRaw(jt808.EncodeWith(h, piece)); err != nil {
			return err
		}
		if err := t.waitAck(h.Serial, jt808.MBatch); err != nil {
			return fmt.Errorf("piece %d of %d: %w", i+1, total, err)
		}
	}
	return nil
}

// Safety is an active safety alarm of an ADAS (item 0x64) or DSM (0x65)
// terminal.
type Safety struct {
	// DSM sends item 0x65; otherwise 0x64.
	DSM bool
	// ID is the alarm ID; Type the alarm type (ADAS 1 forward collision,
	// 2 lane departure, 3 headway, 4 pedestrian; DSM 1 fatigue, 2 phone,
	// 3 smoking, 4 distraction, 5 driver absent); Level 1 or 2; State 0
	// event, 1 start, 2 end.
	ID                 uint32
	Type, Level, State byte
	// Attachments is the number of files the alarm label announces.
	Attachments byte
}

// SafetyItem encodes the 0x64 or 0x65 item of alarm a at position p (a
// 16-byte alarm label with this terminal's ID), to put in Pos.Items.
func (t *Tracker) SafetyItem(p Pos, a Safety) []byte {
	v := binary.BigEndian.AppendUint32(nil, a.ID)
	v = append(v, a.State, a.Type, a.Level)
	v = append(v, 0, 0, 0, 0, 0) // ADAS: front speed, distance, departure, sign type and value; DSM: fatigue, reserved
	v = append(v, byte(p.Speed))
	v = binary.BigEndian.AppendUint16(v, uint16(int16(p.Alt)))
	v = binary.BigEndian.AppendUint32(v, uint32(abs(p.Lat)*1e6+0.5))
	v = binary.BigEndian.AppendUint32(v, uint32(abs(p.Lon)*1e6+0.5))
	tm := p.Time
	if tm.IsZero() {
		tm = time.Now()
	}
	v = append(v, jt808.BCDTime(tm, t.Zone)...)
	v = append(v, 0, 0) // vehicle status
	id := []byte(t.Phone)
	if len(id) > 7 {
		id = id[len(id)-7:]
	}
	v = append(v, pad(string(id), 7)...)
	v = append(v, jt808.BCDTime(tm, t.Zone)...)
	v = append(v, byte(a.ID), a.Attachments, 0)
	item := byte(0x64)
	if a.DSM {
		item = 0x65
	}
	return append([]byte{item, byte(len(v))}, v...)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// SafetyAlarm sends a location with alarm a and waits for the answer.
func (t *Tracker) SafetyAlarm(p Pos, a Safety) error {
	p.Items = append(append([]byte(nil), p.Items...), t.SafetyItem(p, a)...)
	return t.Location(p)
}

// respondMore answers the commands of the later additions; handled is
// false for those answered with a plain 0x0001.
func (t *Tracker) respondMore(cmd Command) (bool, error) {
	switch cmd.ID {
	case jt808.MTerminalControl:
		if len(cmd.Body) < 1 || cmd.Body[0] != jt808.CtrlReset {
			return false, nil
		}
		err := t.General(cmd.Serial, cmd.ID, 0)
		t.mu.Lock()
		t.reboots++
		t.mu.Unlock()
		if t.RebootDrops {
			_ = t.Close()
		}
		return true, err
	case jt808.MSetParams:
		b := cmd.Body
		if len(b) < 1 {
			return true, t.General(cmd.Serial, cmd.ID, jt808.ResultBadMessage)
		}
		n, rest := int(b[0]), b[1:]
		set := map[uint32][]byte{}
		for range n {
			if len(rest) < 5 || len(rest) < 5+int(rest[4]) {
				return true, t.General(cmd.Serial, cmd.ID, jt808.ResultBadMessage)
			}
			l := int(rest[4])
			set[binary.BigEndian.Uint32(rest)] = append([]byte(nil), rest[5:5+l]...)
			rest = rest[5+l:]
		}
		t.mu.Lock()
		if t.params == nil {
			t.params = map[uint32][]byte{}
		}
		for k, v := range set {
			t.params[k] = v
		}
		t.mu.Unlock()
		return true, t.General(cmd.Serial, cmd.ID, 0)
	case jt808.MAVAttributesQuery:
		// AAC audio (19), mono, 8 kHz, 16 bits, 320-sample frames, audio
		// output, H.264 video (98), 1 audio and 4 video channels.
		_, err := t.Send(jt808.MAVAttributes, []byte{19, 1, 0, 1, 0x01, 0x40, 1, 98, 1, 4})
		return true, err
	case jt808.MResourceQuery:
		_, err := t.Send(jt808.MResourceList, t.resourceList(cmd))
		return true, err
	case jt808.MPlatformRSA:
		k, err := t.key()
		if err != nil {
			return true, err
		}
		_, err = t.Send(jt808.MTerminalRSA, jt808.PublicKeyBody(&k.PublicKey))
		return true, err
	}
	return false, nil
}

// resourceList is the 0x1205 answer to a 0x9205 query: Resources, or one
// recording of the queried channel and time.
func (t *Tracker) resourceList(cmd Command) []byte {
	rs := t.Resources
	if rs == nil {
		ch, start, end := 1, time.Now().Add(-time.Hour), time.Now()
		if len(cmd.Body) >= 13 {
			if cmd.Body[0] != 0 {
				ch = int(cmd.Body[0])
			}
			if s, ok := readBCD(cmd.Body[1:7], t.Zone); ok {
				start = s
			}
			if e, ok := readBCD(cmd.Body[7:13], t.Zone); ok {
				end = e
			}
		}
		rs = []Resource{{Channel: ch, Start: start, End: end, Size: 1 << 20}}
	}
	b := binary.BigEndian.AppendUint16(nil, cmd.Serial)
	b = binary.BigEndian.AppendUint32(b, uint32(len(rs)))
	for _, r := range rs {
		b = append(b, byte(r.Channel))
		b = append(b, jt808.BCDTime(r.Start, t.Zone)...)
		b = append(b, jt808.BCDTime(r.End, t.Zone)...)
		b = append(b, make([]byte, 8)...) // alarm flags
		b = append(b, 0, 1, 1)            // audio and video, main stream, main storage
		b = binary.BigEndian.AppendUint32(b, r.Size)
	}
	return b
}

func readBCD(b []byte, loc *time.Location) (time.Time, bool) {
	var v [6]int
	for i := range 6 {
		hi, lo := int(b[i]>>4), int(b[i]&0x0F)
		if hi > 9 || lo > 9 {
			return time.Time{}, false
		}
		v[i] = hi*10 + lo
	}
	if v[1] < 1 || v[1] > 12 || v[2] < 1 {
		return time.Time{}, false
	}
	return time.Date(2000+v[0], time.Month(v[1]), v[2], v[3], v[4], v[5], 0, loc), true
}
