// Package ais140sim is an AIS-140 vehicle location tracking device for
// tests and demos. It sends the packets of AIS-140 as amended by
// Amendment 3 (04/2026): the login (Table 4A(1)), data (Table 4A(2), with
// the RFID trailer ID of 3.1.1.28 when a trailer is coupled), health
// (Table 3B) and emergency (Table 4C i) packets, the emergency SMS (Table
// 4C ii) and the activation and health check answers (Amendment 2, 8.4),
// each with the XOR checksum of the characters between '$' and '*'. It
// executes Table 4D commands ("$IMEI,password,SET|GET|CLR,…") and answers
// each with a data packet of alert ID 12 carrying the OTA response, and
// answers the older text commands ("SET SL:60") with "$OTA,…".
package ais140sim

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Device is one simulated tracker.
type Device struct {
	IMEI, Vehicle, Vendor, Firmware string
	// ICCID is the SIM the login names.
	ICCID string
	// Password is the command password (Table 4D); "" accepts any.
	Password string
	// Trailer is the RFID tag of the coupled trailer ("" none); RFID
	// makes data packets carry the trailer ID field.
	Trailer string
	RFID    bool
	// Source is the command source the OTA responses name (default the
	// server's address as the device sees it).
	Source string
	// Answer, when set, replaces the answer to a command: it gets the
	// command as "OP key:value,key" (or the older text line) and returns
	// the packet to send ("" for none).
	Answer func(cmd string) string
	// Fail makes these command IDs fail (status 0).
	Fail map[string]bool
	// Delay, when set, holds the answer to a command this long (a slow
	// device, or one whose answer arrives after the server gave up).
	Delay func(cmd string) time.Duration
	// NewPort makes a UDP device send its answers from a new local port,
	// as a tracker behind carrier NAT does when its next datagram gets a
	// new mapping.
	NewPort bool

	c      net.Conn
	frame  int
	mu     sync.Mutex
	cmds   []string
	config map[string]string
	lines  chan string
}

func newDevice(c net.Conn, imei, vehicle string) *Device {
	d := &Device{IMEI: imei, Vehicle: vehicle, Vendor: "SIGSIM", Firmware: "01.01.01", ICCID: "89910000000000000012",
		c: c, lines: make(chan string, 16), config: map[string]string{}}
	go d.read()
	return d
}

// Dial connects over TCP.
func Dial(addr, imei, vehicle string) (*Device, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return newDevice(c, imei, vehicle), nil
}

// DialTLS connects over TLS with cfg.
func DialTLS(addr string, cfg *tls.Config, imei, vehicle string) (*Device, error) {
	c, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	return newDevice(c, imei, vehicle), nil
}

// DialUDP sends datagrams to addr from one local port and answers commands
// that arrive there.
func DialUDP(addr, imei, vehicle string) (*Device, error) {
	c, err := net.Dial("udp", addr)
	if err != nil {
		return nil, err
	}
	return newDevice(c, imei, vehicle), nil
}

// SetAnswer changes the answer to commands while the device runs.
func (d *Device) SetAnswer(f func(cmd string) string) {
	d.mu.Lock()
	d.Answer = f
	d.mu.Unlock()
}

// SetDelay holds answers to commands (see Delay).
func (d *Device) SetDelay(f func(cmd string) time.Duration) {
	d.mu.Lock()
	d.Delay = f
	d.mu.Unlock()
}

// SetNewPort makes a UDP device answer from a new local port (see NewPort).
func (d *Device) SetNewPort(on bool) {
	d.mu.Lock()
	d.NewPort = on
	d.mu.Unlock()
}

// SetTrailer couples a trailer (or none with "").
func (d *Device) SetTrailer(tag string) {
	d.mu.Lock()
	d.Trailer = tag
	d.mu.Unlock()
}

// SetRFID turns the trailer ID field of data packets on or off.
func (d *Device) SetRFID(on bool) {
	d.mu.Lock()
	d.RFID = on
	d.mu.Unlock()
}

