// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package interop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The peer: Traccar at this release. The encoders compared in
// commands_test.go are the ones in this image (source tag v6.16.0).
const traccarImage = "traccar/traccar:6.16.0"

// Traccar's default port of each protocol (PortConfigSuffix in its
// source), by our protocol name.
var traccarPorts = map[string]int{
	"tk103":      5001, // Traccar's "gps103"
	"queclink":   5004, // "gl200"
	"suntech":    5011,
	"h02":        5013,
	"jt808":      5015,
	"meitrack":   5020,
	"gt06":       5023,
	"teltonika":  5027,
	"galileosky": 5034, // "galileo"
	"wialon":     5039,
	"atrack":     5044,
	"ruptela":    5046,
	"osmand":     5055,
	"eelink":     5064,
	"calamp":     5082,
	"watch":      5093,
	"ais140":     5179, // "its"
}

// Protocols whose UDP port is published too.
var traccarUDP = map[string]bool{"teltonika": true, "h02": true, "calamp": true}

const (
	apiUser     = "interop@example.test" // demo values for a throwaway container
	apiPassword = "interop-demo"
)

var (
	peerOnce sync.Once
	peerName string
	peerOwn  bool
	peerErr  error
	apiBase  string
	tcpAddr  = map[string]string{}
	udpAddr  = map[string]string{}
)

func TestMain(m *testing.M) {
	code := m.Run()
	if peerOwn {
		_ = exec.Command("docker", "container", "rm", "-f", peerName).Run()
	}
	os.Exit(code)
}

// needTraccar starts Traccar once (or finds the named container), waits for
// its API and makes the admin user.
func needTraccar(t *testing.T) {
	t.Helper()
	if os.Getenv("SIGMA_TRACKERS_INTEROP") != "1" {
		t.Skip("runs Traccar in Docker; set SIGMA_TRACKERS_INTEROP=1 to run")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not on PATH")
	}
	peerOnce.Do(func() { peerErr = startTraccar() })
	if peerErr != nil {
		t.Fatal(peerErr)
	}
}

