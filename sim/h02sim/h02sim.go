// Package h02sim is an H02 tracker (Sinotrack ST-901 class) for tests and
// demos. Like the real device it has no login exchange: it identifies
// itself with its id in every frame, sends V1 and VP1 positions, VP1 and V3
// cell reports, ALRM alarms, BC stored positions (history), HTBT
// heartbeats (and waits for the echo), binary '$' records of 32 and 45
// bytes, Cantrack Y-model records and NBR cell reports, and
// answers commands with V4 in the documents' form: the command word, a
// result, the command's time when the command carried one, the answer time
// and its position. A CR (locate) is answered and followed by a position.
// Over UDP (DialUDP) it sends each frame as a datagram and reads commands
// from the same socket.
package h02sim

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/cB-Sigma/trackers/h02"
)

// Pos is one position.
type Pos struct {
	Time     time.Time
	Lat, Lon float64
	Speed    float64 // knots
	Course   int
	Valid    bool
	// Status is the status word; 0 means FFFFFBFF (nothing active, ACC off).
	Status uint32
}

// StatusIdle is the status word of a parked vehicle: nothing active, ACC off
// (bit 10 is 0, active-low).
const StatusIdle uint32 = 0xFFFFFBFF

// Tracker is one simulated device.
type Tracker struct {
	ID string
	// Result produces the result field of the answer to a command word
	// ("80", "DONE", "ERROR"…); nil answers "80" to numbered commands and
	// leaves the result out for the others, as the V1.0.5 document shows.
	Result func(cmd string) string
	// NoEcho leaves the command's time out of answers (firmware that does
	// not echo it).
	NoEcho bool
	// Delay holds the answer to a command word this long.
	Delay func(cmd string) time.Duration
	// Hold keeps commands unanswered; Commands lists them and Answer
	// answers one later.
	Hold bool
	// Last is the position answers and locate replies carry.
	Last Pos

	c      net.Conn
	udp    bool
	wmu    sync.Mutex
	mu     sync.Mutex
	lmu    sync.Mutex // Last, between the reader and the senders
	cmds   []string
	echoes chan string
}

// Dial connects to the server over TCP.
func Dial(addr, id string) (*Tracker, error) { return dial("tcp", addr, id) }

// DialUDP sends to the server over UDP.
func DialUDP(addr, id string) (*Tracker, error) { return dial("udp", addr, id) }

func dial(network, addr, id string) (*Tracker, error) {
	c, err := net.DialTimeout(network, addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{ID: id, c: c, udp: network == "udp", echoes: make(chan string, 16),
		Last: Pos{Lat: 12.9716, Lon: 77.5946, Valid: true, Status: StatusIdle}}
	go t.read()
	return t, nil
}

// Close drops the connection.
func (t *Tracker) Close() error { return t.c.Close() }

// Commands lists the frames received so far (without '*' and '#').
func (t *Tracker) Commands() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cmds...)
}

func (t *Tracker) read() {
	defer close(t.echoes)
	var frames func() (string, error)
	if t.udp {
		buf := make([]byte, 2048)
		var pending []string
		frames = func() (string, error) {
			for len(pending) == 0 {
				n, err := t.c.Read(buf)
				if err != nil {
					return "", err
				}
				br := bufio.NewReader(strings.NewReader(string(buf[:n])))
				for {
					f, err := h02.ReadFrame(br)
					if err != nil {
						break
					}
					pending = append(pending, f)
				}
			}
			f := pending[0]
			pending = pending[1:]
			return f, nil
		}
	} else {
		br := bufio.NewReader(t.c)
		frames = func() (string, error) { return h02.ReadFrame(br) }
	}
	for {
		f, err := frames()
		if err != nil {
			return
		}
		p := strings.Split(f, ",")
		if len(p) < 3 {
			continue
		}
		switch strings.ToUpper(p[2]) {
		case "HTBT", "V0", "V4", "R12":
			select {
			case t.echoes <- f:
			default:
			}
			continue
		}
		t.mu.Lock()
		t.cmds = append(t.cmds, f)
		hold := t.Hold
		t.mu.Unlock()
		if hold {
			continue
		}
		var dt time.Duration
		if t.Delay != nil {
			dt = t.Delay(p[2])
		}
		if dt > 0 {
			go func(f string) {
				time.Sleep(dt)
				_ = t.Answer(f)
			}(f)
			continue
		}
		_ = t.Answer(f)
	}
}

