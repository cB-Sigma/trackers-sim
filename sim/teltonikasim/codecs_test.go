// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package teltonikasim_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/cB-Sigma/trackers-sim/sim/internal/codectest"
	"github.com/cB-Sigma/trackers-sim/sim/teltonikasim"
	"github.com/cB-Sigma/trackers/teltonika"
	"github.com/cB-Sigma/trackers/track"
)

// Codec 16 records (2-byte IO IDs, a generation type) decode with the
// library's codec.
func TestCodec16AgainstCodec(t *testing.T) {
	at := time.Date(2026, 10, 11, 6, 0, 0, 0, time.UTC)
	rec := teltonikasim.Record{Time: at, Lat: 28.6139, Lon: 77.209, Sats: 9, Speed: 40, Generation: 7,
		IO1: map[uint16]uint8{239: 1}, IO2: map[uint16]uint16{66: 12600}, IO4: map[uint16]uint32{300: 7}}
	addr, msgs := codectest.Serve(t, teltonika.NewCodec, nil)
	tr, ok, err := teltonikasim.Dial(addr, "860000000000016")
	if err != nil || !ok {
		t.Fatalf("handshake %v %v", ok, err)
	}
	defer tr.Close()
	if n, err := tr.SendCodec(teltonika.Codec16, rec); err != nil || n != 1 {
		t.Fatalf("ack %d %v", n, err)
	}
	m, f := codectest.NextFix(t, msgs)
	st := m.Records[0].Status
	if !codectest.Near(f.Lat, 28.6139) || st["generation"] != "periodical" || st["ignition"] != true ||
		st["externalVoltage"] != 12.6 || st["io"].(map[string]any)["300"] != uint64(7) {
		t.Fatalf("codec 16 %+v %v", f, st)
	}
	if _, err := tr.SendCodec(0x42, rec); err == nil {
		t.Error("unknown AVL codec accepted")
	}
}

// Codec 13 and Codec 15 device messages reach the server as a status
// "message" with the device's time; they are not acknowledged.
func TestMessagesAgainstCodec(t *testing.T) {
	addr, msgs := codectest.Serve(t, teltonika.NewCodec, nil)
	tr, ok, err := teltonikasim.Dial(addr, "860000000000024")
	if err != nil || !ok {
		t.Fatalf("handshake %v %v", ok, err)
	}
	defer tr.Close()
	at := time.Date(2026, 10, 11, 6, 30, 0, 0, time.UTC)
	for _, c := range []byte{teltonika.Codec13, teltonika.Codec15} {
		if err := tr.SendMessage(c, at, "fuel 42"); err != nil {
			t.Fatal(err)
		}
		m := next(t, msgs)
		if len(m.Records) != 1 || m.Records[0].Status["message"] != "fuel 42" || !m.Records[0].Time.Equal(at) {
			t.Fatalf("codec 0x%02X %+v", c, m)
		}
	}
	if err := tr.SendMessage(teltonika.Codec12, at, "x"); err == nil {
		t.Error("codec 12 accepted as a device message")
	}
}

