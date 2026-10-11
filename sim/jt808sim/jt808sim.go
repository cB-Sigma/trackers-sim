// Package jt808sim is a JT/T 808 vehicle terminal for tests and demos,
// behaving like a Jimi JT808 tracker: it registers (0x0100) and keeps the
// authentication code it is given, authenticates (0x0102), sends
// heartbeats, locations and batches and waits for the platform's
// answers, and answers commands: 0x0001 general responses, 0x0201 to a
// location query, 0x0500 with its fuel line status to vehicle control,
// and a 0x6006 text (or 0x0900 pass-through) answer after a text command.
package jt808sim

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/cB-Sigma/trackers/jt808"
)

// Tracker is one simulated terminal.
type Tracker struct {
	// Phone is the terminal phone number (12 digits for the 2013 header,
	// up to 20 for the 2019 one).
	Phone string
	// V2019 sends the 2019 header (protocol version 1, 10-byte phone).
	V2019 bool
	// Zone is the device clock's zone (default UTC+8, as the standard).
	Zone *time.Location
	// Result is the general response result to a command (nil: 0, ok).
	Result func(id uint16, text string) byte
	// Answer is the text answer to a text command (nil: "OK"); "" sends
	// none.
	Answer func(text string) string
	// Delay holds the answers to a command this long.
	Delay func(id uint16, text string) time.Duration
	// Hold keeps commands unanswered; Received lists them and Respond
	// answers one later.
	Hold bool
	// StuckRelay ignores vehicle control: the 0x0500 answer reports the
	// fuel line as it was.
	StuckRelay bool
	// AuthCode is the code the platform issued at registration.
	AuthCode string

	c      net.Conn
	wmu    sync.Mutex
	mu     sync.Mutex
	serial uint16
	acks   chan *jt808.Frame
	cmds   []Command
	cut    bool
	last   Pos
}

// Command is a platform command the terminal received.
type Command struct {
	ID     uint16
	Serial uint16
	Body   []byte
	// Text is the text of a 0x8300 or 0x8900 command.
	Text string
}

// Dial connects to the server.
func Dial(addr, phone string, v2019 bool) (*Tracker, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{Phone: phone, V2019: v2019, c: c, acks: make(chan *jt808.Frame, 32), Zone: time.FixedZone("UTC+8", 8*3600)}
	go t.read()
	return t, nil
}

// Close drops the connection.
func (t *Tracker) Close() error { return t.c.Close() }

// Received lists the commands received so far.
func (t *Tracker) Received() []Command {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Command(nil), t.cmds...)
}

// FuelCut reports the simulated fuel line state.
func (t *Tracker) FuelCut() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cut
}

func (t *Tracker) header(id uint16) jt808.Header {
	t.mu.Lock()
	t.serial++
	s := t.serial
	t.mu.Unlock()
	if t.V2019 {
		return jt808.Header{ID: id, Version: 1, Phone: jt808.PhoneBCD(t.Phone, 10), Serial: s}
	}
	return jt808.Header{ID: id, Version: -1, Phone: jt808.PhoneBCD(t.Phone, 6), Serial: s}
}

// Send writes a message with this ID and body; it returns its serial.
func (t *Tracker) Send(id uint16, body []byte) (uint16, error) {
	h := t.header(id)
	return h.Serial, t.SendRaw(jt808.Encode(h, body))
}

// SendRaw writes bytes as they are.
func (t *Tracker) SendRaw(b []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.c.Write(b)
	return err
}

// Ack waits for the platform's next answer (0x8001 or 0x8100).
func (t *Tracker) Ack(timeout time.Duration) (*jt808.Frame, error) {
	select {
	case f, ok := <-t.acks:
		if !ok {
			return nil, errors.New("connection closed")
		}
		return f, nil
	case <-time.After(timeout):
		return nil, errors.New("no answer from the platform")
	}
}

// sendAcked sends a message and checks the platform's 0x8001 to it.
func (t *Tracker) sendAcked(id uint16, body []byte) error {
	serial, err := t.Send(id, body)
	if err != nil {
		return err
	}
	// Answers to earlier messages (a text answer's 0x8001) are passed
	// over.
	var f *jt808.Frame
	for {
		f, err = t.Ack(3 * time.Second)
		if err != nil {
			return err
		}
		if f.ID == jt808.MPlatformResponse && len(f.Body) >= 5 && binary.BigEndian.Uint16(f.Body) == serial {
			break
		}
	}
	if binary.BigEndian.Uint16(f.Body[2:]) != id {
		return fmt.Errorf("0x8001 answers message 0x%04X, not 0x%04X", binary.BigEndian.Uint16(f.Body[2:]), id)
	}
	if f.Body[4] != 0 {
		return fmt.Errorf("refused with result %d", f.Body[4])
	}
	return nil
}

