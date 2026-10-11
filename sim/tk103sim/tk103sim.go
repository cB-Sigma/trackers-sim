// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package tk103sim is a TK103 / Coban (GPS103) tracker for tests and
// demos: it logs in ("##,imei:…,A;", answered LOAD), sends heartbeats
// ("<imei>;", answered ON) and position reports with a keyword
// ("imei:…,tracker,…;"), and records the commands the server sends
// ("**,imei:…,B;"). The protocol has no command answers.
package tk103sim

import (
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"time"
)

// Pos is one position report.
type Pos struct {
	Time     time.Time // UTC
	Lat, Lon float64
	Speed    float64 // km/h
	Heading  float64
	Valid    bool
}

// Tracker is one simulated device on a TCP connection.
type Tracker struct {
	IMEI string
	c    net.Conn

	mu      sync.Mutex
	cmds    []string
	replies chan string
	done    chan struct{}
}

// Dial connects a tracker with this IMEI to addr.
func Dial(addr, imei string) (*Tracker, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{IMEI: imei, c: c, replies: make(chan string, 16), done: make(chan struct{})}
	go t.read()
	return t, nil
}

// Close closes the connection.
func (t *Tracker) Close() error { return t.c.Close() }

// Done is closed when the server closed the connection.
func (t *Tracker) Done() <-chan struct{} { return t.done }

// Commands returns the command texts received so far ("B", "C,30s").
func (t *Tracker) Commands() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cmds...)
}

func (t *Tracker) read() {
	defer close(t.done)
	buf := make([]byte, 512)
	for {
		n, err := t.c.Read(buf)
		// Answers are bare words (LOAD, ON); commands start "**,imei:" and
		// end with ';', or with the write from servers that leave the
		// terminator out.
		s := string(buf[:n])
		for s != "" {
			i := strings.Index(s, "**,imei:")
			if i != 0 {
				w := s
				if i > 0 {
					w = s[:i]
				}
				if w = strings.TrimSpace(w); w != "" {
					select {
					case t.replies <- w:
					default:
					}
				}
				if i < 0 {
					break
				}
				s = s[i:]
				continue
			}
			end := strings.IndexByte(s, ';')
			if next := strings.Index(s[1:], "**,imei:"); next >= 0 && (end < 0 || next+1 < end) {
				end = next
			} else if end >= 0 {
				end++
			} else {
				end = len(s)
			}
			cmd := strings.TrimSuffix(s[:end], ";")
			if f := strings.SplitN(cmd, ",", 3); len(f) == 3 {
				t.mu.Lock()
				t.cmds = append(t.cmds, f[2])
				t.mu.Unlock()
			}
			s = s[end:]
		}
		if err != nil {
			return
		}
	}
}

func (t *Tracker) send(s string, want string, timeout time.Duration) error {
	if _, err := t.c.Write([]byte(s)); err != nil {
		return err
	}
	if want == "" {
		return nil
	}
	select {
	case got := <-t.replies:
		if got != want {
			return fmt.Errorf("tk103sim: answer %q, want %q", got, want)
		}
		return nil
	case <-t.done:
		return errors.New("tk103sim: the server closed the connection")
	case <-time.After(timeout):
		return fmt.Errorf("tk103sim: no %s within %v", want, timeout)
	}
}

// Login sends the login frame and waits for LOAD.
func (t *Tracker) Login() error { return t.send("##,imei:"+t.IMEI+",A;", "LOAD", 5*time.Second) }

// Heartbeat sends a heartbeat and waits for ON.
func (t *Tracker) Heartbeat() error { return t.send(t.IMEI+";", "ON", 5*time.Second) }

// ReportFrame is a report frame: keyword "tracker" for a periodic report,
// or an alarm keyword ("help me", "acc on", …).
func ReportFrame(imei, keyword string, p Pos) string {
	tm := p.Time.UTC()
	if tm.IsZero() {
		tm = time.Now().UTC()
	}
	v := "V"
	if p.Valid {
		v = "A"
	}
	return fmt.Sprintf("imei:%s,%s,%s,,F,%s.000,%s,%s,%s,%s,%s,%.2f,%.0f;", imei, keyword, tm.Format("0601021504"),
		tm.Format("150405"), v, dm(math.Abs(p.Lat), 2), hemi(p.Lat, "N", "S"), dm(math.Abs(p.Lon), 3), hemi(p.Lon, "E", "W"),
		p.Speed/1.852, p.Heading)
}

func dm(v float64, degDigits int) string {
	d := math.Floor(v)
	return fmt.Sprintf("%0*d%07.4f", degDigits, int(d), (v-d)*60)
}

func hemi(v float64, pos, neg string) string {
	if v < 0 {
		return neg
	}
	return pos
}

// Report sends a report; the protocol does not answer it.
func (t *Tracker) Report(keyword string, p Pos) error {
	return t.send(ReportFrame(t.IMEI, keyword, p), "", 0)
}
