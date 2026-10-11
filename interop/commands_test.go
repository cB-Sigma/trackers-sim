// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package interop

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/ais140sim"
	"github.com/cB-Sigma/trackers-sim/sim/gt06sim"
	"github.com/cB-Sigma/trackers-sim/sim/h02sim"
	"github.com/cB-Sigma/trackers-sim/sim/jt808sim"
	"github.com/cB-Sigma/trackers-sim/sim/meitracksim"
	"github.com/cB-Sigma/trackers-sim/sim/queclinksim"
	"github.com/cB-Sigma/trackers-sim/sim/ruptelasim"
	"github.com/cB-Sigma/trackers-sim/sim/teltonikasim"
	"github.com/cB-Sigma/trackers-sim/sim/tk103sim"
	"github.com/cB-Sigma/trackers/ais140"
	"github.com/cB-Sigma/trackers/gt06"
	"github.com/cB-Sigma/trackers/h02"
	"github.com/cB-Sigma/trackers/jt808"
	"github.com/cB-Sigma/trackers/meitrack"
	"github.com/cB-Sigma/trackers/queclink"
	"github.com/cB-Sigma/trackers/ruptela"
	"github.com/cB-Sigma/trackers/teltonika"
	"github.com/cB-Sigma/trackers/tk103"
	"github.com/cB-Sigma/trackers/track"
)

// cmdCase is one command sent both ways: through Traccar's API (Type and
// Attrs, Traccar's command model) and through our codec (Ours, the same
// device action in our command forms). Each frame is reduced to the part
// that says what the device is asked to do (body, below): framing and
// checksums are checked on the way, serial numbers, server flags and
// times are left out. Traccar and Ours are the bodies expected; where they
// differ, Note says why.
type cmdCase struct {
	Type              string
	Attrs             map[string]any
	Ours              any
	Traccar, OursBody string
	Note              string
	// NoProfile: no profile setting of ours gives Traccar's form.
	NoProfile bool
	// TraccarFails is part of the error Traccar answers when it cannot
	// send the command at all; Traccar is then its encoder's text.
	TraccarFails string
}

// cmdProto is one protocol: how to open a device session through the tap,
// our codec, and how to reduce a frame to its body. traccarProfile is a
// profile of ours that gives Traccar's command forms where ours differ by
// choice (namedCommands and the protocol's options).
type cmdProto struct {
	name, uniqueID string
	codec          track.NewCodec
	profile        string
	traccarProfile string
	password       string // our command password (PasswordCommander)
	port           string // the protocol's key in tcpAddr ("" the name)
	dial           func(t *testing.T, addr string) io.Closer
	body           func(frame []byte) (string, error)
	cases          []cmdCase
}

// unsupported is the body of a command our default profile does not have.
const unsupported = "<unsupported by default>"

func named(action string) map[string]any { return map[string]any{"action": action} }

func interval(s int) map[string]any { return map[string]any{"action": "interval", "seconds": s} }

func text(s string) map[string]any { return map[string]any{"text": s} }

func custom(s string) map[string]any { return map[string]any{"data": s} }