func (t *Tracker) write(s string) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.c.Write([]byte(s))
	return err
}

func dm(v float64, degDigits int) string {
	v = math.Abs(v)
	d := math.Floor(v)
	m := (v - d) * 60
	return fmt.Sprintf("%0*d%07.4f", degDigits, int(d), m)
}

func hemi(v float64, pos, neg string) string {
	if v < 0 {
		return neg
	}
	return pos
}

func word(p Pos) uint32 {
	if p.Status == 0 {
		return StatusIdle
	}
	return p.Status
}

// fields is the position part shared by V1 and V4:
// hhmmss,A|V,lat,N|S,lon,E|W,knots,course,ddmmyy,status.
func fields(p Pos) string {
	tm := p.Time
	if tm.IsZero() {
		tm = time.Now().UTC()
	}
	v := "V"
	if p.Valid {
		v = "A"
	}
	return fmt.Sprintf("%s,%s,%s,%s,%s,%s,%.2f,%03d,%s,%08X", tm.UTC().Format("150405"), v,
		dm(p.Lat, 2), hemi(p.Lat, "N", "S"), dm(p.Lon, 3), hemi(p.Lon, "E", "W"), p.Speed, p.Course, tm.UTC().Format("020106"), word(p))
}

func (t *Tracker) last() Pos {
	t.lmu.Lock()
	defer t.lmu.Unlock()
	return t.Last
}

func (t *Tracker) setLast(p Pos) {
	t.lmu.Lock()
	t.Last = p
	t.lmu.Unlock()
}

// V1Frame is a V1 position frame.
func V1Frame(id string, p Pos) string { return "*HQ," + id + ",V1," + fields(p) + "#" }

// Position sends a V1 position and remembers it as the last one.
func (t *Tracker) Position(p Pos) error {
	t.setLast(p)
	return t.write(V1Frame(t.ID, p))
}

// Send writes a raw frame.
func (t *Tracker) Send(s string) error { return t.write(s) }

// Heartbeat sends HTBT and waits for the server's echo.
func (t *Tracker) Heartbeat(timeout time.Duration) error {
	if err := t.write("*HQ," + t.ID + ",HTBT#"); err != nil {
		return err
	}
	return t.wait("HTBT", timeout)
}

// Echo waits for a server frame of this type (HTBT, V4, R12).
func (t *Tracker) wait(typ string, timeout time.Duration) error {
	tm := time.NewTimer(timeout)
	defer tm.Stop()
	for {
		select {
		case f, ok := <-t.echoes:
			if !ok {
				return errors.New("connection closed")
			}
			if p := strings.Split(f, ","); len(p) > 2 && strings.EqualFold(p[2], typ) {
				return nil
			}
		case <-tm.C:
			return fmt.Errorf("no %s from the server within %s", typ, timeout)
		}
	}
}

// Echo waits for a server frame of type typ (HTBT, V4 or R12).
func (t *Tracker) Echo(typ string, timeout time.Duration) error { return t.wait(typ, timeout) }

func bcdByte(s string) byte { return (s[0]-'0')<<4 | (s[1] - '0') }

// BinaryFrame is a 32-byte '$' record of a 10-digit id (nil for another
// id: the record has room for 10 digits only).
func BinaryFrame(id string, p Pos) []byte {
	if len(id) != 10 || strings.Trim(id, "0123456789") != "" {
		return nil
	}
	tm := p.Time.UTC()
	digits := id + tm.Format("150405") + tm.Format("020106")
	lat := strings.Replace(dm(p.Lat, 2), ".", "", 1)
	lon := strings.Replace(dm(p.Lon, 3), ".", "", 1)
	var flags byte
	if p.Valid {
		flags |= 0x02
	}
	if p.Lat >= 0 {
		flags |= 0x04
	}
	if p.Lon >= 0 {
		flags |= 0x08
	}
	b := []byte{'$'}
	for i := 0; i < len(digits); i += 2 {
		b = append(b, bcdByte(digits[i:i+2]))
	}
	for i := 0; i < 8; i += 2 {
		b = append(b, bcdByte(lat[i:i+2]))
	}
	b = append(b, 0x00) // battery / reserved
	for i := 0; i < 8; i += 2 {
		b = append(b, bcdByte(lon[i:i+2]))
	}
	b = append(b, (lon[8]-'0')<<4|flags)
	sc := fmt.Sprintf("%03d%03d", int(math.Round(p.Speed)), p.Course)
	for i := 0; i < 6; i += 2 {
		b = append(b, bcdByte(sc[i:i+2]))
	}
	w := word(p)
	b = append(b, byte(w>>24), byte(w>>16), byte(w>>8), byte(w))
	b = append(b, 0xFF, 0x00, 0x01) // user alarm flag, reserved, record number
	return b
}