// SetFail makes commands on these IDs fail (status 0).
func (d *Device) SetFail(ids ...string) {
	d.mu.Lock()
	d.Fail = map[string]bool{}
	for _, id := range ids {
		d.Fail[id] = true
	}
	d.mu.Unlock()
}

// Config is the device's configuration value of a command ID.
func (d *Device) Config(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.config[id]
}

// Close drops the connection.
func (d *Device) Close() error { return d.c.Close() }

// Checksum is the XOR of s's characters as two upper-case hex digits.
func Checksum(s string) string {
	var x byte
	for i := 0; i < len(s); i++ {
		x ^= s[i]
	}
	return fmt.Sprintf("%02X", x)
}

// frameOf ends a packet body (after '$') with '*' and its checksum.
func frameOf(body string) string { return "$" + body + "*" + Checksum(body) }

func (d *Device) read() {
	sc := bufio.NewScanner(d.c)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		up := strings.ToUpper(line)
		if strings.HasPrefix(up, "SET ") || strings.HasPrefix(up, "GET ") || strings.HasPrefix(up, "CLR ") {
			d.record(line)
			ans := fmt.Sprintf("$OTA,%s,%s,OK*", d.IMEI, line)
			if f := d.answerFunc(); f != nil {
				ans = f(line)
			}
			d.answer(line, ans)
			continue
		}
		if cmd, ok := d.standard(line); ok {
			d.record(cmd.text)
			ans := ""
			if f := d.answerFunc(); f != nil {
				ans = f(cmd.text)
			} else if cmd.valid {
				ans = d.execute(cmd)
			}
			d.answer(cmd.text, ans)
			continue
		}
		d.lines <- line
	}
	close(d.lines)
}

func (d *Device) answerFunc() func(string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Answer
}

func (d *Device) record(cmd string) {
	d.mu.Lock()
	d.cmds = append(d.cmds, cmd)
	d.mu.Unlock()
}

// answer sends the answer to cmd, after the device's Delay.
func (d *Device) answer(cmd, ans string) {
	d.mu.Lock()
	delay := d.Delay
	d.mu.Unlock()
	if delay != nil {
		if dt := delay(cmd); dt > 0 {
			go func() {
				time.Sleep(dt)
				d.write(ans)
			}()
			return
		}
	}
	d.write(ans)
}

func (d *Device) write(ans string) {
	if ans == "" {
		return
	}
	d.mu.Lock()
	newPort := d.NewPort
	d.mu.Unlock()
	if _, udp := d.c.(*net.UDPConn); udp && newPort {
		if c, err := net.Dial("udp", d.c.RemoteAddr().String()); err == nil {
			_, _ = c.Write([]byte(ans + "\r\n"))
			_ = c.Close()
			return
		}
	}
	_, _ = d.c.Write([]byte(ans + "\r\n"))
}

// command is a received Table 4D command.
type command struct {
	op    string
	items [][2]string // key, value
	text  string      // "OP key:value,key"
	valid bool        // IMEI, password and checksum right
}

func (d *Device) standard(line string) (command, bool) {
	body, sum, ok := strings.Cut(strings.TrimPrefix(line, "$"), "*")
	if !ok || !strings.HasPrefix(line, "$") {
		return command{}, false
	}
	f := strings.Split(body, ",")
	if len(f) < 4 {
		return command{}, false
	}
	op := strings.ToUpper(f[2])
	if op != "SET" && op != "GET" && op != "CLR" {
		return command{}, false
	}
	c := command{op: op}
	var parts []string
	for _, it := range f[3:] {
		k, v, _ := strings.Cut(it, ":")
		c.items = append(c.items, [2]string{k, v})
		parts = append(parts, it)
	}
	c.text = op + " " + strings.Join(parts, ",")
	d.mu.Lock()
	pw := d.Password
	d.mu.Unlock()
	c.valid = f[0] == d.IMEI && (pw == "" || f[1] == pw) && strings.EqualFold(sum, Checksum(body))
	return c, true
}

