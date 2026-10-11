// Package queclinksim is a Queclink @Track (ASCII) tracker for tests and
// demos, behaving like a GV300-family device in TCP long-connection mode:
// it sends heartbeats (+ACK:GTHBD) and waits for the server's
// +SACK:GTHBD, sends position and event reports (+RESP, and +BUFF for
// stored ones) and acknowledges the AT+GT commands it accepts with
// +ACK:GTxxx carrying the command's serial number. Like the device, it
// does not acknowledge a command with a wrong password, and it answers a
// real-time locate (AT+GTRTO sub-command 1) with +RESP:GTRTL after the
// acknowledgement. Frame layouts follow the examples of Queclink's GV300N
// @Track Air Interface Protocol 3.01.
//
// With Hex set the tracker sends its heartbeats, reports and
// acknowledgements in the HEX (binary) format (<Protocol Format> 1) with
// the masks HexReportMask, HexAckMask and HexHeartbeatMask; commands and
// the server's +SACK answers stay ASCII, as on the device.
package queclinksim

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Version is the protocol version field the simulator sends (the GV300N
// document's examples).
const Version = "250301"

// Pos is one position.
type Pos struct {
	Time     time.Time
	Lat, Lon float64
	Speed    float64 // km/h
	Heading  int
	Alt      float64
	HDOP     int // 0: no fix
	Mileage  float64
}

// Tracker is one simulated device.
type Tracker struct {
	IMEI     string
	Password string
	// Delay, when set, holds the acknowledgement of a command this long.
	Delay func(cmd string) time.Duration
	// Hold keeps commands unacknowledged; Serials lists them and Ack
	// acknowledges one later.
	Hold bool
	// Last is the position a locate is answered with.
	Last Pos
	// Hex sends heartbeats, reports and acknowledgements in the HEX
	// format instead of ASCII.
	Hex bool

	c     net.Conn
	sacks chan string
	mu    sync.Mutex
	cmds  []string
	count uint16
	out   [3]bool
	every int
	wmu   sync.Mutex
}

// Dial connects to the server.
func Dial(addr, imei, password string) (*Tracker, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{IMEI: imei, Password: password, c: c, sacks: make(chan string, 64),
		Last: Pos{Time: time.Now().UTC(), Lat: 12.9716, Lon: 77.5946, HDOP: 1}}
	go t.read()
	return t, nil
}

// Close drops the connection.
func (t *Tracker) Close() error { return t.c.Close() }

// Commands lists the commands received so far, as received.
func (t *Tracker) Commands() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cmds...)
}

// Output reports digital output n's state (1 to 3).
func (t *Tracker) Output(n int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.out[n-1]
}

// Interval is the fixed report interval last set by AT+GTFRI.
func (t *Tracker) Interval() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.every
}

func (t *Tracker) read() {
	br := bufio.NewReader(t.c)
	for {
		line, err := br.ReadString('$')
		if err != nil {
			close(t.sacks)
			return
		}
		line = strings.TrimLeft(line, "\r\n ")
		switch {
		case strings.HasPrefix(line, "+SACK:"):
			select {
			case t.sacks <- line:
			default:
			}
		case strings.HasPrefix(line, "AT+GT"):
			t.command(line)
		}
	}
}

// command carries out an AT+GTxxx command: password check, then the
// acknowledgement.
func (t *Tracker) command(line string) {
	t.mu.Lock()
	t.cmds = append(t.cmds, line)
	hold := t.Hold
	t.mu.Unlock()
	name := line[3:8]
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return
	}
	f := strings.Split(strings.TrimSuffix(line[eq+1:], "$"), ",")
	if len(f) < 2 || f[0] != t.Password {
		return // the device ignores a command with a wrong password
	}
	serial := f[len(f)-1]
	sub := ""
	switch name {
	case "GTOUT":
		t.mu.Lock()
		t.out[0] = f[1] == "1"
		if len(f) > 4 {
			t.out[1] = f[4] == "1"
		}
		if len(f) > 7 {
			t.out[2] = f[7] == "1"
		}
		t.mu.Unlock()
	case "GTFRI":
		if len(f) > 8 {
			var s int
			if _, err := fmt.Sscanf(f[8], "%d", &s); err == nil {
				t.mu.Lock()
				t.every = s
				t.mu.Unlock()
			}
		}
	case "GTRTO":
		sub = map[string]string{"0": "GPS", "1": "RTL", "2": "READ", "3": "REBOOT", "4": "RESET", "5": "PWROFF",
			"6": "CID", "7": "CSQ", "8": "VER", "9": "BAT", "A": "IOS"}[f[1]]
	}
	if hold {
		return
	}
	var dt time.Duration
	if t.Delay != nil {
		dt = t.Delay(line)
	}
	send := func() {
		_ = t.Ack(name, sub, serial)
		if name == "GTRTO" && sub == "RTL" {
			_ = t.Report("GTRTL", "00", t.Last, false)
		}
	}
	if dt > 0 {
		go func() { time.Sleep(dt); send() }()
		return
	}
	send()
}

func (t *Tracker) next() string { return fmt.Sprintf("%04X", t.nextN()) }

func (t *Tracker) nextN() uint16 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.count++
	return t.count
}

func now() string { return time.Now().UTC().Format("20060102150405") }

// Send writes a frame as it is.
func (t *Tracker) Send(frame string) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.c.Write([]byte(frame))
	return err
}

// Ack acknowledges a command by its name (GTOUT), sub-command string (for
// GTRTO, "" otherwise) and serial number.
func (t *Tracker) Ack(name, sub, serial string) error {
	if t.Hex {
		return t.hexAck(name, sub, serial)
	}
	f := []string{"+ACK:" + name, Version, t.IMEI, ""}
	if sub != "" {
		f = append(f, sub)
	}
	f = append(f, serial, now(), t.next())
	return t.Send(strings.Join(f, ",") + "$")
}