// Binary sends a binary record.
func (t *Tracker) Binary(p Pos) error {
	b := BinaryFrame(t.ID, p)
	if b == nil {
		return errors.New("binary records carry a 10-digit device id; " + t.ID + " is not one")
	}
	t.setLast(p)
	return t.write(string(b))
}

// Cells sends an NBR report of serving and neighbour cells (lac, cid
// pairs) and waits for the server's answer.
func (t *Tracker) Cells(mcc, mnc int, cells [][2]int, timeout time.Duration) error {
	now := time.Now().UTC()
	s := fmt.Sprintf("*HQ,%s,NBR,%s,%d,%d,0,%d", t.ID, now.Format("150405"), mcc, mnc, len(cells))
	for _, c := range cells {
		s += fmt.Sprintf(",%d,%d,30", c[0], c[1])
	}
	s += fmt.Sprintf(",%s,%08X#", now.Format("020106"), word(t.last()))
	if err := t.write(s); err != nil {
		return err
	}
	return t.wait("V4", timeout)
}

// Answer answers a command frame (without '*' and '#') as the device does.
func (t *Tracker) Answer(frame string) error {
	p := strings.Split(frame, ",")
	if len(p) < 3 {
		return errors.New("not a command frame")
	}
	cmd := p[2]
	ctime := ""
	if len(p) > 3 && len(p[3]) == 6 && strings.Trim(p[3], "0123456789") == "" {
		ctime = p[3]
	}
	res := ""
	if t.Result != nil {
		res = t.Result(cmd)
	} else if ctime != "" {
		res = "80"
	}
	pos := t.last()
	pos.Time = time.Now().UTC()
	if strings.EqualFold(cmd, "S20") && len(p) > 5 && res >= "80" {
		if p[5] == "1" {
			pos.Status = word(pos) &^ (1 << 27) // fuel cut, active-low
		} else {
			pos.Status = word(pos) | 1<<27
		}
		t.lmu.Lock()
		t.Last.Status = pos.Status
		t.lmu.Unlock()
	}
	ans := "*HQ," + t.ID + ",V4," + cmd
	switch {
	case ctime != "":
		if res != "" {
			ans += "," + res
		}
		if !t.NoEcho {
			ans += "," + ctime
		}
		ans += "," + fields(pos)
	case res != "":
		ans += "," + res
	}
	ans += "#"
	if err := t.write(ans); err != nil {
		return err
	}
	if strings.EqualFold(cmd, "CR") {
		return t.Position(pos)
	}
	return nil
}

// VP1Frame is a short VP1 position frame (no time of day: the server
// takes it as now).
func VP1Frame(id string, p Pos) string {
	v := "V"
	if p.Valid {
		v = "A"
	}
	tm := p.Time
	if tm.IsZero() {
		tm = time.Now().UTC()
	}
	return fmt.Sprintf("*HQ,%s,VP1,%s,%s,%s,%s,%s,%.2f,%03d,%s,%08X#", id, v, dm(p.Lat, 2), hemi(p.Lat, "N", "S"),
		dm(p.Lon, 3), hemi(p.Lon, "E", "W"), p.Speed, p.Course, tm.UTC().Format("020106"), word(p))
}

// VP1 sends a short VP1 position and remembers it as the last one.
func (t *Tracker) VP1(p Pos) error {
	if !p.Valid {
		return errors.New("a VP1 position is valid (A); an invalid one is sent as cells (VP1Cells)")
	}
	t.setLast(p)
	return t.write(VP1Frame(t.ID, p))
}

