// Package meitracksim is a Meitrack tracker for tests and demos: it dials
// the server, identifies itself with its first report (the protocol has no
// login frame), sends AAA event reports and CCE batches, and answers "@@"
// commands with the same data identifier and command type ("C01,OK"; A10
// with an AAA report of event 34), as the Meitrack GPRS protocol documents
// show. Answers can be delayed or held.
package meitracksim

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cB-Sigma/trackers/meitrack"
)

// Pos is a simulated position.
type Pos struct {
	Time     time.Time
	Lat, Lon float64
	Speed    int // km/h
	Course   int // degrees
	Sats     int
	Alt      int // metres
	Valid    bool
}

// Command is a command the tracker received.
type Command struct {
	ID   byte   // data identifier
	Text string // "<type>,<params>", as framed
}

// Tracker is one simulated device.
type Tracker struct {
	IMEI string
	// Answer produces the answer text to a command ("C01,OK"); nil answers
	// "<type>,OK". It is not called for A10, answered with a report.
	Answer func(cmd string) string
	// Delay, when set, holds the answer to a command this long.
	Delay func(cmd string) time.Duration
	// Hold keeps commands unanswered; Reply answers one later.
	Hold bool
	// Last is the position reported in answers to A10.
	Last Pos
	// IO is the I/O port word of reports (bits 0-7 outputs, 8-15 inputs).
	IO uint16

	c      net.Conn
	closed chan struct{}
	mu     sync.Mutex
	cmds   []Command
	seq    int
	wmu    sync.Mutex
}

// Dial connects to the server.
func Dial(addr, imei string) (*Tracker, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{IMEI: imei, c: c, closed: make(chan struct{})}
	go t.read()
	return t, nil
}

// Close drops the connection.
func (t *Tracker) Close() error { return t.c.Close() }

// Commands lists the commands received so far.
func (t *Tracker) Commands() []Command {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Command(nil), t.cmds...)
}

// Closed reports whether the server closed the connection within d.
func (t *Tracker) Closed(d time.Duration) bool {
	select {
	case <-t.closed:
		return true
	case <-time.After(d):
		return false
	}
}

func (t *Tracker) read() {
	defer close(t.closed)
	br := bufio.NewReader(t.c)
	for {
		f, err := meitrack.ReadFrame(br)
		if errors.Is(err, meitrack.ErrLength) {
			continue
		}
		if err != nil {
			return
		}
		if !f.Server || !f.CRCOK || f.IMEI != t.IMEI {
			continue
		}
		text := f.Type
		if len(f.Data) > 0 {
			text += "," + string(f.Data)
		}
		t.mu.Lock()
		t.cmds = append(t.cmds, Command{ID: f.ID, Text: text})
		hold := t.Hold
		t.mu.Unlock()
		if hold || f.Type == "AFF" {
			continue
		}
		var dt time.Duration
		if t.Delay != nil {
			dt = t.Delay(text)
		}
		ans := t.answerText(text)
		if dt > 0 {
			go func(id byte) {
				time.Sleep(dt)
				_ = t.Reply(id, ans)
			}(f.ID)
			continue
		}
		_ = t.Reply(f.ID, ans)
	}
}

func (t *Tracker) answerText(cmd string) string {
	typ, _, _ := strings.Cut(cmd, ",")
	if typ == "A10" {
		t.mu.Lock()
		last := t.Last
		t.mu.Unlock()
		return "AAA," + AAA(34, last, t.IO)
	}
	if t.Answer != nil {
		return t.Answer(cmd)
	}
	return typ + ",OK"
}

// Reply sends an answer body ("C01,OK", or "AAA,34,…") with data
// identifier id, on this connection.
func (t *Tracker) Reply(id byte, body string) error {
	return t.write(meitrack.Encode(false, id, t.IMEI, []byte(body)))
}

func (t *Tracker) write(b []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.c.Write(b)
	return err
}

// SendRaw writes bytes as they are.
func (t *Tracker) SendRaw(b []byte) error { return t.write(b) }

// nextID is the data identifier of the tracker's next report (the device
// picks it; the documents' examples use letters).
func (t *Tracker) nextID() byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	return "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"[t.seq%52]
}