func startTraccar() error {
	peerName = os.Getenv("SIGMA_TRACKERS_TRACCAR_CONTAINER")
	if peerName == "" {
		peerName = fmt.Sprintf("sigma-trackers-traccar-%d", os.Getpid())
		args := []string{"run", "-d", "--name", peerName, "-p", "127.0.0.1::8082"}
		for p, n := range traccarPorts {
			args = append(args, "-p", fmt.Sprintf("127.0.0.1::%d", n))
			if traccarUDP[p] {
				args = append(args, "-p", fmt.Sprintf("127.0.0.1::%d/udp", n))
			}
		}
		args = append(args, traccarImage)
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("docker run: %v: %s", err, out)
		}
		peerOwn = true
	}
	api, err := published(peerName, "8082/tcp")
	if err != nil {
		return err
	}
	apiBase = "http://" + api
	for p, n := range traccarPorts {
		if tcpAddr[p], err = published(peerName, fmt.Sprintf("%d/tcp", n)); err != nil {
			return err
		}
		if traccarUDP[p] {
			if udpAddr[p], err = published(peerName, fmt.Sprintf("%d/udp", n)); err != nil {
				return err
			}
		}
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		r, err := http.Get(apiBase + "/api/server")
		if err == nil {
			_ = r.Body.Close()
			if r.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Traccar's API did not come up: %v", err)
		}
		time.Sleep(time.Second)
	}
	// The first user registered becomes the administrator; on a reused
	// container it exists already.
	body, _ := json.Marshal(map[string]any{"name": "interop", "email": apiUser, "password": apiPassword})
	r, err := http.Post(apiBase+"/api/users", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	_ = r.Body.Close()
	// Protocol listeners open after the API; wait for the last one.
	for _, a := range tcpAddr {
		for {
			c, err := net.DialTimeout("tcp", a, time.Second)
			if err == nil {
				_ = c.Close()
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("Traccar's port %s did not open", a)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	return nil
}

// published is the host address of a container port.
func published(name, port string) (string, error) {
	out, err := exec.Command("docker", "port", name, port).Output()
	if err != nil {
		return "", fmt.Errorf("docker port %s %s: %v", name, port, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "127.0.0.1:") || strings.HasPrefix(line, "0.0.0.0:") {
			return strings.Replace(line, "0.0.0.0:", "127.0.0.1:", 1), nil
		}
	}
	return "", fmt.Errorf("docker port %s %s: %q", name, port, out)
}

// api calls Traccar's REST API as the administrator; out, when set,
// receives the JSON answer.
func api(t *testing.T, method, path string, in, out any) int {
	t.Helper()
	st, b := apiRaw(t, method, path, in)
	if st >= 300 {
		t.Fatalf("%s %s: %d %s", method, path, st, b)
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, b)
		}
	}
	return st
}

// trySend sends a command through Traccar's API and returns the status and
// answer, failing nothing.
func trySend(t *testing.T, id int64, typ string, attrs map[string]any) (int, string) {
	t.Helper()
	if attrs == nil {
		attrs = map[string]any{}
	}
	st, b := apiRaw(t, "POST", "/api/commands/send", map[string]any{"deviceId": id, "type": typ, "attributes": attrs})
	return st, string(b)
}

// apiRaw makes one API request and returns the status and body.
func apiRaw(t *testing.T, method, path string, in any) (int, []byte) {
	t.Helper()
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, apiBase+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(apiUser, apiPassword)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, b
}

// device makes a Traccar device with uniqueID (once per container) and
// returns its id.
func device(t *testing.T, uniqueID string) int64 {
	t.Helper()
	var list []struct {
		ID int64 `json:"id"`
	}
	api(t, "GET", "/api/devices?uniqueId="+url.QueryEscape(uniqueID), nil, &list)
	if len(list) > 0 {
		return list[0].ID
	}
	var d struct {
		ID int64 `json:"id"`
	}
	api(t, "POST", "/api/devices", map[string]any{"name": "sim " + uniqueID, "uniqueId": uniqueID}, &d)
	return d.ID
}

// position is the part of Traccar's position we check.
type position struct {
	ID         int64          `json:"id"`
	Protocol   string         `json:"protocol"`
	FixTime    time.Time      `json:"fixTime"`
	Valid      bool           `json:"valid"`
	Latitude   float64        `json:"latitude"`
	Longitude  float64        `json:"longitude"`
	Speed      float64        `json:"speed"` // knots
	Course     float64        `json:"course"`
	Attributes map[string]any `json:"attributes"`
}

// route reads the device's positions since from.
func route(t *testing.T, id int64, from time.Time) []position {
	t.Helper()
	var out []position
	q := url.Values{"deviceId": {strconv.FormatInt(id, 10)},
		"from": {from.Add(-48 * time.Hour).UTC().Format(time.RFC3339)}, "to": {time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)}}
	api(t, "GET", "/api/reports/route?"+q.Encode(), nil, &out)
	return out
}

// waitPositions waits until the device has at least n positions with an ID
// above after, and returns them.
func waitPositions(t *testing.T, id int64, after int64, n int) []position {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var got []position
		for _, p := range route(t, id, time.Now()) {
			if p.ID > after {
				got = append(got, p)
			}
		}
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("Traccar has %d of %d positions for device %d", len(got), n, id)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// lastID is the highest position ID the device has (0 none).
func lastID(t *testing.T, id int64) int64 {
	t.Helper()
	var max int64
	for _, p := range route(t, id, time.Now()) {
		if p.ID > max {
			max = p.ID
		}
	}
	return max
}

// event is a Traccar event.
type event struct {
	Type       string         `json:"type"`
	PositionID int64          `json:"positionId"`
	Attributes map[string]any `json:"attributes"`
}

// alarms reads the device's alarm events.
func alarms(t *testing.T, id int64) []event {
	t.Helper()
	var out []event
	q := url.Values{"deviceId": {strconv.FormatInt(id, 10)}, "type": {"alarm"},
		"from": {time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)}, "to": {time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)}}
	api(t, "GET", "/api/reports/events?"+q.Encode(), nil, &out)
	return out
}

