// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package wialonsim is a Wialon IPS tracker (version 2.0, or 1.1) for
// tests and demos: it logs in (#L#, answered #AL#1), pings (#P#), sends
// short and extended data (#SD#, #D#), black box records (#B#) and driver
// messages (#M#), waits for the server's answers, and takes what the
// server sends: custom commands (answered with #M#, as the protocol
// suggests), messages for the driver (#M#, recorded), picture requests
// (#QI#, answered with a picture in #I# blocks) and firmware or
// configuration uploads (#US#, #UC#, recorded).
package wialonsim

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cB-Sigma/trackers/wialon"
)

// Pos is one position report.
type Pos struct {
	Time     time.Time // UTC
	Lat, Lon float64
	Speed    float64 // km/h
	Heading  float64
	Alt      float64
	Sats     int
	HDOP     float64
	// Params are the additional parameters of a #D# packet: int64 (type
	// 1), float64 (2) or string (3).
	Params map[string]any
}

// Tracker is one simulated device on a TCP connection.
type Tracker struct {
	IMEI     string
	Password string // "" sends NA
	// V11 speaks version 1.1 (no version field, no checksums); set it
	// before Login.
	V11 bool
	// Answer produces the answer to a custom command; nil answers
	// "OK <command>".
	Answer func(cmd string) string
	// Picture is sent when the server asks for one (#QI#).
	Picture []byte

	c   net.Conn
	wmu sync.Mutex

	mu       sync.Mutex
	cmds     []string
	messages []string
	uploads  [][]byte
	answers  map[string]chan string // by answer type (AL, AD, AI, …)
	done     chan struct{}
}

// Dial connects a tracker with this IMEI to addr.
func Dial(addr, imei string) (*Tracker, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{IMEI: imei, c: c, answers: map[string]chan string{}, done: make(chan struct{}),
		Picture: []byte{0xFF, 0xD8, 0xFF, 0xD9}}
	for _, typ := range []string{"AL", "ASD", "AD", "AB", "AP", "AM", "AI", "AIT", "AT"} {
		t.answers[typ] = make(chan string, 16)
	}
	go t.read()
	return t, nil
}

// Close closes the connection.
func (t *Tracker) Close() error { return t.c.Close() }

// Done is closed when the server closed the connection.
func (t *Tracker) Done() <-chan struct{} { return t.done }

// Commands returns the custom commands received so far, as sent.
func (t *Tracker) Commands() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cmds...)
}

// Messages returns the messages for the driver received so far.
func (t *Tracker) Messages() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.messages...)
}

// Uploads returns the firmware and configuration files received.
func (t *Tracker) Uploads() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([][]byte(nil), t.uploads...)
}

func (t *Tracker) read() {
	defer close(t.done)
	br := bufio.NewReader(t.c)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "#A"):
			if ch := t.answers[answerType(line)]; ch != nil {
				select {
				case ch <- line:
				default:
				}
			}
		case strings.HasPrefix(line, "#M#"):
			text := strings.TrimPrefix(line, "#M#")
			if !t.V11 {
				if i := strings.LastIndexByte(text, ';'); i >= 0 {
					text = text[:i]
				}
			}
			t.mu.Lock()
			t.messages = append(t.messages, text)
			t.mu.Unlock()
		case line == "#QI#":
			go func() { _ = t.SendPicture("snapshot.jpg", t.Picture, 2) }()
		case strings.HasPrefix(line, "#US#"), strings.HasPrefix(line, "#UC#"):
			f := strings.Split(line[4:], ";")
			n, err := strconv.Atoi(f[0])
			if err != nil || n < 0 || n > 16<<20 {
				return
			}
			b := make([]byte, n)
			if _, err := io.ReadFull(br, b); err != nil {
				return
			}
			t.mu.Lock()
			t.uploads = append(t.uploads, b)
			t.mu.Unlock()
		case strings.HasPrefix(line, "#QT#"):
			t.mu.Lock()
			t.cmds = append(t.cmds, line)
			t.mu.Unlock()
		case line != "":
			t.mu.Lock()
			t.cmds = append(t.cmds, line)
			t.mu.Unlock()
			ans := "OK " + line
			if t.Answer != nil {
				ans = t.Answer(line)
			}
			go func() { _ = t.write(t.packet("M", ans+";")) }()
		}
	}
}

func (t *Tracker) write(b []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.c.Write(b)
	return err
}

// packet frames a packet; text ends with the separator before the CRC.
func (t *Tracker) packet(typ, text string) []byte {
	if t.V11 {
		return wialon.Packet(typ, strings.TrimRight(text, ";|"), false)
	}
	return wialon.Packet(typ, text, true)
}

// answerType is "AD" of "#AD#1".
func answerType(s string) string {
	s = strings.TrimPrefix(s, "#")
	if i := strings.IndexByte(s, '#'); i >= 0 {
		return s[:i]
	}
	return s
}

func (t *Tracker) await(want string, timeout time.Duration) error {
	select {
	case got := <-t.answers[answerType(want)]:
		if got != want {
			return fmt.Errorf("wialonsim: answer %q, want %q", got, want)
		}
		return nil
	case <-t.done:
		return errors.New("wialonsim: the server closed the connection")
	case <-time.After(timeout):
		return fmt.Errorf("wialonsim: no %s within %v", want, timeout)
	}
}