// Cell is a mobile cell: location area, cell ID and a signal (a level for
// VP1, dBm for V3).
type Cell struct {
	LAC, CID, Signal int
}

// VP1Cells sends the VP1 form without a fix: the cells separated by 'Y'.
func (t *Tracker) VP1Cells(mcc, mnc int, cells []Cell) error {
	s := fmt.Sprintf("*HQ,%s,VP1,V,%d,%d", t.ID, mcc, mnc)
	for i, c := range cells {
		if i > 0 {
			s += "Y"
		} else {
			s += ","
		}
		s += fmt.Sprintf("%d,%d,%d", c.LAC, c.CID, c.Signal)
	}
	return t.write(s + "Y#")
}

// V3 sends a Cantrack V3 cell report: MCC and MNC, the cells with their
// signal in dBm, and the battery voltage (up to 5.6 V).
func (t *Tracker) V3(mcc, mnc int, cells []Cell, battery float64) error {
	now := time.Now().UTC()
	s := fmt.Sprintf("*HQ,%s,V3,%s,%03d%02d,%02d", t.ID, now.Format("150405"), mcc, mnc, len(cells))
	for _, c := range cells {
		s += fmt.Sprintf(",%06d,%06d,,%d", c.LAC, c.CID, c.Signal)
	}
	s += fmt.Sprintf(",%04X,0,X,%s,%08X#", int(battery/5.6*1024+0.5), now.Format("020106"), word(t.last()))
	return t.write(s)
}

// Alarm types of ALRM.
const (
	AlarmSOS        = 1
	AlarmLowBattery = 2
	AlarmGeofence   = 3
)

// Alarm sends an ALRM frame of this type with a position.
func (t *Tracker) Alarm(kind int, p Pos) error {
	t.setLast(p)
	return t.write(fmt.Sprintf("*HQ,%s,ALRM,%d,%s#", t.ID, kind, fields(p)))
}

// Blind sends positions stored while out of coverage as one BC frame
// (each stored position names only its day of the month).
func (t *Tracker) Blind(ps []Pos) error {
	if len(ps) == 0 {
		return errors.New("no stored positions")
	}
	var segs []string
	for _, p := range ps {
		v := "V"
		if p.Valid {
			v = "A"
		}
		tm := p.Time.UTC()
		segs = append(segs, fmt.Sprintf("%s,%s,%s,%s,%s,%.2f,%03d,%s,%08X,194,0,25FC,F48", v, dm(p.Lat, 2), hemi(p.Lat, "N", "S"),
			dm(p.Lon, 3), hemi(p.Lon, "E", "W"), p.Speed, p.Course, tm.Format("02150405"), word(p)))
	}
	body := strings.Join(segs, ";")
	return t.write(fmt.Sprintf("*HQ,%s,BC,%s,%d,%s#", t.ID, time.Now().UTC().Format("150405"), len(body), body))
}

// LongBinaryFrame is the 45-byte form of BinaryFrame: the 32-byte record
// and 13 more bytes, which no public document describes (sent as zeros).
func LongBinaryFrame(id string, p Pos) []byte {
	b := BinaryFrame(id, p)
	if b == nil {
		return nil
	}
	return append(b, make([]byte, 13)...)
}

// LongBinary sends a 45-byte binary record.
func (t *Tracker) LongBinary(p Pos) error {
	b := LongBinaryFrame(t.ID, p)
	if b == nil {
		return errors.New("binary records carry a 10-digit device id; " + t.ID + " is not one")
	}
	t.setLast(p)
	return t.write(string(b))
}

// YFrame is a Cantrack Y-model record: two analog inputs (fuel level is
// the tank capacity times analog1/analog2) and the position; it carries no
// device id.
func YFrame(analog1, analog2 uint16, record byte, p Pos) []byte {
	b := BinaryFrame("0000000000", p)
	b[0] = 'Y'
	b[1], b[2], b[3], b[4], b[5] = byte(analog1>>8), byte(analog1), byte(analog2>>8), byte(analog2), 0x00
	b[31] = record
	return b
}

// Y sends a Y-model record; the server knows the device from the frames
// before it (send a position or heartbeat first).
func (t *Tracker) Y(analog1, analog2 uint16, record byte, p Pos) error {
	t.setLast(p)
	return t.write(string(YFrame(analog1, analog2, record, p)))
}
