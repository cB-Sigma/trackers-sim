// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package watchsim is a 3G / SeTracker-style children's watch for tests
// and demos: it sends the link keep-alive ("LK", answered), positions
// ("UD", not answered) and alarms ("AL", answered), and answers commands
// by repeating their keyword as the watch does ("[SG*id*0002*CR]",
// "VERNO,<version>").
package watchsim

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/cB-Sigma/trackers/track"
	"github.com/cB-Sigma/trackers/watch"
)

// Pos is one position report.
type Pos struct {
	Time     time.Time
	Lat, Lon float64
	Speed    float64 // km/h
	Heading  float64
	Sats     int
	Valid    bool
}

// Tracker is one simulated watch on a TCP connection.
type Tracker struct {
	// ID is the device ID (often the last ten digits of the IMEI).
	ID string
	// Vendor is the two-character vendor of the frames (default "SG").
	Vendor string
	// Answer produces the answer content to a command; nil repeats its
	// keyword (VERNO with a version).
	Answer func(cmd string) string

	c       net.Conn
	wmu     sync.Mutex
	replies chan string
	mu      sync.Mutex
	cmds    []string
}

// Dial connects a watch with this ID to addr.
func Dial(addr, id string) (*Tracker, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{ID: id, Vendor: "SG", c: c, replies: make(chan string, 16)}
	go t.read()
	return t, nil
}

// Close closes the connection.
func (t *Tracker) Close() error { return t.c.Close() }

// Commands returns the command contents received so far ("CR", "UPLOAD,60").
func (t *Tracker) Commands() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cmds...)
}

// Server answers to the watch's own frames.
var answers = map[string]bool{"LK": true, "AL": true, "TKQ": true, "TKQ2": true, "INIT,1": true}

func (t *Tracker) read() {
	defer close(t.replies)
	r := bufio.NewReader(t.c)
	for {
		f, err := watch.ReadFrame(r)
		if errors.Is(err, track.ErrSkip) {
			continue
		}
		if err != nil {
			return
		}
		content := string(f.Content)
		if answers[content] {
			t.replies <- content
			continue
		}
		t.mu.Lock()
		t.cmds = append(t.cmds, content)
		t.mu.Unlock()
		ans := t.answer(content)
		if ans != "" {
			_ = t.write(watch.Encode(t.Vendor, t.ID, f.Index, []byte(ans)))
		}
	}
}

func (t *Tracker) answer(cmd string) string {
	if t.Answer != nil {
		return t.Answer(cmd)
	}
	kw, _, _ := strings.Cut(cmd, ",")
	switch strings.ToUpper(kw) {
	case "VERNO":
		return "VERNO,SIM_WATCH_V1.0"
	case "IP":
		return "" // moves to another server: no answer
	}
	return kw
}

func (t *Tracker) write(b []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.c.Write(b)
	return err
}

func (t *Tracker) send(content, want string) error {
	if err := t.write(watch.Encode(t.Vendor, t.ID, "", []byte(content))); err != nil {
		return err
	}
	if want == "" {
		return nil
	}
	select {
	case got, ok := <-t.replies:
		if !ok {
			return errors.New("watchsim: the server closed the connection")
		}
		if got != want {
			return fmt.Errorf("watchsim: answer %q, want %q", got, want)
		}
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("watchsim: no %s within 5s", want)
	}
}

// Login sends the link keep-alive with steps, rolls and battery and waits
// for LK.
func (t *Tracker) Login() error { return t.send("LK,0,0,95", "LK") }

// position is the location data: time, fix, speed, course, altitude,
// satellites, GSM, battery, steps, rolls, status word, one cell, no WiFi.
func position(p Pos, status uint32) string {
	tm := p.Time.UTC()
	if tm.IsZero() {
		tm = time.Now().UTC()
	}
	v, ns, ew := "V", "N", "E"
	if p.Valid {
		v = "A"
	}
	if p.Lat < 0 {
		ns = "S"
	}
	if p.Lon < 0 {
		ew = "W"
	}
	return fmt.Sprintf("%s,%s,%s,%.6f,%s,%.7f,%s,%.2f,%.1f,920.0,%d,80,95,1200,3,%08X,1,1,404,45,4660,11259375,60,0",
		tm.Format("020106"), tm.Format("150405"), v, math.Abs(p.Lat), ns, math.Abs(p.Lon), ew, p.Speed, p.Heading, p.Sats, status)
}

// Report sends a UD position (not answered).
func (t *Tracker) Report(p Pos) error { return t.send("UD,"+position(p, 0), "") }

// Alarm sends an AL position with this status word (0x00010000 SOS) and
// waits for AL.
func (t *Tracker) Alarm(p Pos, status uint32) error { return t.send("AL,"+position(p, status), "AL") }