func (t *Tracker) send(b []byte, want string) error {
	if err := t.write(b); err != nil {
		return err
	}
	return t.await(want, 5*time.Second)
}

// Login sends the login packet and waits for #AL#1.
func (t *Tracker) Login() error {
	pw := t.Password
	if pw == "" {
		pw = "NA"
	}
	if t.V11 {
		return t.send(wialon.Packet("L", t.IMEI+";"+pw, false), "#AL#1")
	}
	return t.send(t.packet("L", "2.0;"+t.IMEI+";"+pw+";"), "#AL#1")
}

// Ping sends a ping and waits for #AP#.
func (t *Tracker) Ping() error { return t.send([]byte("#P#\r\n"), "#AP#") }

func dm(v float64, degDigits int) string {
	v = math.Abs(v)
	d := math.Floor(v)
	return fmt.Sprintf("%0*d%07.4f", degDigits, int(d), (v-d)*60)
}

func hemi(v float64, pos, neg string) string {
	if v < 0 {
		return neg
	}
	return pos
}

// ShortText is the text of an SD record (without the packet type).
func ShortText(p Pos) string {
	tm := p.Time.UTC()
	if tm.IsZero() {
		tm = time.Now().UTC()
	}
	return fmt.Sprintf("%s;%s;%s;%s;%s;%s;%.0f;%.0f;%.0f;%d", tm.Format("020106"), tm.Format("150405"),
		dm(p.Lat, 2), hemi(p.Lat, "N", "S"), dm(p.Lon, 3), hemi(p.Lon, "E", "W"), p.Speed, p.Heading, p.Alt, p.Sats)
}

// DataText is the text of a D record (without the packet type).
func DataText(p Pos) string {
	names := make([]string, 0, len(p.Params))
	for k := range p.Params {
		names = append(names, k)
	}
	sort.Strings(names)
	params := make([]string, 0, len(names))
	for _, k := range names {
		switch v := p.Params[k].(type) {
		case int64:
			params = append(params, fmt.Sprintf("%s:1:%d", k, v))
		case int:
			params = append(params, fmt.Sprintf("%s:1:%d", k, v))
		case float64:
			params = append(params, fmt.Sprintf("%s:2:%s", k, strconv.FormatFloat(v, 'f', -1, 64)))
		default:
			params = append(params, fmt.Sprintf("%s:3:%v", k, v))
		}
	}
	ps := strings.Join(params, ",")
	if ps == "" {
		ps = "NA"
	}
	return fmt.Sprintf("%s;%.1f;NA;NA;;NA;%s", ShortText(p), p.HDOP, ps)
}

// Short sends a short data packet and waits for #ASD#1, or #AD#1: some
// servers (Traccar 6.16.0) answer short data as extended data.
func (t *Tracker) Short(p Pos) error {
	if err := t.write(t.packet("SD", ShortText(p)+";")); err != nil {
		return err
	}
	select {
	case got := <-t.answers["ASD"]:
		if got != "#ASD#1" {
			return fmt.Errorf("wialonsim: answer %q, want #ASD#1", got)
		}
	case got := <-t.answers["AD"]:
		if got != "#AD#1" {
			return fmt.Errorf("wialonsim: answer %q, want #ASD#1 (or #AD#1)", got)
		}
	case <-t.done:
		return errors.New("wialonsim: the server closed the connection")
	case <-time.After(5 * time.Second):
		return errors.New("wialonsim: no #ASD#1 within 5s")
	}
	return nil
}

// Data sends an extended data packet and waits for #AD#1.
func (t *Tracker) Data(p Pos) error { return t.send(t.packet("D", DataText(p)+";"), "#AD#1") }

// BlackBox sends records from memory and waits for #AB#<count>.
func (t *Tracker) BlackBox(ps ...Pos) error {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(DataText(p) + "|")
	}
	return t.send(t.packet("B", b.String()), fmt.Sprintf("#AB#%d", len(ps)))
}

// Message sends a driver's message and waits for #AM#1.
func (t *Tracker) Message(text string) error { return t.send(t.packet("M", text+";"), "#AM#1") }

// SendPicture sends a picture in blocks of at most blockSize bytes,
// waiting for each block's answer.
func (t *Tracker) SendPicture(name string, img []byte, blockSize int) error {
	if blockSize < 1 {
		blockSize = 50 << 10
	}
	n := (len(img) + blockSize - 1) / blockSize
	if n == 0 {
		n = 1
	}
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		blk := img[i*blockSize : min(len(img), (i+1)*blockSize)]
		head := fmt.Sprintf("%d;%d;%d;%s;%s;%s;", len(blk), i, n-1, now.Format("020106"), now.Format("150405"), name)
		var b []byte
		if t.V11 {
			b = []byte("#I#" + strings.TrimSuffix(head, ";") + "\r\n")
		} else {
			b = []byte(fmt.Sprintf("#I#%s%04X\r\n", head, wialon.CRC(blk)))
		}
		want := fmt.Sprintf("#AI#%d;1", i)
		if i == n-1 {
			want = "#AI#1"
		}
		if err := t.send(append(b, blk...), want); err != nil {
			return err
		}
	}
	return nil
}