// AAA is the data of an AAA report after "AAA,": event code, position,
// time, status, satellites, GSM 22, speed, course, HDOP 1, altitude,
// mileage, run time, cell 404|45|1A2B|3C4D, the I/O word and analog
// values 0|0|0|0185|0546 (T333 hundredths: 3.89 V battery, 13.50 V
// external), as the documents lay out the fields.
func AAA(event int, p Pos, io uint16) string {
	st := "V"
	if p.Valid {
		st = "A"
	}
	ts := p.Time.UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	return fmt.Sprintf("%d,%.6f,%.6f,%s,%s,%d,22,%d,%d,1,%d,5120,3600,404|45|1A2B|3C4D,%04X,0|0|0|185|546,,,3,,",
		event, p.Lat, p.Lon, ts.Format("060102150405"), st, p.Sats, p.Speed, p.Course, p.Alt, io)
}

// Report sends an AAA report of this event.
func (t *Tracker) Report(event int, p Pos) error {
	t.mu.Lock()
	t.Last = p
	t.mu.Unlock()
	return t.write(meitrack.Encode(false, t.nextID(), t.IMEI, []byte("AAA,"+AAA(event, p, t.IO))))
}

// Hello sends a heartbeat report (event 31) and reports an error when the
// server closes the connection (an unlisted tracker) within a second.
func (t *Tracker) Hello() error {
	t.mu.Lock()
	last := t.Last
	t.mu.Unlock()
	if err := t.Report(31, last); err != nil {
		return err
	}
	if t.Closed(time.Second) {
		return errors.New("the server closed the connection")
	}
	return nil
}

// CCEPacket is one data packet of a CCE batch.
type CCEPacket struct {
	Event int
	Pos   Pos
	// Inputs and Outputs are the port bits; Battery and External are in
	// hundredths of a volt.
	Inputs, Outputs   byte
	Battery, External uint16
	MCC, MNC, LAC     uint16
	CID               uint32
	RSSI              int16
}

// CCE builds the binary data of a CCE batch (after "CCE,"), with the
// parameter IDs of the CCE ID protocol V1.0.
func CCE(remaining uint32, pkts []CCEPacket) []byte {
	le16 := binary.LittleEndian.AppendUint16
	le32 := binary.LittleEndian.AppendUint32
	b := le32(nil, remaining)
	b = le16(b, uint16(len(pkts)))
	for _, p := range pkts {
		valid := byte(0)
		if p.Pos.Valid {
			valid = 1
		}
		var d []byte
		d = append(d, 5, 0x05, valid, 0x06, byte(p.Pos.Sats), 0x07, 22, 0x14, p.Outputs, 0x15, p.Inputs)
		d = append(d, 7)
		for _, kv := range [][2]uint16{{0x08, uint16(p.Pos.Speed)}, {0x09, uint16(p.Pos.Course)}, {0x0A, 10},
			{0x0B, uint16(int16(p.Pos.Alt))}, {0x19, p.Battery}, {0x1A, p.External}, {0x40, uint16(p.Event)}} {
			d = append(d, byte(kv[0]))
			d = le16(d, kv[1])
		}
		secs := uint32(p.Pos.Time.UTC().Sub(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) / time.Second)
		d = append(d, 5)
		for _, kv := range [][2]uint32{{0x02, uint32(int32(p.Pos.Lat * 1e6))}, {0x03, uint32(int32(p.Pos.Lon * 1e6))},
			{0x04, secs}, {0x0C, 5120}, {0x0D, 3600}} {
			d = append(d, byte(kv[0]))
			d = le32(d, kv[1])
		}
		d = append(d, 1, 0x0E, 12)
		d = le16(d, p.MCC)
		d = le16(d, p.MNC)
		d = le16(d, p.LAC)
		d = le32(d, p.CID)
		d = le16(d, uint16(p.RSSI))
		pkt := le16(nil, uint16(5+7+5+1)) // number of IDs
		pkt = append(pkt, d...)
		b = le16(b, uint16(len(pkt)))
		b = append(b, pkt...)
	}
	return b
}

// Batch sends a CCE batch.
func (t *Tracker) Batch(remaining uint32, pkts []CCEPacket) error {
	return t.write(meitrack.Encode(false, t.nextID(), t.IMEI, append([]byte("CCE,"), CCE(remaining, pkts)...)))
}

// String shows a command with its data identifier.
func (c Command) String() string { return strconv.QuoteRune(rune(c.ID)) + " " + c.Text }
