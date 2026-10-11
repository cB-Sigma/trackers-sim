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
	"strconv"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/atracksim"
	"github.com/cB-Sigma/trackers-sim/sim/eelinksim"
	"github.com/cB-Sigma/trackers-sim/sim/galileoskysim"
	"github.com/cB-Sigma/trackers-sim/sim/suntechsim"
	"github.com/cB-Sigma/trackers-sim/sim/watchsim"
	"github.com/cB-Sigma/trackers-sim/sim/wialonsim"
	"github.com/cB-Sigma/trackers/atrack"
	"github.com/cB-Sigma/trackers/eelink"
	"github.com/cB-Sigma/trackers/galileosky"
	"github.com/cB-Sigma/trackers/suntech"
	"github.com/cB-Sigma/trackers/watch"
	"github.com/cB-Sigma/trackers/wialon"
)

// The protocols added in v0.2.0 whose Traccar encoder takes commands
// (CalAmp's takes none).
func init() {
	quoted := func(b []byte) (string, error) { return strconv.Quote(string(b)), nil }
	suntechCases := func(id, prefix string) []cmdCase {
		return []cmdCase{
			{Type: "engineStop", Ours: named("engineStop"), Traccar: strconv.Quote(prefix + "CMD;" + id + ";" + suntechWord(prefix, "04;01", "02;Enable1") + "\r")},
			{Type: "engineResume", Ours: named("engineResume"), Traccar: strconv.Quote(prefix + "CMD;" + id + ";" + suntechWord(prefix, "04;02", "02;Disable1") + "\r")},
			{Type: "positionSingle", Ours: named("locate"), Traccar: strconv.Quote(prefix + "CMD;" + id + ";" + suntechWord(prefix, "03;01", "02;StatusReq") + "\r")},
			{Type: "rebootDevice", Ours: named("reboot"), Traccar: strconv.Quote(prefix + "CMD;" + id + ";" + suntechWord(prefix, "03;03", "02;Reboot") + "\r")},
		}
	}
	cmdProtos = append(cmdProtos,
		cmdProto{
			name: "suntech", uniqueID: "907000111", codec: suntech.NewCodec,
			dial: func(t *testing.T, addr string) io.Closer {
				tr, err := suntechsim.Dial(addr, "907000111", "")
				must(t, err)
				must(t, tr.Status(suntechsim.Pos{Time: time.Now().Add(-time.Minute), Lat: 12.97, Lon: 77.59, Sats: 9, Valid: true}))
				return tr
			},
			body:  quoted,
			cases: suntechCases("907000111", ""),
		},
		cmdProto{
			name: "suntech-st300", port: "suntech", uniqueID: "907000112", codec: suntech.NewCodec,
			dial: func(t *testing.T, addr string) io.Closer {
				tr, err := suntechsim.Dial(addr, "907000112", "ST300")
				must(t, err)
				must(t, tr.Status(suntechsim.Pos{Time: time.Now().Add(-time.Minute), Lat: 12.97, Lon: 77.59, Sats: 9, Valid: true}))
				return tr
			},
			body:  quoted,
			cases: suntechCases("907000112", "ST300"),
		},
		cmdProto{
			name: "eelink", uniqueID: "868204000000211", codec: eelink.NewCodec,
			dial: func(t *testing.T, addr string) io.Closer {
				tr, err := eelinksim.Dial(addr, "868204000000211")
				must(t, err)
				must(t, tr.Login())
				must(t, tr.Location(eelinksim.Pos{Time: time.Now().Add(-time.Minute), Lat: 12.97, Lon: 77.59, Sats: 9, Valid: true}))
				return tr
			},
			body: eelinkBody,
			cases: []cmdCase{
				{Type: "engineStop", Ours: named("engineStop"), Traccar: `0x80 "RELAY,1#"`},
				{Type: "engineResume", Ours: named("engineResume"), Traccar: `0x80 "RELAY,0#"`},
				{Type: "positionSingle", Ours: named("locate"), Traccar: `0x80 "WHERE#"`},
				{Type: "rebootDevice", Ours: named("reboot"), Traccar: `0x80 "RESET#"`},
				{Type: "custom", Attrs: custom("VERSION#"), Ours: text("VERSION#"), Traccar: `0x80 "VERSION#"`},
			},
		},
		cmdProto{
			name: "watch", uniqueID: "8800000111", codec: watch.NewCodec,
			dial: func(t *testing.T, addr string) io.Closer {
				tr, err := watchsim.Dial(addr, "8800000111")
				must(t, err)
				must(t, tr.Login())
				return tr
			},
			body: watchBody,
			cases: []cmdCase{
				{Type: "positionSingle", Ours: named("locate"), Traccar: `SG*8800000111 "CR"`},
				{Type: "rebootDevice", Ours: named("reboot"), Traccar: `SG*8800000111 "RESET"`},
				{Type: "positionPeriodic", Attrs: map[string]any{"frequency": 60}, Ours: interval(60), Traccar: `SG*8800000111 "UPLOAD,60"`},
				{Type: "custom", Attrs: custom("VERNO"), Ours: text("VERNO"), Traccar: `SG*8800000111 "VERNO"`},
			},
		},
		cmdProto{
			name: "atrack", uniqueID: "352964050000111", codec: atrack.NewCodec,
			traccarProfile: `{"commandTag": false}`,
			dial: func(t *testing.T, addr string) io.Closer {
				tr, err := atracksim.Dial(addr, "352964050000111")
				must(t, err)
				must(t, tr.Login())
				must(t, tr.Report(atracksim.Pos{Time: time.Now().Add(-time.Minute), Lat: 12.97, Lon: 77.59, Sats: 9}))
				return tr
			},
			body: quoted,
			cases: []cmdCase{
				{Type: "custom", Attrs: custom("AT$INFO=?"), Ours: text("AT$INFO=?"), Traccar: `"AT$INFO=?\r\n"`, OursBody: `"AT$INFO+100=?\r\n"`,
					Note: "ours tags the command with a sequence number the device echoes in its answer, so answers are matched to commands; Traccar sends no tag"},
			},
		},
		cmdProto{
			name: "galileosky", uniqueID: "868204000000111", codec: galileosky.NewCodec,
			dial: func(t *testing.T, addr string) io.Closer {
				tr, err := galileoskysim.Dial(addr, "868204000000111")
				must(t, err)
				must(t, tr.Head())
				return tr
			},
			body: galileoskyBody,
			cases: []cmdCase{
				{Type: "custom", Attrs: custom("Status"), Ours: text("Status"), Traccar: `imei 868204000000111 text "Status"`},
				{Type: "outputControl", Attrs: map[string]any{"index": 0, "data": "1"}, Ours: text("Out 0,1"), Traccar: `imei 868204000000111 text "Out 0,1"`},
			},
		},
		cmdProto{
			name: "wialon", uniqueID: "866000000000111", codec: wialon.NewCodec,
			traccarProfile: `{"namedCommands": {"reboot": "reboot"}}`,
			dial: func(t *testing.T, addr string) io.Closer {
				tr, err := wialonsim.Dial(addr, "866000000000111")
				must(t, err)
				must(t, tr.Login())
				return tr
			},
			body: firstLine,
			cases: []cmdCase{
				{Type: "rebootDevice", Ours: named("reboot"), Traccar: `"reboot\r\n"`, OursBody: unsupported,
					Note: "Wialon IPS defines no commands of its own, so ours has no named reboot by default; Traccar's text is one firmware's, set by profile"},
				{Type: "outputControl", Attrs: map[string]any{"index": 1, "data": "1"}, Ours: text("L1=1"), Traccar: `"L1=1\r\n"`},
			},
		},
	)
}