var cmdProtos = []cmdProto{
	{
		name: "gt06", uniqueID: "359710049090011", codec: gt06.NewCodec,
		traccarProfile: `{"commandLanguage": false, "namedCommands": {"engineStop": "Relay,1#", "engineResume": "Relay,0#"}}`,
		dial: func(t *testing.T, addr string) io.Closer {
			tr, err := gt06sim.Dial(addr, "359710049090011")
			must(t, err)
			must(t, tr.Login())
			return tr
		},
		body: gt06Body,
		cases: []cmdCase{
			{Type: "engineStop", Ours: named("engineStop"), Traccar: `0x80 "Relay,1#"`, OursBody: `0x80 "RELAY,1#" then 00 02`,
				Note: "ours spells RELAY as the GT06 v1.8 command list does and ends with the language bytes (English) of the Concox layout; " +
					"Traccar sends the language bytes only to devices set to need them"},
			{Type: "engineResume", Ours: named("engineResume"), Traccar: `0x80 "Relay,0#"`, OursBody: `0x80 "RELAY,0#" then 00 02`,
				Note: "as engineStop"},
			{Type: "custom", Attrs: custom("WHERE#"), Ours: text("WHERE#"), Traccar: `0x80 "WHERE#"`, OursBody: `0x80 "WHERE#" then 00 02`,
				Note: "the language bytes, as engineStop"},
		},
	},
	{
		name: "teltonika", uniqueID: "352093081420011", codec: teltonika.NewCodec,
		dial: func(t *testing.T, addr string) io.Closer {
			tr, ok, err := teltonikasim.Dial(addr, "352093081420011")
			must(t, err)
			if !ok {
				t.Fatal("Traccar refused the IMEI")
			}
			_, err = tr.Send(false, teltonikasim.Record{Time: time.Now().Add(-time.Minute), Lat: 12.97, Lon: 77.59, Sats: 9})
			must(t, err)
			return tr
		},
		body: teltonikaBody,
		cases: []cmdCase{
			{Type: "engineStop", Ours: named("engineStop"), Traccar: `codec 12 type 5 "setdigout 1"`},
			{Type: "engineResume", Ours: named("engineResume"), Traccar: `codec 12 type 5 "setdigout 0"`},
			{Type: "custom", Attrs: custom("getinfo"), Ours: text("getinfo"), Traccar: `codec 12 type 5 "getinfo"`},
		},
	},
	{
		name: "tk103", uniqueID: "359586015820011", codec: tk103.NewCodec,
		traccarProfile: `{"commandTerminator": ""}`,
		dial: func(t *testing.T, addr string) io.Closer {
			tr, err := tk103sim.Dial(addr, "359586015820011")
			must(t, err)
			must(t, tr.Login())
			return tr
		},
		body: func(b []byte) (string, error) { return strconv.Quote(string(b)), nil },
		cases: []cmdCase{
			{Type: "engineStop", Ours: named("engineStop"), Traccar: `"**,imei:359586015820011,J"`, OursBody: `"**,imei:359586015820011,J;"`,
				Note: "ours ends commands with the ';' the GPS103 document shows; Traccar sends none"},
			{Type: "engineResume", Ours: named("engineResume"), Traccar: `"**,imei:359586015820011,K"`, OursBody: `"**,imei:359586015820011,K;"`,
				Note: "the terminator, as engineStop"},
			{Type: "positionSingle", Ours: named("locate"), Traccar: `"**,imei:359586015820011,B"`, OursBody: `"**,imei:359586015820011,B;"`,
				Note: "the terminator, as engineStop"},
			{Type: "positionPeriodic", Attrs: map[string]any{"frequency": 30}, Ours: interval(30), Traccar: `"**,imei:359586015820011,C,30s"`,
				OursBody: `"**,imei:359586015820011,C,30s;"`, Note: "the terminator, as engineStop"},
			{Type: "custom", Attrs: custom("E"), Ours: text("E"), Traccar: `"**,imei:359586015820011,E"`, OursBody: `"**,imei:359586015820011,E;"`,
				Note: "the terminator, as engineStop"},
		},
	},
	{
		name: "queclink", uniqueID: "862106020000011", codec: queclink.NewCodec, password: "gv300",
		dial: func(t *testing.T, addr string) io.Closer {
			tr, err := queclinksim.Dial(addr, "862106020000011", "gv300")
			must(t, err)
			must(t, tr.Heartbeat(5*time.Second))
			return tr
		},
		body: queclinkBody,
		cases: []cmdCase{
			{Type: "engineStop", Attrs: map[string]any{"devicePassword": "gv300"}, Ours: named("engineStop"),
				Traccar: `"AT+GTOUT=gv300,1,,,0,0,0,0,0,0,0,,,,,,,<serial>$"`},
			{Type: "engineResume", Attrs: map[string]any{"devicePassword": "gv300"}, Ours: named("engineResume"),
				Traccar: `"AT+GTOUT=gv300,0,,,0,0,0,0,0,0,0,,,,,,,<serial>$"`},
			{Type: "positionSingle", Attrs: map[string]any{"devicePassword": "gv300"}, Ours: named("locate"),
				Traccar: `"AT+GTRTO=gv300,1,,,,,,<serial>$"`},
			{Type: "rebootDevice", Attrs: map[string]any{"devicePassword": "gv300"}, Ours: named("reboot"),
				Traccar: `"AT+GTRTO=gv300,3,,,,,,<serial>$"`},
		},
	},
	{
		name: "meitrack", uniqueID: "864507030000011", codec: meitrack.NewCodec,
		dial: func(t *testing.T, addr string) io.Closer {
			tr, err := meitracksim.Dial(addr, "864507030000011")
			must(t, err)
			must(t, tr.Report(35, meitracksim.Pos{Time: time.Now().Add(-time.Minute), Lat: 12.97, Lon: 77.59, Sats: 9, Valid: true}))
			return tr
		},
		body: meitrackBody,
		cases: []cmdCase{
			{Type: "engineStop", Ours: named("engineStop"), Traccar: `"864507030000011,C01,0,12222"`},
			{Type: "engineResume", Ours: named("engineResume"), Traccar: `"864507030000011,C01,0,02222"`},
			{Type: "positionSingle", Ours: named("locate"), Traccar: `"864507030000011,A10"`},
			{Type: "custom", Attrs: custom("A11,30"), Ours: text("A11,30"), Traccar: `"864507030000011,A11,30"`},
		},
	},
	{
		name: "ruptela", uniqueID: "863071010000011", codec: ruptela.NewCodec, password: "pass",
		traccarProfile: `{"namedCommands": {"engineStop": "{password} immobilizer 10"}}`,
		dial: func(t *testing.T, addr string) io.Closer {
			tr, err := ruptelasim.Dial(addr, 863071010000011)
			must(t, err)
			must(t, tr.Records(false, false, ruptelasim.Rec{Time: time.Now().Add(-time.Minute), Lat: 12.97, Lon: 77.59, Sats: 9}))
			return tr
		},
		body: ruptelaBody,
		cases: []cmdCase{
			{Type: "engineStop", Ours: named("engineStop"), Traccar: `command 108 "pass immobilizer 10"`, OursBody: `command 108 "pass immobilizer 5"`,
				Note: "the same SMS command with another argument (5 against 10); both are values the SMS command list allows"},
			{Type: "engineResume", Ours: named("engineResume"), Traccar: `command 108 "pass resetimmob"`},
			{Type: "custom", Attrs: custom("pass getgps"), Ours: text("pass getgps"), Traccar: `command 108 "pass getgps"`},
		},
	},
	{
		name: "jt808", uniqueID: "013912340011", codec: jt808.NewCodec,
		dial: func(t *testing.T, addr string) io.Closer {
			tr, err := jt808sim.Dial(addr, "013912340011", false)
			must(t, err)
			must(t, tr.Login())
			must(t, tr.Location(jt808sim.Pos{Time: time.Now().Add(-time.Minute), Lat: 12.97, Lon: 77.59, Sats: 9, Valid: true}))
			return tr
		},
		body: jt808Body,
		cases: []cmdCase{
			{Type: "positionSingle", Ours: named("locate"), Traccar: `0x8201 phone 013912340011 body ""`},
			{Type: "rebootDevice", Ours: named("reboot"), Traccar: `0x0310 phone 013912340011 body "01230103"`,
				OursBody: `0x8105 phone 013912340011 body "04"`, NoProfile: true,
				Note: "ours is the standard's terminal control (0x8105, command word 4: reset); Traccar sends a vendor message 0x0310 (parameter 0x23 = 3)"},
			{Type: "positionPeriodic", Attrs: map[string]any{"frequency": 30}, Ours: interval(30), Traccar: `0x0310 phone 013912340011 body "0106040000001E"`,
				OursBody: `0x8103 phone 013912340011 body "0100000029040000001E"`, NoProfile: true,
				Note: "ours is the standard's set parameters (0x8103, parameter 0x0029: default reporting interval); Traccar sends the vendor message 0x0310 (parameter 0x06)"},
			{Type: "engineStop", Ours: named("engineStop"), Traccar: `0x8105 phone 013912340011 body "F0"`,
				OursBody: `0x8500 phone 013912340011 body "01"`, NoProfile: true,
				Note: "ours is the standard's vehicle control (0x8500, flag bit 0: lock); Traccar sends terminal control with the vendor command word 0xF0"},
			{Type: "engineResume", Ours: named("engineResume"), Traccar: `0x8105 phone 013912340011 body "F1"`,
				OursBody: `0x8500 phone 013912340011 body "00"`, NoProfile: true,
				Note: "as engineStop (0xF1: unlock)"},
		},
	},
	{
		name: "h02", uniqueID: "4210000011", codec: h02.NewCodec,
		traccarProfile: `{"namedCommands": {"interval": "S71,22,{seconds}"}}`,
		dial: func(t *testing.T, addr string) io.Closer {
			tr, err := h02sim.Dial(addr, "4210000011")
			must(t, err)
			must(t, tr.Position(h02sim.Pos{Time: time.Now().Add(-time.Minute), Lat: 12.97, Lon: 77.59, Valid: true}))
			return tr
		},
		body: h02Body,
		cases: []cmdCase{
			{Type: "engineStop", Ours: named("engineStop"), Traccar: `"*HQ,4210000011,S20,<time>,1,1#"`},
			{Type: "engineResume", Ours: named("engineResume"), Traccar: `"*HQ,4210000011,S20,<time>,1,0#"`},
			{Type: "positionPeriodic", Attrs: map[string]any{"frequency": 30}, Ours: interval(30), Traccar: `"*HQ,4210000011,S71,<time>,22,30#"`,
				OursBody: `"*HQ,4210000011,XT,30#"`,
				Note:     "ours is the HuaSunTeK document's XT command; Traccar sends the numbered S71 form"},
		},
	},
	{
		name: "ais140", uniqueID: "861693034600011", codec: ais140.NewCodec, password: "1234",
		traccarProfile: `{"namedCommands": {"engineStop": "@SET#RLP,OP1,", "engineResume": "@CLR#RLP,OP1,"}}`,
		dial: func(t *testing.T, addr string) io.Closer {
			d, err := ais140sim.Dial(addr, "861693034600011", "KA01SIM0011")
			must(t, err)
			must(t, d.Send(d.Login()))
			must(t, d.Send(d.Normal(ais140sim.Fix{Time: time.Now().Add(-time.Minute), Lat: 12.97, Lon: 77.59, Sats: 9, Valid: true})))
			return d
		},
		// Traccar's encoder text has no line end (its pipeline never sends
		// it); ours ends the line with CRLF. The text is compared.
		body: func(b []byte) (string, error) { return strconv.Quote(strings.TrimSuffix(string(b), "\r\n")), nil },
		cases: []cmdCase{
			// Traccar 6.16.0's "its" encoder returns a string its pipeline
			// has no encoder for, so the API refuses the command: Traccar
			// cannot send AIS-140 commands. The text compared is the
			// encoder's (ItsProtocolEncoder.java at v6.16.0).
			{Type: "engineStop", Ours: named("engineStop"), Traccar: `"@SET#RLP,OP1,"`, TraccarFails: "unsupported message type",
				OursBody: unsupported, Note: "Table 4D has no engine command, so ours has none by default; Traccar's text is one manufacturer's (RLP), set by profile"},
			{Type: "engineResume", Ours: named("engineResume"), Traccar: `"@CLR#RLP,OP1,"`, TraccarFails: "unsupported message type",
				OursBody: unsupported, Note: "as engineStop"},
		},
	},
}