func pad(s string, n int) []byte {
	b := make([]byte, n)
	copy(b, s)
	return b
}

// Register sends 0x0100 and keeps the authentication code of the 0x8100
// answer; a result other than 0 is an error.
func (t *Tracker) Register() error {
	b := []byte{0x00, 0x1D, 0x01, 0x2C} // province 29, city 300
	if t.V2019 {
		b = append(b, pad("JIMI", 11)...)
		b = append(b, pad("VL103M", 30)...)
		b = append(b, pad("JM0000001", 30)...)
	} else {
		b = append(b, pad("JIMI", 5)...)
		b = append(b, pad("VL103M", 20)...)
		b = append(b, pad("JM00001", 7)...)
	}
	b = append(b, 0x01)
	b = append(b, "KA01F1234"...)
	serial, err := t.Send(jt808.MRegister, b)
	if err != nil {
		return err
	}
	var f *jt808.Frame
	for {
		f, err = t.Ack(3 * time.Second)
		if err != nil {
			return err
		}
		if f.ID == jt808.MRegisterAnswer && len(f.Body) >= 3 && binary.BigEndian.Uint16(f.Body) == serial {
			break
		}
	}
	if f.Body[2] != 0 {
		return fmt.Errorf("registration refused with result %d", f.Body[2])
	}
	t.AuthCode = string(f.Body[3:])
	return nil
}

// Authenticate sends 0x0102 with AuthCode and waits for the answer.
func (t *Tracker) Authenticate() error {
	b := []byte(t.AuthCode)
	if t.V2019 {
		b = append([]byte{byte(len(t.AuthCode))}, t.AuthCode...)
		b = append(b, pad("866496077582164", 15)...)
		b = append(b, pad("V1.2.3", 20)...)
	}
	return t.sendAcked(jt808.MAuth, b)
}

// Login registers and authenticates.
func (t *Tracker) Login() error {
	if err := t.Register(); err != nil {
		return err
	}
	return t.Authenticate()
}

// Heartbeat sends 0x0002 and waits for the answer.
func (t *Tracker) Heartbeat() error { return t.sendAcked(jt808.MHeartbeat, nil) }

// Logout sends 0x0003 and waits for the answer.
func (t *Tracker) Logout() error { return t.sendAcked(jt808.MLogout, nil) }

// Pos is a simulated location report.
type Pos struct {
	Time     time.Time
	Lat, Lon float64
	Alt      int
	Speed    float64 // km/h
	Course   int
	Sats     int
	Valid    bool
	ACC      bool
	// Alarm is the alarm flag word; Status bits are added to the ones
	// made from the fields above.
	Alarm, Status uint32
	// Mileage is the odometer in km (item 0x01), GSM the signal (0x30).
	Mileage float64
	GSM     int
}

// LocationBody encodes a location body (basic block and items 0x01, 0x30,
// 0x31) with the device clock in zone.
func LocationBody(p Pos, fuelCut bool, zone *time.Location) []byte {
	st := p.Status
	if p.ACC {
		st |= jt808.StACC
	}
	if p.Valid {
		st |= jt808.StPositioned
	}
	lat, lon := p.Lat, p.Lon
	if lat < 0 {
		st |= jt808.StSouth
		lat = -lat
	}
	if lon < 0 {
		st |= jt808.StWest
		lon = -lon
	}
	if fuelCut {
		st |= jt808.StFuelCut
	}
	b := binary.BigEndian.AppendUint32(nil, p.Alarm)
	b = binary.BigEndian.AppendUint32(b, st)
	b = binary.BigEndian.AppendUint32(b, uint32(lat*1e6+0.5))
	b = binary.BigEndian.AppendUint32(b, uint32(lon*1e6+0.5))
	b = binary.BigEndian.AppendUint16(b, uint16(int16(p.Alt)))
	b = binary.BigEndian.AppendUint16(b, uint16(p.Speed*10+0.5))
	b = binary.BigEndian.AppendUint16(b, uint16(p.Course))
	tm := p.Time
	if tm.IsZero() {
		tm = time.Now()
	}
	tm = tm.In(zone)
	for _, v := range []int{tm.Year() % 100, int(tm.Month()), tm.Day(), tm.Hour(), tm.Minute(), tm.Second()} {
		b = append(b, jt808.BCD(v))
	}
	b = append(b, 0x01, 4)
	b = binary.BigEndian.AppendUint32(b, uint32(p.Mileage*10+0.5))
	b = append(b, 0x30, 1, byte(p.GSM), 0x31, 1, byte(p.Sats))
	return b
}