// The tracker answers Codec 12 commands in Codec 12 (or Codec 13 / 15 when
// AnswerCodec says so), Codec 14 commands in Codec 14 with an ACK when the
// IMEI is its own and a nACK otherwise, and keeps setparam values; the
// interval named command sets its reporting period.
func TestCommandAnswers(t *testing.T) {
	for _, tc := range []struct {
		profile string
		answer  byte
	}{{`{}`, 0}, {`{"commandCodec": 14}`, 0}, {`{}`, teltonika.Codec13}, {`{}`, teltonika.Codec15}} {
		nc, err := teltonika.NewCodec(json.RawMessage(tc.profile))
		if err != nil {
			t.Fatal(err)
		}
		srv, dev := net.Pipe()
		imei := "860000000000032"
		got := make(chan *teltonikasim.Tracker, 1)
		go func() {
			tr, _, _ := teltonikasim.Accept(dev, imei)
			got <- tr
		}()
		c := nc()
		m := decodeOn(t, c, srv)
		if !m.Login {
			t.Fatalf("login %+v", m)
		}
		_, _ = srv.Write(m.Reply)
		tr := <-got
		if tr == nil {
			t.Fatal("handshake")
		}
		tr.SetAnswerCodec(tc.answer)
		f, _, err := c.Command(imei, map[string]any{"action": "interval", "seconds": 30.0}, 1)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = srv.Write(f)
		m = decodeOn(t, c, srv)
		switch tc.answer {
		case 0:
			if m.Response == nil || m.Failed || !strings.HasPrefix(*m.Response, "New value 10000:30;10050:30") {
				t.Fatalf("%s: answer %+v", tc.profile, m)
			}
		default:
			if len(m.Records) != 1 || !strings.HasPrefix(m.Records[0].Status["message"].(string), "New value 10000:30") {
				t.Fatalf("codec 0x%02X answer %+v", tc.answer, m)
			}
		}
		_ = srv.Close()
	}
}

// A Codec 14 command for another IMEI is refused with the nACK type, which
// fails the command; the tracker's period follows setparam. Commands go
// server to device, so the test plays the server over a pipe.
func TestCodec14OtherIMEIAndInterval(t *testing.T) {
	srv, dev := net.Pipe()
	defer srv.Close()
	got := make(chan *teltonikasim.Tracker, 1)
	go func() {
		d, _, _ := teltonikasim.Accept(dev, "860000000000040")
		got <- d
	}()
	nc, _ := teltonika.NewCodec(json.RawMessage(`{"commandCodec": 14}`))
	c := nc()
	m := decodeOn(t, c, srv)
	_, _ = srv.Write(m.Reply)
	d := <-got
	if d == nil || d.Interval() != 0 {
		t.Fatal("no tracker, or an interval before any setparam")
	}
	f, _ := teltonika.Codec14Frame("860000000000099", "setparam 10050:15")
	_, _ = srv.Write(f)
	m = decodeOn(t, c, srv)
	if m.Response == nil || !m.Failed {
		t.Fatalf("other IMEI %+v", m)
	}
	if d.Interval() != 0 {
		t.Fatal("a refused command changed the interval")
	}
	f, _ = teltonika.Codec14Frame("860000000000040", "setparam 10050:15")
	_, _ = srv.Write(f)
	m = decodeOn(t, c, srv)
	if m.Response == nil || m.Failed || d.Interval() != 15*time.Second || d.Params()[10050] != "15" {
		t.Fatalf("own IMEI %+v %v %v", m, d.Interval(), d.Params())
	}
	f, _ = teltonika.Codec14Frame("860000000000040", "getparam 10050")
	_, _ = srv.Write(f)
	m = decodeOn(t, c, srv)
	if m.Response == nil || *m.Response != "Param ID:10050 Value:15" {
		t.Fatalf("getparam %+v", m)
	}
	if cmds := d.Commands(); len(cmds) != 3 || cmds[0] != "setparam 10050:15" {
		t.Fatalf("commands %q", cmds)
	}
}

// next waits for the next decoded message other than the login.
func next(t *testing.T, msgs <-chan *track.Message) *track.Message {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-msgs:
			if !m.Login {
				return m
			}
		case <-deadline:
			t.Fatal("nothing decoded")
			return nil
		}
	}
}

// decodeOn decodes the next message the tracker sends on conn.
func decodeOn(t *testing.T, c track.Codec, conn net.Conn) *track.Message {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	br, ok := readers[conn]
	if !ok {
		br = bufio.NewReader(conn)
		readers[conn] = br
	}
	for {
		m, err := c.Decode(br)
		if errors.Is(err, track.ErrSkip) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
}

var readers = map[net.Conn]*bufio.Reader{}