// TestCommandsAgainstTraccar sends each command through Traccar to a
// simulator, takes the frame Traccar's encoder wrote, makes the frame for
// the same command with our codec (in the same session state: it decodes
// what the simulator sent), and compares the two bodies with the table.
func TestCommandsAgainstTraccar(t *testing.T) {
	for _, p := range cmdProtos {
		t.Run(p.name, func(t *testing.T) {
			needTraccar(t)
			id := device(t, p.uniqueID)
			key := p.port
			if key == "" {
				key = p.name
			}
			tp := newTap(t, tcpAddr[key])
			c := p.dial(t, tp.addr())
			defer c.Close()
			waitOnline(t, id)
			ours := primed(t, p, p.profile, tp)
			var alike track.Codec
			if p.traccarProfile != "" {
				alike = primed(t, p, p.traccarProfile, tp)
			}
			for i, k := range p.cases {
				var tb string
				if k.TraccarFails != "" {
					// Traccar cannot send it; Traccar is then the encoder's
					// text from its source at the same tag.
					st, msg := trySend(t, id, k.Type, k.Attrs)
					if st < 400 || !strings.Contains(msg, k.TraccarFails) {
						t.Errorf("%s: Traccar answered %d %.200s; the table says it fails with %q (fixed in Traccar? update the table)", k.Type, st, msg, k.TraccarFails)
					}
					tb = k.Traccar
				} else {
					mark := tp.mark()
					send(t, id, k.Type, k.Attrs)
					theirs := tp.since(t, mark)
					var err error
					tb, err = p.body(theirs)
					if err != nil {
						t.Errorf("%s: Traccar's frame % X: %v", k.Type, theirs, err)
						continue
					}
				}
				if tb != k.Traccar {
					t.Errorf("%s: Traccar sent %s, the table says %s", k.Type, tb, k.Traccar)
				}
				ob, err := ourBody(p, ours, k.Ours, uint32(100+i))
				if errors.Is(err, track.ErrUnsupported) {
					// The default has no such command; the table says so.
					ob = unsupported
				} else if err != nil {
					t.Errorf("%s: our frame: %v", k.Type, err)
					continue
				}
				want := k.OursBody
				if want == "" {
					want = k.Traccar
				}
				if ob != want {
					t.Errorf("%s: ours is %s, the table says %s (Traccar: %s)", k.Type, ob, want, tb)
				}
				if tb == ob {
					t.Logf("%s: same: %s", k.Type, tb)
					continue
				}
				t.Logf("%s: Traccar %s, ours %s: %s", k.Type, tb, ob, k.Note)
				// Where the difference is a choice of command text or form,
				// a profile gives Traccar's.
				if alike == nil || k.NoProfile {
					continue
				}
				ab, err := ourBody(p, alike, k.Ours, uint32(200+i))
				if err != nil {
					t.Errorf("%s: our frame with profile %s: %v", k.Type, p.traccarProfile, err)
				} else if ab != tb {
					t.Errorf("%s: with profile %s ours is %s, Traccar %s", k.Type, p.traccarProfile, ab, tb)
				} else {
					t.Logf("%s: with profile %s, same: %s", k.Type, p.traccarProfile, ab)
				}
			}
		})
	}
}

