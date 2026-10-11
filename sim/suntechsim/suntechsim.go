// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package suntechsim is a Suntech tracker for tests and demos. It speaks
// either the Universal protocol of newer firmware and the ST4300 family
// (headers STT, ALT, ALV, RES, with a field mask) or the older protocol of
// the ST300 and SA200 families (headers such as ST300STT), as chosen by
// the model given to Dial. It sends status reports, alerts and keep-alives
// ending with a carriage return, and answers the commands the library's
// named commands use as the device does: output 1 on and off (the
// immobiliser), a position request (answered, then a status report) and
// a reboot. Commands it does not know are recorded and not answered.
// Frame layouts follow the field orders of the library's suntech package.
package suntechsim

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Firmware is the firmware version the simulator reports.
const Firmware = "1.0.15"

// Pos is one position.
type Pos struct {
	Time     time.Time
	Lat, Lon float64
	Speed    float64 // km/h
	Course   float64
	Sats     int
	Valid    bool
}

// Tracker is one simulated device.
type Tracker struct {
	ID string
	// Model is the older protocol's header prefix (ST300), or "" for the
	// Universal protocol.
	Model string
	// Last is the position a position request is answered with.
	Last Pos

	c    net.Conn
	mu   sync.Mutex
	cmds []string
	out1 bool
	boot int
	num  int
	wmu  sync.Mutex
}

// Dial connects to the server. model "" speaks the Universal protocol;
// a prefix such as ST300 or SA200 the older one.
func Dial(addr, id, model string) (*Tracker, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{ID: id, Model: model, c: c,
		Last: Pos{Time: time.Now().UTC(), Lat: 12.9716, Lon: 77.5946, Sats: 9, Valid: true}}
	go t.read()
	return t, nil
}

// Close drops the connection.
func (t *Tracker) Close() error { return t.c.Close() }

// Commands lists the commands received, without the carriage return.
func (t *Tracker) Commands() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cmds...)
}

// Output1 reports output 1 (the immobiliser relay).
func (t *Tracker) Output1() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.out1
}

// Reboots counts the reboot commands carried out.
func (t *Tracker) Reboots() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.boot
}

func (t *Tracker) read() {
	br := bufio.NewReader(t.c)
	for {
		line, err := br.ReadString('\r')
		if err != nil {
			return
		}
		t.command(strings.TrimSpace(line))
	}
}

func (t *Tracker) command(line string) {
	if line == "" {
		return
	}
	t.mu.Lock()
	t.cmds = append(t.cmds, line)
	t.mu.Unlock()
	f := strings.Split(line, ";")
	if len(f) < 4 || f[1] != t.ID {
		return
	}
	var action string
	if t.Model == "" {
		if f[0] != "CMD" {
			return
		}
		action = map[string]string{"04;01": "Enable1", "04;02": "Disable1", "03;01": "StatusReq", "03;03": "Reboot"}[f[2]+";"+f[3]]
	} else {
		if f[0] != t.Model+"CMD" || f[2] != "02" {
			return
		}
		action = f[3]
	}
	switch action {
	case "Enable1", "Disable1":
		t.mu.Lock()
		t.out1 = action == "Enable1"
		t.mu.Unlock()
	case "StatusReq":
	case "Reboot":
		t.mu.Lock()
		t.boot++
		t.mu.Unlock()
	default:
		return // not a command this device knows
	}
	if t.Model == "" {
		_ = t.Send("RES;" + t.ID + ";" + f[2] + ";" + f[3])
	} else {
		_ = t.Send(t.Model + "CMD;Res;" + t.ID + ";" + Firmware + ";" + action)
	}
	if action == "StatusReq" {
		_ = t.Status(t.Last)
	}
}

// Send writes a frame and its carriage return.
func (t *Tracker) Send(frame string) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.c.Write([]byte(frame + "\r"))
	return err
}

func (t *Tracker) next() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.num++
	return t.num
}

func b01(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// universal is the Universal body after the header and ID with field
// mask 3FFFFF: model, firmware, live flag, date, time, cell, MCC, MNC,
// LAC, signal, latitude, longitude, speed, course, satellites, fix,
// inputs, outputs, then three report-specific fields.
func (t *Tracker) universal(p Pos, a, b, c string) string {
	t.mu.Lock()
	out := b01(t.out1)
	t.mu.Unlock()
	return fmt.Sprintf("3FFFFF;43;%s;1;%s;%s;00008D6C;404;45;0925;24;%+.6f;%+.6f;%.2f;%.2f;%d;%s;00000001;0000000%s;%s;%s;%s",
		Firmware, p.Time.UTC().Format("20060102"), p.Time.UTC().Format("15:04:05"), p.Lat, p.Lon, p.Speed, p.Course, p.Sats, b01(p.Valid), out, a, b, c)
}

// legacy is the older protocol's body after the header and ID (ST300
// layout; SA200 has no model field), up to and with the I/O field.
func (t *Tracker) legacy(p Pos) string {
	t.mu.Lock()
	out := b01(t.out1)
	t.mu.Unlock()
	model := "40;"
	if !strings.HasPrefix(t.Model, "ST3") && t.Model != "ST500" && t.Model != "ST600" {
		model = ""
	}
	return fmt.Sprintf("%s%s;%s;%s;c99e48;%+010.6f;%+011.6f;%07.3f;%06.2f;%d;%s;997100;12.40;1000%s0",
		model, Firmware, p.Time.UTC().Format("20060102"), p.Time.UTC().Format("15:04:05"), p.Lat, p.Lon, p.Speed, p.Course, p.Sats, b01(p.Valid), out)
}

// Status sends a status report (STT).
func (t *Tracker) Status(p Pos) error {
	n := t.next()
	if t.Model == "" {
		return t.Send("STT;" + t.ID + ";" + t.universal(p, "2", "1", fmt.Sprint(n)))
	}
	return t.Send(fmt.Sprintf("%sSTT;%s;%s;2;%04d", t.Model, t.ID, t.legacy(p), n))
}

// Alert sends an alert report (ALT) with its code (42 the panic button,
// 41 main power removed, 1 over-speed …).
func (t *Tracker) Alert(p Pos, code int) error {
	if t.Model == "" {
		return t.Send("ALT;" + t.ID + ";" + t.universal(p, fmt.Sprint(code), "0", ""))
	}
	return t.Send(fmt.Sprintf("%sALT;%s;%s;%d", t.Model, t.ID, t.legacy(p), code))
}

// Emergency sends the panic button: EMG code 1 on the older protocol, ALT
// 42 on the Universal one.
func (t *Tracker) Emergency(p Pos) error {
	if t.Model == "" {
		return t.Alert(p, 42)
	}
	return t.Send(fmt.Sprintf("%sEMG;%s;%s;1", t.Model, t.ID, t.legacy(p)))
}

// Alive sends a keep-alive (ALV).
func (t *Tracker) Alive() error {
	if t.Model == "" {
		return t.Send("ALV;" + t.ID)
	}
	return t.Send(t.Model + "ALV;" + t.ID)
}
