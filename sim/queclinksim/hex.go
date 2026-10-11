// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package queclinksim

// The HEX (binary) report format, as a GV300N sends it with <Protocol
// Format> 1 and AT+GTHRM masks that name the device by its IMEI and leave
// out RFID and CAN data: section 4 of the GV300N @Track Air Interface
// Protocol V3.01. Commands and the server's +SACK answers stay ASCII.

import (
	"encoding/binary"
	"strconv"
	"strings"
	"time"
)

// The masks the simulator's frames carry.
const (
	// HexReportMask (+RSP, +EVT): speed, azimuth, altitude, cell, send
	// time, count, length, device type, versions, battery, external
	// power, analogue mode and inputs, digital I/O, motion, satellites,
	// mileages and hour meters; IMEI as the unique ID (bit 6 clear).
	HexReportMask = 0x00FEFFBF
	// HexAckMask (+ACK): length, device type, versions, send time,
	// count; IMEI (bit 4 clear).
	HexAckMask = 0x6F
	// HexHeartbeatMask (+HBD): as HexAckMask, with the unique ID (bit 7).
	HexHeartbeatMask = 0xEF
)

var hexRSPTypes = map[string]byte{
	"GTTOW": 1, "GTLBC": 3, "GTEPS": 4, "GTDIS": 5, "GTIOB": 6, "GTFRI": 7, "GTGEO": 8, "GTSPD": 9, "GTSOS": 10,
	"GTRTL": 11, "GTDOG": 12, "GTAIS": 14, "GTHBM": 15, "GTIGL": 16, "GTIDA": 17, "GTERI": 18, "GTGIN": 20, "GTGOT": 21,
}

var hexEVTTypes = map[string]byte{
	"GTPNA": 1, "GTPFA": 2, "GTMPN": 3, "GTMPF": 4, "GTBPL": 6, "GTBTC": 7, "GTSTC": 8, "GTSTT": 9, "GTANT": 10,
	"GTPDP": 12, "GTIGN": 13, "GTIGF": 14, "GTIDN": 16, "GTIDF": 17, "GTJDR": 20, "GTSTR": 23, "GTSTP": 24,
	"GTCRA": 25, "GTLSP": 29, "GTJDS": 32, "GTRMD": 33,
}

var hexACKTypes = map[string]byte{
	"GTBSI": 0, "GTSRI": 1, "GTQSS": 2, "GTCFG": 4, "GTTOW": 5, "GTEPS": 6, "GTDIS": 7, "GTOUT": 8, "GTIOB": 9,
	"GTTMA": 10, "GTFRI": 11, "GTGEO": 12, "GTSPD": 13, "GTSOS": 14, "GTMON": 15, "GTRTO": 16, "GTUPD": 21,
	"GTPIN": 22, "GTDAT": 23, "GTOWH": 24, "GTDOG": 25, "GTAIS": 26, "GTJDC": 27, "GTIDL": 28, "GTHBM": 29,
	"GTHMC": 30, "GTURT": 32, "GTHRM": 35, "GTIDA": 43, "GTCRA": 46, "GTTMP": 51, "GTCAN": 57,
}

var rtoSubIDs = map[string]byte{"GPS": 0, "RTL": 1, "READ": 2, "REBOOT": 3, "RESET": 4, "PWROFF": 5, "CID": 6, "CSQ": 7, "VER": 8, "BAT": 9, "IOS": 10}

// crc16 is the frames' checksum: polynomial 0x1021, initial value 0xFFFF.
func crc16(b []byte) uint16 {
	c := uint16(0xFFFF)
	for _, x := range b {
		c ^= uint16(x) << 8
		for i := 0; i < 8; i++ {
			if c&0x8000 != 0 {
				c = c<<1 ^ 0x1021
			} else {
				c <<= 1
			}
		}
	}
	return c
}

type hexw struct{ b []byte }

func (w *hexw) put(v ...byte) { w.b = append(w.b, v...) }
func (w *hexw) u16(v int)     { w.b = binary.BigEndian.AppendUint16(w.b, uint16(v)) }
func (w *hexw) u32(v int64)   { w.b = binary.BigEndian.AppendUint32(w.b, uint32(v)) }
func (w *hexw) when(t time.Time) {
	t = t.UTC()
	w.u16(t.Year())
	w.put(byte(t.Month()), byte(t.Day()), byte(t.Hour()), byte(t.Minute()), byte(t.Second()))
}

// version writes device type, protocol version and firmware version.
func (w *hexw) version() {
	v, _ := strconv.ParseUint(Version, 16, 32)
	w.put(byte(v>>16), byte(v>>8), byte(v), 0x01, 0x02)
}