// primed is our codec with this profile after decoding what the simulator
// sent through the tap, so it is in the same session state as Traccar's.
func primed(t *testing.T, p cmdProto, profile string, tp *tap) track.Codec {
	t.Helper()
	var raw []byte
	if profile != "" {
		raw = []byte(profile)
	}
	newCodec, err := p.codec(raw)
	must(t, err)
	c := newCodec()
	r := bufio.NewReader(bytes.NewReader(tp.upstream()))
	for {
		_, err := c.Decode(r)
		if err != nil && !errors.Is(err, track.ErrSkip) {
			return c
		}
	}
}

func ourBody(p cmdProto, c track.Codec, content any, seq uint32) (string, error) {
	var frame []byte
	var err error
	if pc, ok := c.(track.PasswordCommander); ok && p.password != "" {
		frame, _, err = pc.CommandWithPassword(p.uniqueID, p.password, content, seq)
	} else {
		frame, _, err = c.Command(p.uniqueID, content, seq)
	}
	if err != nil {
		return "", err
	}
	b, err := p.body(frame)
	if err != nil {
		return "", fmt.Errorf("% X: %w", frame, err)
	}
	return b, nil
}

// ---- bodies -----------------------------------------------------------------

func gt06Body(b []byte) (string, error) {
	f, err := gt06.ReadServerFrame(bufio.NewReader(bytes.NewReader(b)))
	if err != nil {
		return "", err
	}
	if f.Protocol != gt06.PCommand || len(f.Content) < 5 {
		return "", fmt.Errorf("protocol 0x%02X", f.Protocol)
	}
	n := int(f.Content[0]) - 4
	if n < 0 || 5+n > len(f.Content) {
		return "", fmt.Errorf("command length %d", f.Content[0])
	}
	s := fmt.Sprintf("0x%02X %q", f.Protocol, f.Content[5:5+n])
	if rest := f.Content[5+n:]; len(rest) > 0 {
		s += fmt.Sprintf(" then % X", rest)
	}
	return s, nil
}