// Location sends a 0x0200 report and waits for the answer.
func (t *Tracker) Location(p Pos) error {
	t.mu.Lock()
	t.last = p
	cut := t.cut
	t.mu.Unlock()
	return t.sendAcked(jt808.MLocation, LocationBody(p, cut, t.Zone))
}

// Batch sends a 0x0704 batch (blind: type 1, stored while offline) and
// waits for the answer.
func (t *Tracker) Batch(blind bool, ps ...Pos) error {
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
	return t.sendAcked(jt808.MBatch, b)
}

func (t *Tracker) read() {
	br := bufio.NewReader(t.c)
	for {
		f, err := jt808.ReadFrame(br)
		if err != nil {
			close(t.acks)
			return
		}
		switch f.ID {
		case jt808.MPlatformResponse, jt808.MRegisterAnswer:
			t.acks <- f
			continue
		}
		cmd := Command{ID: f.ID, Serial: f.Serial, Body: append([]byte(nil), f.Body...)}
		switch f.ID {
		case jt808.MText:
			n := 1
			if t.V2019 {
				n = 2
			}
			if len(f.Body) >= n {
				cmd.Text = string(f.Body[n:])
			}
		case jt808.MDownlinkPass:
			if len(f.Body) >= 1 {
				cmd.Text = string(f.Body[1:])
			}
		}
		t.mu.Lock()
		t.cmds = append(t.cmds, cmd)
		hold := t.Hold
		t.mu.Unlock()
		if hold {
			continue
		}
		var dt time.Duration
		if t.Delay != nil {
			dt = t.Delay(cmd.ID, cmd.Text)
		}
		if dt > 0 {
			go func() { time.Sleep(dt); _ = t.Respond(cmd) }()
			continue
		}
		_ = t.Respond(cmd)
	}
}

// Respond answers a command as the device does (see Hold).
func (t *Tracker) Respond(cmd Command) error {
	res := byte(0)
	if t.Result != nil {
		res = t.Result(cmd.ID, cmd.Text)
	}
	switch cmd.ID {
	case jt808.MVehicleControl:
		if res != 0 {
			return t.General(cmd.Serial, cmd.ID, res)
		}
		if len(cmd.Body) > 0 && !t.StuckRelay {
			t.mu.Lock()
			t.cut = cmd.Body[len(cmd.Body)-1] == 1
			t.mu.Unlock()
		}
		return t.positionAnswer(jt808.MControlAnswer, cmd.Serial)
	case jt808.MLocate:
		if res != 0 {
			return t.General(cmd.Serial, cmd.ID, res)
		}
		return t.positionAnswer(jt808.MLocateAnswer, cmd.Serial)
	case jt808.MText, jt808.MDownlinkPass:
		if err := t.General(cmd.Serial, cmd.ID, res); err != nil || res != 0 {
			return err
		}
		switch strings.ToUpper(cmd.Text) {
		case "RELAY,1#":
			t.mu.Lock()
			t.cut = true
			t.mu.Unlock()
		case "RELAY,0#":
			t.mu.Lock()
			t.cut = false
			t.mu.Unlock()
		}
		ans := "OK"
		if t.Answer != nil {
			ans = t.Answer(cmd.Text)
		}
		if ans == "" {
			return nil
		}
		return t.Text(cmd.ID == jt808.MDownlinkPass, ans)
	}
	return t.General(cmd.Serial, cmd.ID, res)
}

// General sends a 0x0001 general response.
func (t *Tracker) General(serial, id uint16, result byte) error {
	b := binary.BigEndian.AppendUint16(nil, serial)
	b = binary.BigEndian.AppendUint16(b, id)
	_, err := t.Send(jt808.MTerminalResponse, append(b, result))
	return err
}

// Text sends a text answer: 0x6006 (encoding byte 0, then the text) or,
// with pass, 0x0900 pass-through type 0xF0. The platform's 0x8001 is
// read by Ack.
func (t *Tracker) Text(pass bool, text string) error {
	if pass {
		_, err := t.Send(jt808.MUplinkPass, append([]byte{0xF0}, text...))
		return err
	}
	_, err := t.Send(jt808.MTextReport, append([]byte{0x00}, text...))
	return err
}

func (t *Tracker) positionAnswer(id, serial uint16) error {
	t.mu.Lock()
	p, cut := t.last, t.cut
	t.mu.Unlock()
	_, err := t.Send(id, append(binary.BigEndian.AppendUint16(nil, serial), LocationBody(p, cut, t.Zone)...))
	return err
}
