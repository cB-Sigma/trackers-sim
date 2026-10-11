// Package ruptelasim is a Ruptela FM tracker for tests and demos: it sends
// its identification packet, records (command 1) and extended records
// (command 68), heartbeats and trouble codes, waits for the server's
// acknowledgements, and answers SMS commands sent over GPRS (command 108)
// the way the device's SMS command list describes, echoing an ID tag when
// the command carries one, and set-IO-value commands (117). It also
// answers the version request (103) with command 3, takes the connection
// (105) and odometer (106) settings, and keeps camera files: AddFile
// stores one, the file list (137, subcommand 1) is answered with the names
// and a file request (137, subcommand 2) with that packet, as the device
// sends one packet per request. DTCs builds diagnostic trouble codes.
package ruptelasim

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cB-Sigma/trackers/ruptela"
)

// Tracker is one simulated device.
type Tracker struct {
	IMEI uint64
	// Password is the device's SMS password; when set, commands without it
	// are ignored, as the device does.
	Password string
	// Answer produces the answer to a command (without password and ID
	// tag); nil answers as the SMS command list describes.
	Answer func(cmd string) string
	// Delay, when set, holds the answer to a command this long.
	Delay func(cmd string) time.Duration
	// Hold keeps commands unanswered; Reply answers later.
	Hold bool
	// SetIOResult is the result byte of a set-IO-value answer (0 changed,
	// 1 failed, 2 not supported).
	SetIOResult byte
	// Version is the answer to a version request (103): bootloader,
	// firmware, hardware, GSM level and voltage state.
	Version string
	// PacketSize is the file bytes per files packet (default 512).
	PacketSize int

	c     net.Conn
	acks  chan ack
	mu    sync.Mutex
	cmds  []string
	sets  [][2]uint32
	wmu   sync.Mutex
	bin   []Binary
	files map[fileKey][]byte
}

// Binary is a binary command the tracker received (not an SMS command,
// set-IO value or acknowledgement): its command ID and payload.
type Binary struct {
	Cmd     byte
	Payload []byte
}

type fileKey struct {
	source int
	name   string
}

type ack struct {
	cmd     byte
	payload []byte
}

// Dial connects to the server.
func Dial(addr string, imei uint64) (*Tracker, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	t := &Tracker{IMEI: imei, c: c, acks: make(chan ack, 16)}
	go t.read()
	return t, nil
}

// Close drops the connection.
func (t *Tracker) Close() error { return t.c.Close() }

// Commands lists the SMS commands received so far, as sent.
func (t *Tracker) Commands() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cmds...)
}

// SetIOs lists the set-IO-value commands received (IO ID, value).
func (t *Tracker) SetIOs() [][2]uint32 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([][2]uint32(nil), t.sets...)
}

var idTag = regexp.MustCompile(` (ID[0-9A-Fa-f]{16,32})$`)

func (t *Tracker) read() {
	br := bufio.NewReader(t.c)
	for {
		cmd, p, err := ruptela.ReadServerFrame(br)
		if err != nil {
			close(t.acks)
			return
		}
		switch cmd {
		case ruptela.SMSCommand:
			text := string(p)
			t.mu.Lock()
			t.cmds = append(t.cmds, text)
			hold := t.Hold
			t.mu.Unlock()
			if hold {
				continue
			}
			ans, ok := t.answer(text)
			if !ok {
				continue
			}
			var dt time.Duration
			if t.Delay != nil {
				dt = t.Delay(text)
			}
			if dt > 0 {
				go func() {
					time.Sleep(dt)
					_ = t.Reply(ans)
				}()
				continue
			}
			_ = t.Reply(ans)
		case ruptela.SetIO:
			if len(p) < 8 {
				continue
			}
			t.mu.Lock()
			t.sets = append(t.sets, [2]uint32{binary.BigEndian.Uint32(p[0:4]), binary.BigEndian.Uint32(p[4:8])})
			t.mu.Unlock()
			_ = t.write(ruptela.DeviceFrame(t.IMEI, ruptela.CmdSetIOAnswer, []byte{t.SetIOResult}))
		case ruptela.GetVersion, ruptela.SetConnection, ruptela.SetOdometer, ruptela.Files:
			t.mu.Lock()
			t.bin = append(t.bin, Binary{cmd, append([]byte(nil), p...)})
			hold := t.Hold
			t.mu.Unlock()
			if hold {
				continue
			}
			t.binaryAnswer(cmd, p)
		default:
			t.acks <- ack{cmd, p}
		}
	}
}

// answer builds the device's answer to an SMS command; false when the
// device would not answer (wrong password).
func (t *Tracker) answer(text string) (string, bool) {
	id := ""
	if m := idTag.FindStringSubmatch(text); m != nil {
		id = " " + m[1]
		text = strings.TrimSuffix(text, m[0])
	}
	if t.Password != "" {
		rest, ok := strings.CutPrefix(text, t.Password+" ")
		if !ok {
			return "", false
		}
		text = rest
	}
	if t.Answer != nil {
		return t.Answer(text) + id, true
	}
	return Answer(text, time.Now()) + id, true
}