func teltonikaBody(b []byte) (string, error) {
	if len(b) < 12 || binary.BigEndian.Uint32(b) != 0 {
		return "", errors.New("no preamble")
	}
	n := int(binary.BigEndian.Uint32(b[4:]))
	if len(b) < 8+n+4 {
		return "", fmt.Errorf("data length %d", n)
	}
	data := b[8 : 8+n]
	if uint32(track.CRC16IBM(data)) != binary.BigEndian.Uint32(b[8+n:]) {
		return "", errors.New("CRC mismatch")
	}
	if len(data) < 8 || data[1] != 1 || data[len(data)-1] != 1 {
		return "", errors.New("quantity is not 1")
	}
	size := int(binary.BigEndian.Uint32(data[3:]))
	if 7+size+1 != len(data) {
		return "", fmt.Errorf("command size %d", size)
	}
	return fmt.Sprintf("codec %d type %d %q", data[0], data[2], data[7:7+size]), nil
}

var queclinkSerial = regexp.MustCompile(`,[0-9A-Fa-f]{4}\$$`)

func queclinkBody(b []byte) (string, error) {
	s := string(b)
	if !strings.HasPrefix(s, "AT+GT") || !strings.HasSuffix(s, "$") {
		return "", errors.New("not an AT+GT command")
	}
	return strconv.Quote(queclinkSerial.ReplaceAllString(s, ",<serial>$")), nil
}

