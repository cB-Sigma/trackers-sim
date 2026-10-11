// Package gt06sim is a GT06/Concox tracker for tests and demos: it logs in,
// sends location, status, alarm, cell (LBS) and WiFi frames, waits for the server's
// acknowledgements and answers 0x80 commands.
package gt06sim

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/cB-Sigma/trackers/gt06"
)

// Tracker is one simulated device.
type Tracker struct {
	IMEI string
	// Answer produces the reply text to a command; nil answers "OK".
	Answer func(cmd string) string
	// Delay, when set, holds the answer to a command this long (a slow
	// device, or one whose answer arrives after the server gave up).
	Delay func(cmd string) time.Duration
	// Hold keeps commands unanswered; Flags lists their server flags and
	// Reply answers one later, on this or another connection.
	Hold bool

	c      net.Conn
	serial uint16
	acks   chan *gt06.Frame
	mu     sync.Mutex
	cmds   []string
	flags  [][]byte
	wmu    sync.Mutex
}

// Dial connects to the server.
func Dial(addr, imei string) (*Tracker, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{IMEI: imei, c: c, acks: make(chan *gt06.Frame, 16)}
	go t.read()
	return t, nil
}

// Close drops the connection.
func (t *Tracker) Close() error { return t.c.Close() }

// Commands lists the commands received so far.
func (t *Tracker) Commands() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cmds...)
}

func (t *Tracker) read() {
	br := bufio.NewReader(t.c)
	for {
		f, err := gt06.ReadServerFrame(br)
		if err != nil {
			close(t.acks)
			return
		}
		if f.Protocol == gt06.PCommand && len(f.Content) >= 5 {
			flag := f.Content[1:5]
			n := int(f.Content[0]) - 4
			cmd := string(f.Content[5 : 5+n])
			t.mu.Lock()
			t.cmds = append(t.cmds, cmd)
			t.flags = append(t.flags, append([]byte(nil), flag...))
			hold := t.Hold
			t.mu.Unlock()
			if hold {
				continue
			}
			ans := "OK"
			if t.Answer != nil {
				ans = t.Answer(cmd)
			}
			var dt time.Duration
			if t.Delay != nil {
				dt = t.Delay(cmd)
			}
			if dt > 0 {
				go func(flag []byte, serial uint16) {
					time.Sleep(dt)
					_ = t.reply(flag, ans, serial)
				}(append([]byte(nil), flag...), f.Serial)
				continue
			}
			_ = t.reply(flag, ans, f.Serial)
			continue
		}
		t.acks <- f
	}
}

// AnswerFrame is a 0x15 command answer with the server flag of the command
// it answers.
func AnswerFrame(flag []byte, text string, serial uint16) []byte {
	c := append([]byte{byte(4 + len(text))}, flag...)
	c = append(c, text...)
	c = append(c, 0x00, 0x02)
	return gt06.Encode(gt06.PCmdAnswer, c, serial)
}

func (t *Tracker) reply(flag []byte, text string, serial uint16) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.c.Write(AnswerFrame(flag, text, serial))
	return err
}

// Flags lists the server flags of the commands received so far.
func (t *Tracker) Flags() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([][]byte(nil), t.flags...)
}

// Reply answers a command by its server flag (see Hold).
func (t *Tracker) Reply(flag []byte, text string) error {
	t.serial++
	return t.reply(flag, text, t.serial)
}

// Send writes a frame of this protocol number and content.
func (t *Tracker) Send(protocol byte, content []byte) error {
	t.serial++
	_, err := t.c.Write(gt06.Encode(protocol, content, t.serial))
	return err
}

// SendRaw writes bytes as they are.
func (t *Tracker) SendRaw(b []byte) error {
	_, err := t.c.Write(b)
	return err
}

// Ack waits for the next acknowledgement.
func (t *Tracker) Ack(timeout time.Duration) (*gt06.Frame, error) {
	select {
	case f, ok := <-t.acks:
		if !ok {
			return nil, errors.New("connection closed")
		}
		return f, nil
	case <-time.After(timeout):
		return nil, errors.New("no acknowledgement")
	}
}

// Login sends the login frame and waits for its acknowledgement.
func (t *Tracker) Login() error {
	imei := t.IMEI
	for len(imei) < 16 {
		imei = "0" + imei
	}
	b, err := hex.DecodeString(imei)
	if err != nil {
		return err
	}
	if err := t.Send(gt06.PLogin, b); err != nil {
		return err
	}
	f, err := t.Ack(3 * time.Second)
	if err != nil {
		return err
	}
	if f.Protocol != gt06.PLogin {
		return fmt.Errorf("unexpected answer 0x%02X", f.Protocol)
	}
	return nil
}

// Pos is a simulated position.
type Pos struct {
	Time     time.Time
	Lat, Lon float64
	Speed    int
	Course   int
	Sats     int
	Valid    bool
}