// firstLine is the first line of a text protocol's bytes (Traccar may
// acknowledge the simulator's answer right after the command).
func firstLine(b []byte) (string, error) {
	if i := bytes.Index(b, []byte("\r\n")); i >= 0 {
		b = b[:i+2]
	}
	return strconv.Quote(string(b)), nil
}

func suntechWord(prefix, universal, legacy string) string {
	if prefix == "" {
		return universal
	}
	return legacy
}

func eelinkBody(b []byte) (string, error) {
	p, err := eelink.ReadPackage(bufio.NewReader(bytes.NewReader(b)))
	if err != nil {
		return "", err
	}
	// instruction: type 0x01, server flag (4), text
	if len(p.Content) < 5 || p.Content[0] != 0x01 {
		return "", fmt.Errorf("package 0x%02X content % X", p.PID, p.Content)
	}
	return fmt.Sprintf("0x%02X %q", p.PID, p.Content[5:]), nil
}

func watchBody(b []byte) (string, error) {
	f, err := watch.ReadFrame(bufio.NewReader(bytes.NewReader(b)))
	if err != nil {
		return "", err
	}
	if f.Length != len(f.Content) {
		return "", fmt.Errorf("length field %d, content %d bytes", f.Length, len(f.Content))
	}
	return fmt.Sprintf("%s*%s %q", f.Vendor, f.ID, f.Content), nil
}

func galileoskyBody(b []byte) (string, error) {
	head, tags, err := galileosky.ReadFrame(bufio.NewReader(bytes.NewReader(b)))
	if err != nil {
		return "", err
	}
	if head != 0x01 {
		return "", fmt.Errorf("header 0x%02X", head)
	}
	var imei, text string
	for len(tags) > 0 {
		t := tags[0]
		tags = tags[1:]
		switch t {
		case 0x03:
			if len(tags) < 15 {
				return "", errors.New("short IMEI tag")
			}
			imei, tags = string(tags[:15]), tags[15:]
		case 0x04:
			if len(tags) < 2 {
				return "", errors.New("short device ID tag")
			}
			tags = tags[2:]
		case 0xE0:
			if len(tags) < 4 {
				return "", errors.New("short number tag")
			}
			_ = binary.LittleEndian.Uint32(tags) // the command number differs by design
			tags = tags[4:]
		case 0xE1:
			if len(tags) < 1 || len(tags) < 1+int(tags[0]) {
				return "", errors.New("short text tag")
			}
			n := int(tags[0])
			text, tags = string(tags[1:1+n]), tags[1+n:]
		default:
			return "", fmt.Errorf("tag 0x%02X", t)
		}
	}
	return fmt.Sprintf("imei %s text %q", imei, text), nil
}