func meitrackBody(b []byte) (string, error) {
	s := string(b)
	if !strings.HasPrefix(s, "@@") || !strings.HasSuffix(s, "\r\n") || len(s) < 10 {
		return "", errors.New("not an @@ command")
	}
	star := strings.LastIndexByte(s, '*')
	if star < 0 || star+5 != len(s) {
		return "", errors.New("no checksum")
	}
	var sum byte
	for i := 0; i < star+1; i++ {
		sum += s[i]
	}
	if fmt.Sprintf("%02X", sum) != s[star+1:star+3] {
		return "", fmt.Errorf("checksum %s, computed %02X", s[star+1:star+3], sum)
	}
	comma := strings.IndexByte(s, ',')
	l, err := strconv.Atoi(s[3:comma])
	if err != nil || l != len(s)-comma {
		return "", fmt.Errorf("length field %q, the rest is %d bytes", s[3:comma], len(s)-comma)
	}
	return strconv.Quote(s[comma+1 : star]), nil
}

func ruptelaBody(b []byte) (string, error) {
	cmd, payload, err := ruptela.ReadServerFrame(bufio.NewReader(bytes.NewReader(b)))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("command %d %q", cmd, payload), nil
}

func jt808Body(b []byte) (string, error) {
	f, err := jt808.ReadFrame(bufio.NewReader(bytes.NewReader(b)))
	if err != nil {
		return "", err
	}
	if !f.CheckOK {
		return "", errors.New("check code mismatch")
	}
	return fmt.Sprintf("0x%04X phone %X body \"%X\"", f.ID, f.Phone, f.Body), nil
}

var h02Time = regexp.MustCompile(`^(\*HQ,[^,]+,[^,]+,)\d{6}`)

func h02Body(b []byte) (string, error) {
	s := string(b)
	if !strings.HasPrefix(s, "*HQ,") || !strings.HasSuffix(s, "#") {
		return "", errors.New("not an *HQ command")
	}
	return strconv.Quote(h02Time.ReplaceAllString(s, "${1}<time>")), nil
}