// execute applies a command and returns the alert 12 data packet.
func (d *Device) execute(c command) string {
	d.mu.Lock()
	var items []string
	for _, it := range c.items {
		id, v := it[0], it[1]
		ok := !d.Fail[id]
		switch c.op {
		case "SET":
			if ok {
				d.config[id] = v
			}
		case "GET":
			switch id {
			case "001":
				v = d.Firmware
			case "019":
				v = d.IMEI
			case "022":
				v = "24"
			case "023":
				v = d.Trailer
			default:
				v = d.config[id]
			}
		case "CLR":
			if ok {
				delete(d.config, id)
			}
			v = d.config[id]
		}
		st := "1"
		if !ok {
			st = "0"
		}
		items = append(items, id+":"+v+":"+st)
	}
	src := d.Source
	d.mu.Unlock()
	if src == "" {
		src = d.c.RemoteAddr().String()
	}
	return d.Normal(Fix{Time: time.Now().UTC(), Lat: 12.974512, Lon: 77.614563, Sats: 9, Valid: true, PacketType: "OA", AlertID: 12,
		OTA: "(" + src + "|" + c.op + "|" + strings.Join(items, "|") + ")"})
}

// Line waits for a line from the server (an acknowledgement).
func (d *Device) Line(timeout time.Duration) (string, bool) {
	select {
	case l, ok := <-d.lines:
		return l, ok
	case <-time.After(timeout):
		return "", false
	}
}

// Commands lists the commands received ("SET 008:60", or the text line).
func (d *Device) Commands() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.cmds...)
}

// Send writes one packet (a trailing CRLF is added).
func (d *Device) Send(pkt string) error {
	_, err := d.c.Write([]byte(pkt + "\r\n"))
	return err
}

// Fix is a simulated position report.
type Fix struct {
	Time            time.Time
	Lat, Lon        float64
	Speed, Heading  float64
	Sats            int
	Valid           bool
	Ignition, Power bool
	Emergency       bool
	BoxOpen         bool
	PacketType      string // NR, EA, TA, …
	AlertID         int
	History         bool
	MainV, BatteryV float64
	GSM             int
	// OTA is the OTA response field ("" is "()").
	OTA string
}

func b01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func hemi(lat, lon float64) (float64, string, float64, string) {
	ns, ew := "N", "E"
	if lat < 0 {
		lat, ns = -lat, "S"
	}
	if lon < 0 {
		lon, ew = -lon, "W"
	}
	return lat, ns, lon, ew
}

// Login is a login packet (Table 4A(1)).
func (d *Device) Login() string {
	return frameOf(fmt.Sprintf("LGN,%s,%s,%s,%s,0100,12.974512,N,77.614563,E", d.Vehicle, d.IMEI, d.ICCID, d.Firmware))
}

// Normal is a data packet (Table 4A(2)).
func (d *Device) Normal(f Fix) string {
	d.mu.Lock()
	d.frame++
	frame := d.frame
	rfid, trailer := d.RFID, d.Trailer
	d.mu.Unlock()
	pt := f.PacketType
	if pt == "" {
		pt = "NR"
	}
	id := f.AlertID
	if id == 0 {
		id = 1
	}
	st := "L"
	if f.History {
		st = "H"
	}
	lat, ns, lon, ew := hemi(f.Lat, f.Lon)
	tamper := "C"
	if f.BoxOpen {
		tamper = "O"
	}
	ota := f.OTA
	if ota == "" {
		ota = "()"
	}
	t := f.Time.UTC()
	body := fmt.Sprintf("PVT,%s,%s,%s,%02d,%s,%s,%s,%s,%s,%s,%.6f,%s,%.6f,%s,%.1f,%.0f,%d,%.1f,%.1f,%.1f,SIMNET,%s,%s,%.1f,%.1f,%s,%s,%d,404,45,0d33,cc17,"+
		"19,0d33,cc18,15,0d33,a7f4,12,0d33,a7f5,11,0d33,a7f6,0001,00,%06d,0.00,0.00,0.0,%s",
		d.Vendor, d.Firmware, pt, id, st, d.IMEI, d.Vehicle, b01(f.Valid), t.Format("02012006"), t.Format("150405"),
		lat, ns, lon, ew, f.Speed, f.Heading, f.Sats, 900.0, 1.1, 0.6, b01(f.Ignition), b01(f.Power), f.MainV, f.BatteryV,
		b01(f.Emergency), tamper, f.GSM, frame%1000000, ota)
	if rfid {
		body += "," + trailer
	}
	return frameOf(body)
}