// imei writes the IMEI two digits per byte, the fifteenth in the last.
func (w *hexw) imei(s string) {
	s = (s + "000000000000000")[:15]
	for i := 0; i < 14; i += 2 {
		w.put((s[i]-'0')*10 + s[i+1] - '0')
	}
	w.put(s[14] - '0')
}

// decimal writes an integer part of n bytes and a one-digit fraction.
func (w *hexw) decimal(v float64, n int) {
	whole := int64(v)
	frac := byte(int64(v*10+0.5) % 10)
	for i := n - 1; i >= 0; i-- {
		w.put(byte(whole >> (8 * i)))
	}
	w.put(frac)
}

// digits writes an MCC or MNC as hex digits (404 → 0x0404).
func (w *hexw) digits(v string) {
	n, _ := strconv.ParseUint(v, 16, 16)
	w.u16(int(n))
}

// seal sets the 2-byte (or, small, 1-byte) length at lenAt and adds the
// checksum and the tail.
func (w *hexw) seal(lenAt int, small bool) []byte {
	n := len(w.b) + 4
	if small {
		w.b[lenAt] = byte(n)
	} else {
		binary.BigEndian.PutUint16(w.b[lenAt:], uint16(n))
	}
	w.u16(int(crc16(w.b[4:])))
	w.put(0x0D, 0x0A)
	return w.b
}

func (t *Tracker) sendHex(b []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.c.Write(b)
	return err
}

// hexHeartbeat sends +HBD with count cnt.
func (t *Tracker) hexHeartbeat(cnt uint16) error {
	w := &hexw{}
	w.put('+', 'H', 'B', 'D', HexHeartbeatMask, 0)
	w.version()
	w.imei(t.IMEI)
	w.when(time.Now())
	w.u16(int(cnt))
	return t.sendHex(w.seal(5, true))
}

// hexAck sends a command's +ACK: its type, the sub-command ID (AT+GTRTO)
// and the serial number.
func (t *Tracker) hexAck(name, sub, serial string) error {
	s, _ := strconv.ParseUint(serial, 16, 16)
	w := &hexw{}
	w.put('+', 'A', 'C', 'K', hexACKTypes[name], HexAckMask, 0)
	w.version()
	w.imei(t.IMEI)
	w.put(rtoSubIDs[sub])
	w.u16(int(s))
	w.when(time.Now())
	w.u16(int(t.nextN()))
	return t.sendHex(w.seal(6, true))
}

// io is the items between the unique ID and the type's own items.
type hexIO struct {
	battery, mv int
	in, out     byte
	motion      byte
}

// hexReport sends a +RSP (rsp) or +EVT with one header item set, the
// type's own items, the positions and the mileage.
func (t *Tracker) hexReport(event, history bool, typ byte, io hexIO, extra []byte, ps []Pos) error {
	head := "+RSP"
	switch {
	case event && history:
		head = "+BVT"
	case event:
		head = "+EVT"
	case history:
		head = "+BSP"
	}
	w := &hexw{}
	w.put([]byte(head)...)
	w.put(typ)
	w.u32(HexReportMask)
	w.u16(0)
	w.version()
	w.imei(t.IMEI)
	w.put(byte(io.battery))
	w.u16(io.mv)
	w.u16(0)              // analogue input mode
	w.u16(0)              // analogue input 1, mV
	w.u16(0)              // analogue input 2, mV
	w.put(io.in, io.out)  // digital inputs, outputs
	w.put(io.motion)      // motion state
	w.put(0x10 | byte(8)) // GPS antenna present, 8 satellites
	w.put(extra...)
	w.put(byte(len(ps)))
	for _, p := range ps {
		w.put(byte(p.HDOP))
		w.decimal(p.Speed, 2)
		w.u16(p.Heading)
		w.u16(int(int16(p.Alt)))
		w.u32(int64(int32(p.Lon * 1e6)))
		w.u32(int64(int32(p.Lat * 1e6)))
		w.when(p.Time)
		w.digits("0404")
		w.digits("0045")
		w.u16(0x1A2B)
		w.u16(0xC3D4)
		w.put(0)
	}
	last := ps[len(ps)-1]
	w.decimal(0, 2)            // current mileage
	w.decimal(last.Mileage, 4) // total mileage
	w.put(0, 0, 0)             // current hour meter
	w.u32(0)                   // total hour meter, hours
	w.put(0, 0)                // minutes, seconds
	w.when(time.Now())
	w.u16(int(t.nextN()))
	return t.sendHex(w.seal(9, false))
}

// hexStatus reads the 6 hex digits of the ASCII device status (motion,
// inputs, outputs).
func hexStatus(s string) (motion, in, out byte) {
	v, _ := strconv.ParseUint((s + "000000")[:6], 16, 32)
	return byte(v >> 16), byte(v >> 8), byte(v)
}

func hexByte(s string) byte {
	v, _ := strconv.ParseUint(strings.TrimSpace(s), 16, 8)
	return byte(v)
}