// Answer is the device's answer to an SMS command (without password),
// in the forms of the SMS command list.
func Answer(cmd string, now time.Time) string {
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return "Unknown command."
	}
	switch f[0] {
	case "coords":
		return fmt.Sprintf("%s, lat. 12.9716000, long. 77.5946000, alt. 920, sat. 9, dir. 90.0, hdop 9, state 3", now.UTC().Format("2006-01-02 15:04:05"))
	case "reset":
		return "Resetting device."
	case "immobilizer":
		if len(f) != 2 {
			return "Invalid speed."
		}
		if s, err := strconv.Atoi(f[1]); err != nil || s < 1 || s > 250 {
			return "Invalid speed."
		}
		return "Immobilizer OK."
	case "resetimmob":
		return "immobilizer reset."
	case "getio":
		return "DIN1=0,DIN2=0,DIN3=0,DIN4=1,DOUT1=0,DOUT2=0,DOUT3=0,DOUT4=0,AIN1=12600,AIN2=0"
	case "setio":
		return "SETIO configuration data ok."
	case "setcfg":
		return "Configuration parameter(s) was set!"
	}
	return "Unknown command."
}

// Reply sends an SMS answer (command 7) with this text.
func (t *Tracker) Reply(text string) error {
	return t.write(ruptela.DeviceFrame(t.IMEI, ruptela.CmdSMSAnswer, []byte(text)))
}

func (t *Tracker) write(b []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.c.Write(b)
	return err
}

// SendRaw writes bytes as they are.
func (t *Tracker) SendRaw(b []byte) error { return t.write(b) }

// Send writes a packet of this command and payload.
func (t *Tracker) Send(cmd byte, payload []byte) error {
	return t.write(ruptela.DeviceFrame(t.IMEI, cmd, payload))
}

// Ack waits for the server's next packet that is not a command.
func (t *Tracker) Ack(timeout time.Duration) (byte, []byte, error) {
	select {
	case a, ok := <-t.acks:
		if !ok {
			return 0, nil, errors.New("connection closed")
		}
		return a.cmd, a.payload, nil
	case <-time.After(timeout):
		return 0, nil, errors.New("no acknowledgement")
	}
}

func (t *Tracker) expect(cmd byte, ok byte) error {
	c, p, err := t.Ack(3 * time.Second)
	if err != nil {
		return err
	}
	if c != cmd || len(p) < 1 {
		return fmt.Errorf("unexpected answer: command %d", c)
	}
	if p[0] != ok {
		return fmt.Errorf("refused: command %d answered %d", c, p[0])
	}
	return nil
}

// Identification is a command 15 payload: device type (4 characters),
// firmware (11), IMSI, operator code and the three coefficients.
func Identification(device, firmware string, imsi uint64, operator uint32) []byte {
	b := []byte(fmt.Sprintf("%-4.4s%-11.11s", device, firmware))
	b = binary.BigEndian.AppendUint64(b, imsi)
	b = binary.BigEndian.AppendUint32(b, operator)
	b = binary.BigEndian.AppendUint32(b, 1000)
	b = binary.BigEndian.AppendUint32(b, 60)
	return binary.BigEndian.AppendUint16(b, 20)
}

// Identify sends the identification packet and waits for its acceptance.
func (t *Tracker) Identify() error {
	if err := t.Send(ruptela.CmdIdent, Identification("Pro4", "00.02.24.00", 404450123456789, 40445)); err != nil {
		return err
	}
	return t.expect(ruptela.AckIdent, 1)
}

// Heartbeat sends a heartbeat and waits for its acknowledgement.
func (t *Tracker) Heartbeat() error {
	if err := t.Send(ruptela.CmdHeartbeat, nil); err != nil {
		return err
	}
	return t.expect(ruptela.AckHeartbeat, 1)
}

// Rec is one simulated record.
type Rec struct {
	Time     time.Time
	Lat, Lon float64
	Alt      float64 // metres
	Angle    float64 // degrees
	Speed    int     // km/h
	Sats     int
	HDOP     float64
	Priority int
	Event    int
	// IO elements by size in bytes (1, 2, 4 or 8).
	IO1, IO2, IO4, IO8 map[uint16]uint64
}

// RecordsPayload builds a command 1 (ext false) or 68 payload.
func RecordsPayload(ext, left bool, recs ...Rec) []byte {
	b := []byte{0, byte(len(recs))}
	if left {
		b[0] = 1
	}
	for _, r := range recs {
		b = binary.BigEndian.AppendUint32(b, uint32(r.Time.Unix()))
		b = append(b, 0)
		if ext {
			b = append(b, 0) // record extension: not split
		}
		b = append(b, byte(r.Priority))
		b = binary.BigEndian.AppendUint32(b, uint32(int32(r.Lon*1e7)))
		b = binary.BigEndian.AppendUint32(b, uint32(int32(r.Lat*1e7)))
		b = binary.BigEndian.AppendUint16(b, uint16(int16(r.Alt*10)))
		b = binary.BigEndian.AppendUint16(b, uint16(r.Angle*100))
		b = append(b, byte(r.Sats))
		b = binary.BigEndian.AppendUint16(b, uint16(r.Speed))
		b = append(b, byte(r.HDOP*10))
		if ext {
			b = binary.BigEndian.AppendUint16(b, uint16(r.Event))
		} else {
			b = append(b, byte(r.Event))
		}
		for i, m := range []map[uint16]uint64{r.IO1, r.IO2, r.IO4, r.IO8} {
			size := 1 << i
			ids := make([]int, 0, len(m))
			for id := range m {
				ids = append(ids, int(id))
			}
			sort.Ints(ids)
			b = append(b, byte(len(ids)))
			for _, id := range ids {
				if ext {
					b = binary.BigEndian.AppendUint16(b, uint16(id))
				} else {
					b = append(b, byte(id))
				}
				v := m[uint16(id)]
				var vb [8]byte
				binary.BigEndian.PutUint64(vb[:], v)
				b = append(b, vb[8-size:]...)
			}
		}
	}
	return b
}