// Health is a health packet (Table 3B).
func (d *Device) Health(battery int) string {
	return frameOf(fmt.Sprintf("HLM,%s,%s,%s,%d,20,15.00,10,60,00010000,0.00,0.00", d.Vendor, d.Firmware, d.IMEI, battery))
}

// Emergency is an emergency packet to the server (Table 4C i); on true
// EMR, else SEM.
func (d *Device) Emergency(on bool, t time.Time, lat, lon float64) string {
	typ := "SEM"
	if on {
		typ = "EMR"
	}
	la, ns, lo, ew := hemi(lat, lon)
	return frameOf(fmt.Sprintf("EPB,%s,%s,NM,%s,A,%.6f,%s,%.6f,%s,900.0,35.5,0.0,G,%s,+919999999999", typ, d.IMEI, t.UTC().Format("02012006150405"), la, ns, lo, ew, d.Vehicle))
}

// EmergencySMS is the emergency SMS text (Table 4C ii).
func (d *Device) EmergencySMS(t time.Time, lat, lon float64) string {
	la, ns, lo, ew := hemi(lat, lon)
	return frameOf(fmt.Sprintf("EPB,EMR,%s,%.6f,%s,%.6f,%s,A,35.5,cc17,0d33,%s", d.IMEI, la, ns, lo, ew, t.UTC().Format("02012006150405")))
}

// CheckReply is the answer to an activation (ACTV) or health check (HCHK)
// request with its code (Amendment 2, 8.4 Table-1), as sent by SMS.
func (d *Device) CheckReply(kind, code string, t time.Time, lat, lon float64) string {
	la, ns, lo, ew := hemi(lat, lon)
	return fmt.Sprintf("%sR,%s,%s,%s,%s,01,%.6f,%s,%.6f,%s,1,%s,263.19,25.4,23,404,10,d6d6,1,1,24.6,100000,ID",
		kind, code, d.Vendor, d.Firmware, d.IMEI, la, ns, lo, ew, t.UTC().Format("02012006 150405"))
}

// Backend is a simulated second (control centre) backend that devices'
// packets are forwarded to. It records the lines each connection sent.
type Backend struct {
	Addr string

	mu    sync.Mutex
	ln    net.Listener
	conns [][]string
	open  []net.Conn
}

// StartBackend listens on a free loopback port.
func StartBackend() (*Backend, error) {
	b := &Backend{Addr: "127.0.0.1:0"}
	if err := b.Start(); err != nil {
		return nil, err
	}
	return b, nil
}

// Start listens (again) on the backend's address.
func (b *Backend) Start() error {
	ln, err := net.Listen("tcp", b.Addr)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.ln, b.Addr = ln, ln.Addr().String()
	b.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			b.mu.Lock()
			i := len(b.conns)
			b.conns = append(b.conns, nil)
			b.open = append(b.open, c)
			b.mu.Unlock()
			go func() {
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					b.mu.Lock()
					b.conns[i] = append(b.conns[i], sc.Text())
					b.mu.Unlock()
				}
			}()
		}
	}()
	return nil
}

// Stop closes the listener and every connection (the backend goes away).
func (b *Backend) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ln != nil {
		_ = b.ln.Close()
		b.ln = nil
	}
	for _, c := range b.open {
		_ = c.Close()
	}
	b.open = nil
}

// Connections returns the lines received, per connection in accept order.
func (b *Backend) Connections() [][]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([][]string, len(b.conns))
	for i, c := range b.conns {
		out[i] = append([]string(nil), c...)
	}
	return out
}