// waitAlarm waits for an alarm event with this alarm value.
func waitAlarm(t *testing.T, id int64, alarm string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		for _, e := range alarms(t, id) {
			if e.Attributes["alarm"] == alarm {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q alarm event for device %d; events %+v", alarm, id, alarms(t, id))
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// sent is one position a simulator sent.
type sent struct {
	Lat, Lon float64
	KmH      float64
	Course   float64
}

// checkPositions compares what Traccar stored with what was sent, in order:
// coordinates within tol degrees, speed within 2 km/h, course within 2
// degrees (when sent).
func checkPositions(t *testing.T, got []position, want []sent, tol float64) {
	t.Helper()
	if len(got) < len(want) {
		t.Fatalf("Traccar stored %d positions, %d sent", len(got), len(want))
	}
	got = got[len(got)-len(want):]
	for i, w := range want {
		g := got[i]
		if math.Abs(g.Latitude-w.Lat) > tol || math.Abs(g.Longitude-w.Lon) > tol {
			t.Errorf("position %d: Traccar has %.6f,%.6f, sent %.6f,%.6f", i, g.Latitude, g.Longitude, w.Lat, w.Lon)
		}
		if kmh := g.Speed * 1.852; w.KmH >= 0 && math.Abs(kmh-w.KmH) > 2 {
			t.Errorf("position %d: Traccar has %.1f km/h, sent %.1f", i, kmh, w.KmH)
		}
		if w.Course >= 0 && math.Abs(g.Course-w.Course) > 2 {
			t.Errorf("position %d: Traccar has course %.0f, sent %.0f", i, g.Course, w.Course)
		}
	}
}

// waitOnline waits until Traccar shows the device online (it has a live
// session, so commands go out at once rather than queue).
func waitOnline(t *testing.T, id int64) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var list []struct {
			Status string `json:"status"`
		}
		api(t, "GET", "/api/devices?id="+strconv.FormatInt(id, 10), nil, &list)
		if len(list) == 1 && list[0].Status == "online" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("device %d is not online in Traccar", id)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// send sends a command through Traccar's API; it must go out at once (200),
// not be queued (202).
func send(t *testing.T, id int64, typ string, attrs map[string]any) {
	t.Helper()
	if attrs == nil {
		attrs = map[string]any{}
	}
	st := api(t, "POST", "/api/commands/send", map[string]any{"deviceId": id, "type": typ, "attributes": attrs}, nil)
	if st != 200 {
		t.Fatalf("command %s to device %d: status %d (queued, not sent)", typ, id, st)
	}
}

// tap is a TCP proxy between a simulator and Traccar that keeps every byte
// each side sent, so a test sees the exact command frames (down) and can
// give our codec the same session (up).
type tap struct {
	ln       net.Listener
	mu       sync.Mutex
	down, up []byte
}

// upstream is what the device has sent so far.
func (p *tap) upstream() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.up...)
}

func newTap(t *testing.T, upstream string) *tap {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &tap{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			u, err := net.Dial("tcp", upstream)
			if err != nil {
				_ = c.Close()
				continue
			}
			go func() {
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						p.mu.Lock()
						p.up = append(p.up, buf[:n]...)
						p.mu.Unlock()
						if _, err := u.Write(buf[:n]); err != nil {
							break
						}
					}
					if err != nil {
						break
					}
				}
				_ = u.Close()
			}()
			go func() {
				buf := make([]byte, 4096)
				for {
					n, err := u.Read(buf)
					if n > 0 {
						p.mu.Lock()
						p.down = append(p.down, buf[:n]...)
						p.mu.Unlock()
						if _, err := c.Write(buf[:n]); err != nil {
							break
						}
					}
					if err != nil {
						break
					}
				}
				_ = c.Close()
			}()
		}
	}()
	return p
}

func (p *tap) addr() string { return p.ln.Addr().String() }

// mark is how many bytes Traccar has sent so far.
func (p *tap) mark() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.down)
}

// since waits until Traccar has sent more than mark bytes and then for a
// quiet 300 ms, and returns what came after mark.
func (p *tap) since(t *testing.T, mark int) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	last := -1
	for {
		p.mu.Lock()
		n := len(p.down)
		p.mu.Unlock()
		if n > mark && n == last {
			p.mu.Lock()
			defer p.mu.Unlock()
			return append([]byte(nil), p.down[mark:]...)
		}
		if time.Now().After(deadline) {
			t.Fatalf("Traccar sent nothing to the device")
		}
		last = n
		time.Sleep(300 * time.Millisecond)
	}
}