// Heartbeat sends +ACK:GTHBD and waits for the server's +SACK:GTHBD with
// the same count number; it fails when the server closes the connection
// (an unlisted or blocked device) or does not answer.
func (t *Tracker) Heartbeat(timeout time.Duration) error {
	n := t.nextN()
	cnt := fmt.Sprintf("%04X", n)
	var err error
	if t.Hex {
		err = t.hexHeartbeat(n)
	} else {
		err = t.Send("+ACK:GTHBD," + Version + "," + t.IMEI + ",," + now() + "," + cnt + "$")
	}
	if err != nil {
		return err
	}
	tm := time.NewTimer(timeout)
	defer tm.Stop()
	for {
		select {
		case s, ok := <-t.sacks:
			if !ok {
				return errors.New("the server closed the connection")
			}
			if strings.HasPrefix(s, "+SACK:GTHBD,") && strings.HasSuffix(s, ","+cnt+"$") {
				return nil
			}
		case <-tm.C:
			return errors.New("no +SACK:GTHBD from the server")
		}
	}
}

// Sack waits for a server acknowledgement +SACK:<count>$ of any count.
func (t *Tracker) Sack(timeout time.Duration) (string, error) {
	select {
	case s, ok := <-t.sacks:
		if !ok {
			return "", errors.New("the server closed the connection")
		}
		return s, nil
	case <-time.After(timeout):
		return "", errors.New("no +SACK from the server")
	}
}

func block(p Pos) string {
	return fmt.Sprintf("%d,%.1f,%d,%.1f,%.6f,%.6f,%s,0404,0045,1A2B,00C3D4,00",
		p.HDOP, p.Speed, p.Heading, p.Alt, p.Lon, p.Lat, p.Time.UTC().Format("20060102150405"))
}

// Report sends a position report of the common layout (GTSOS, GTSPD,
// GTRTL, GTGEO, GTHBM, GTTOW, GTIGL…): reserved, report ID/type rt, one
// position, mileage. history sends it as +BUFF.
func (t *Tracker) Report(name, rt string, p Pos, history bool) error {
	if t.Hex {
		extra := []byte{hexByte(rt)}
		if name == "GTSOS" {
			extra = append(extra, 0x55) // microphone and speaker volume
		}
		return t.hexReport(false, history, hexRSPTypes[name], hexIO{battery: 90, mv: 12400, in: 1, motion: 0x22}, extra, []Pos{p})
	}
	head := "+RESP:"
	if history {
		head = "+BUFF:"
	}
	return t.Send(fmt.Sprintf("%s%s,%s,%s,,,%s,1,%s,%.1f,%s,%s$", head, name, Version, t.IMEI, rt, block(p), p.Mileage, now(), t.next()))
}

// Fixed sends a +RESP:GTFRI of the GV300N layout with its positions,
// external voltage (mV), battery percentage and device status (6 hex
// digits: motion state, inputs, outputs).
func (t *Tracker) Fixed(ps []Pos, mv, battery int, status string) error {
	if t.Hex {
		motion, in, out := hexStatus(status)
		return t.hexReport(false, false, hexRSPTypes["GTFRI"], hexIO{battery: battery, mv: mv, in: in, out: out, motion: motion}, []byte{0x10}, ps)
	}
	var b []string
	for _, p := range ps {
		b = append(b, block(p))
	}
	last := ps[len(ps)-1]
	return t.Send(fmt.Sprintf("+RESP:GTFRI,%s,%s,,%d,10,%d,%s,%.1f,,,,%d,%s,,,,%s,%s$",
		Version, t.IMEI, mv, len(ps), strings.Join(b, ","), last.Mileage, battery, status, now(), t.next()))
}

// Event sends a report whose value field precedes the position (GTIGN,
// GTIGF: duration; GTSTT: state; GTBPL: battery volts) and which has no
// report ID or number (GTMPN, GTMPF: value "").
func (t *Tracker) Event(name, value string, p Pos) error {
	if t.Hex {
		return t.hexEvent(name, value, p)
	}
	if value == "" {
		return t.Send(fmt.Sprintf("+RESP:%s,%s,%s,,%s,%s,%s$", name, Version, t.IMEI, block(p), now(), t.next()))
	}
	return t.Send(fmt.Sprintf("+RESP:%s,%s,%s,,%s,%s,%s,%s$", name, Version, t.IMEI, value, block(p), now(), t.next()))
}

// hexEvent sends an +EVT: the value is the ignition duration in seconds
// (GTIGN, GTIGF), the motion state (GTSTT) or the backup battery in volts
// (GTBPL).
func (t *Tracker) hexEvent(name, value string, p Pos) error {
	io := hexIO{battery: 90, mv: 12400, in: 1, motion: 0x21}
	var extra []byte
	switch name {
	case "GTIGN", "GTIGF":
		var s uint32
		_, _ = fmt.Sscanf(value, "%d", &s)
		extra = []byte{byte(s >> 24), byte(s >> 16), byte(s >> 8), byte(s)}
		if name == "GTIGF" {
			io.in, io.motion = 0, 0x11
		}
	case "GTSTT":
		io.motion = hexByte(value)
	case "GTBPL":
		var v float64
		_, _ = fmt.Sscanf(value, "%g", &v)
		mv := int(v*1000 + 0.5)
		extra = []byte{byte(mv >> 8), byte(mv)}
	}
	return t.hexReport(true, false, hexEVTTypes[name], io, extra, []Pos{p})
}