// Records sends records (command 68 when ext) and waits for the server's
// acceptance; left sets the "records left" flag.
func (t *Tracker) Records(ext, left bool, recs ...Rec) error {
	cmd := byte(ruptela.CmdRecords)
	if ext {
		cmd = ruptela.CmdExtRecords
	}
	if err := t.Send(cmd, RecordsPayload(ext, left, recs...)); err != nil {
		return err
	}
	return t.expect(ruptela.AckRecords, 1)
}

// Binaries lists the binary commands received so far.
func (t *Tracker) Binaries() []Binary {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Binary(nil), t.bin...)
}

// AddFile stores a camera file under an 8-character name on a source (0
// camera A SD card, 1 camera B, 2 and 3 camera memory on port A or B).
func (t *Tracker) AddFile(source int, name string, data []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.files == nil {
		t.files = map[fileKey][]byte{}
	}
	t.files[fileKey{source, fmt.Sprintf("%-8.8s", name)}] = append([]byte(nil), data...)
}

func (t *Tracker) binaryAnswer(cmd byte, p []byte) {
	switch cmd {
	case ruptela.GetVersion:
		v := t.Version
		if v == "" {
			v = "542C,00.02.24.00,668,22,1"
		}
		_ = t.Send(ruptela.CmdVersion, []byte(v))
	case ruptela.Files:
		if len(p) < 2 {
			return
		}
		sub, src := p[0], int(p[1])
		t.mu.Lock()
		size := t.PacketSize
		if size <= 0 {
			size = 512
		}
		var names []string
		for k := range t.files {
			if k.source == src {
				names = append(names, k.name)
			}
		}
		var data []byte
		var name string
		if sub == 2 && len(p) >= 12 {
			name = string(p[2:10])
			data = t.files[fileKey{src, name}]
		}
		t.mu.Unlock()
		sort.Strings(names)
		switch sub {
		case 0:
			_ = t.Send(ruptela.CmdFiles, binary.BigEndian.AppendUint16([]byte{0, byte(src)}, uint16(len(names))))
		case 1:
			b := []byte{1, byte(src), byte(len(names))}
			for _, n := range names {
				b = append(b, n...)
			}
			_ = t.Send(ruptela.CmdFiles, b)
		case 2:
			if len(p) < 12 {
				return
			}
			n := int(binary.BigEndian.Uint16(p[10:12]))
			total := (len(data) + size - 1) / size
			b := []byte{2, byte(src)}
			if data == nil || n >= total {
				b = append(b, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0, 0, 0, 0)
				_ = t.Send(ruptela.CmdFiles, b)
				return
			}
			b = append(b, name...)
			b = binary.BigEndian.AppendUint16(b, uint16(total))
			b = binary.BigEndian.AppendUint16(b, uint16(n))
			_ = t.Send(ruptela.CmdFiles, append(b, data[n*size:min((n+1)*size, len(data))]...))
		}
	}
}

// DTC is one diagnostic trouble code.
type DTC struct {
	Source   byte // 0xFF OBD, 1 J1939, 2 J1708
	Time     time.Time
	Lat, Lon float64
	History  bool
	Code     string // the 5-byte code: text for OBD ("P0010"), the bytes for J1939 and J1708
}

// DTCs builds a command 9 payload.
func DTCs(codes ...DTC) []byte {
	b := []byte{byte(len(codes))}
	for _, d := range codes {
		b = append(b, d.Source)
		b = binary.BigEndian.AppendUint32(b, uint32(d.Time.Unix()))
		b = binary.BigEndian.AppendUint32(b, uint32(int32(d.Lon*1e7)))
		b = binary.BigEndian.AppendUint32(b, uint32(int32(d.Lat*1e7)))
		st := byte(1)
		if d.History {
			st = 2
		}
		b = append(b, st)
		b = append(b, fmt.Sprintf("%-5.5s", d.Code)...)
	}
	return b
}

// TroubleCodes sends trouble codes and waits for their acknowledgement.
func (t *Tracker) TroubleCodes(codes ...DTC) error {
	if err := t.Send(ruptela.CmdDTC, DTCs(codes...)); err != nil {
		return err
	}
	return t.expect(ruptela.AckDTC, 1)
}