// GPS encodes date-time and the 12-byte GPS block.
func GPS(p Pos) []byte {
	t := p.Time.UTC()
	b := []byte{byte(t.Year() - 2000), byte(t.Month()), byte(t.Day()), byte(t.Hour()), byte(t.Minute()), byte(t.Second())}
	b = append(b, byte(0xC0|p.Sats&0x0F))
	lat, lon := p.Lat, p.Lon
	cs := uint16(p.Course & 0x03FF)
	if lat >= 0 {
		cs |= 0x0400
	} else {
		lat = -lat
	}
	if lon < 0 {
		cs |= 0x0800
		lon = -lon
	}
	if p.Valid {
		cs |= 0x1000
	}
	b = binary.BigEndian.AppendUint32(b, uint32(lat*1800000+0.5))
	b = binary.BigEndian.AppendUint32(b, uint32(lon*1800000+0.5))
	b = append(b, byte(p.Speed))
	return binary.BigEndian.AppendUint16(b, cs)
}

// Location sends a 0x22 location frame (acc on, real time).
func (t *Tracker) Location(p Pos, history bool) error {
	c := GPS(p)
	c = append(c, 0x01, 0x94, 0x00, 0x28, 0x7D, 0x00, 0x1F, 0x71) // MCC 404, MNC 0, LAC, cell
	re := byte(0)
	if history {
		re = 1
	}
	c = append(c, 0x01, 0x00, re)
	return t.Send(gt06.PLocation2, c)
}

// Status sends a heartbeat with the terminal information byte, voltage
// level and GSM level, and waits for its acknowledgement.
func (t *Tracker) Status(info, voltage, gsm byte) error {
	if err := t.Send(gt06.PStatus, []byte{info, voltage, gsm, 0x00, 0x02}); err != nil {
		return err
	}
	_, err := t.Ack(3 * time.Second)
	return err
}

// Alarm sends a 0x16 alarm frame and waits for its acknowledgement.
func (t *Tracker) Alarm(p Pos, alarm byte) error {
	c := GPS(p)
	c = append(c, 0x09, 0x01, 0x94, 0x00, 0x28, 0x7D, 0x00, 0x1F, 0x71)
	c = append(c, 0x46, 0x05, 0x04, alarm, 0x02)
	if err := t.Send(gt06.PAlarm, c); err != nil {
		return err
	}
	_, err := t.Ack(3 * time.Second)
	return err
}

// Cell is a simulated mobile cell: LAC, cell ID and signal in dBm.
type Cell struct {
	LAC  uint16
	CID  uint32
	RSSI int
}

// AP is a simulated WiFi access point: MAC (6 bytes) and signal in dBm.
type AP struct {
	MAC  [6]byte
	RSSI int
}

// LBS sends a multi-cell frame (gt06.PLBSMulti, PLBSMulti2, PLBSMulti3,
// PLBSExtend) or, with protocol gt06.PLBSWiFi, cells and access points,
// and waits for the acknowledgement. Up to seven cells; ta < 0 is unknown.
func (t *Tracker) LBS(protocol byte, at time.Time, mcc uint16, mnc byte, cells []Cell, ta int, aps []AP) error {
	u := at.UTC()
	c := []byte{byte(u.Year() - 2000), byte(u.Month()), byte(u.Day()), byte(u.Hour()), byte(u.Minute()), byte(u.Second())}
	c = binary.BigEndian.AppendUint16(c, mcc)
	c = append(c, mnc)
	for i := range 7 {
		if i < len(cells) {
			x := cells[i]
			c = binary.BigEndian.AppendUint16(c, x.LAC)
			c = append(c, byte(x.CID>>16), byte(x.CID>>8), byte(x.CID), byte(-x.RSSI))
		} else {
			c = append(c, 0, 0, 0, 0, 0, 0)
		}
	}
	if ta < 0 {
		ta = 0xFF
	}
	c = append(c, byte(ta))
	if protocol == gt06.PLBSWiFi {
		c = append(c, byte(len(aps)))
		for _, a := range aps {
			c = append(c, a.MAC[:]...)
			c = append(c, byte(-a.RSSI))
		}
	} else {
		c = append(c, 0x00, 0x02) // language
	}
	if err := t.Send(protocol, c); err != nil {
		return err
	}
	f, err := t.Ack(3 * time.Second)
	if err != nil {
		return err
	}
	if f.Protocol != protocol {
		return fmt.Errorf("unexpected answer 0x%02X", f.Protocol)
	}
	return nil
}

// WiFi sends a count-in-header WiFi frame (gt06.PWiFi2 or PWiFi) and
// waits for its acknowledgement, which must echo the frame's time. Cells
// carry a signal level (not dBm).
func (t *Tracker) WiFi(protocol byte, at time.Time, aps []AP, mcc uint16, mnc byte, cells []Cell) error {
	var ab [][7]byte
	for _, a := range aps {
		var x [7]byte
		copy(x[:], a.MAC[:])
		x[6] = byte(-a.RSSI)
		ab = append(ab, x)
	}
	var cb [][5]byte
	for _, c := range cells {
		cb = append(cb, [5]byte{byte(c.LAC >> 8), byte(c.LAC), byte(c.CID >> 8), byte(c.CID), byte(c.RSSI)})
	}
	frame := gt06.WiFiFrame(protocol, at.UTC(), ab, mcc, mnc, cb)
	if err := t.SendRaw(frame); err != nil {
		return err
	}
	f, err := t.Ack(3 * time.Second)
	if err != nil {
		return err
	}
	if f.Protocol != protocol || !f.CountHeader || string(f.Content) != string(frame[4:10]) {
		return fmt.Errorf("unexpected answer 0x%02X % X", f.Protocol, f.Content)
	}
	return nil
}
