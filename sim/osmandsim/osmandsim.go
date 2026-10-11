// Package osmandsim is a phone running a tracking app that speaks the
// OsmAnd HTTP protocol, and a Traccar server forwarding positions, for
// tests and demos. Like the apps it sends one HTTP request per position
// and reads the server's answer; the connection is kept alive between
// requests unless the server (or KeepAlive false) closes it, and a closed
// connection is dialled again for the next request, as an HTTP client
// does.
//
// Request forms, after the public sources cited in package osmand:
//   - Send: Traccar Client 9 (traccar-client-sdk HttpUploader): a POST of
//     form parameters id, lat, lon, timestamp (Unix seconds), accuracy,
//     altitude, speed (knots), bearing, batt (percent), charge, alarm.
//   - SendQuery: the classic GET with the parameters in the query string
//     (OsmAnd's online tracking and older apps).
//   - SendJSON: the older Traccar Client's JSON body (speed in m/s,
//     battery level 0..1).
//   - Forward: a Traccar server's forward.type json POST.
package osmandsim

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Position is one report of the phone.
type Position struct {
	Time       time.Time
	Lat, Lon   float64
	SpeedKnots float64
	Bearing    float64
	Altitude   float64
	Accuracy   float64
	Battery    int // percent; 0 leaves it out
	Charging   bool
	Alarm      string // "sos" for the SOS button
}

// Phone is one app on one phone.
type Phone struct {
	Addr string
	// ID is the app's device identifier.
	ID string
	// Token, when set, is sent as the token parameter (Bearer when
	// BearerToken is set).
	Token       string
	BearerToken bool
	// TLS dials HTTPS with this configuration (nil: plain HTTP).
	TLS *tls.Config
	// KeepAlive keeps the connection between requests (default true from
	// Dial).
	KeepAlive bool

	mu   sync.Mutex
	conn net.Conn
	br   *bufio.Reader
}

// Dial returns a phone for the server at addr; the connection is made by
// the first request.
func Dial(addr, id string) (*Phone, error) {
	if addr == "" || id == "" {
		return nil, fmt.Errorf("osmandsim: an address and a device id are needed")
	}
	return &Phone{Addr: addr, ID: id, KeepAlive: true}, nil
}

// Close closes the connection.
func (p *Phone) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return nil
	}
	err := p.conn.Close()
	p.conn, p.br = nil, nil
	return err
}

func f(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func (p *Phone) params(pos Position) url.Values {
	v := url.Values{}
	v.Set("id", p.ID)
	v.Set("lat", f(pos.Lat))
	v.Set("lon", f(pos.Lon))
	t := pos.Time
	if t.IsZero() {
		t = time.Now()
	}
	v.Set("timestamp", strconv.FormatInt(t.Unix(), 10))
	if pos.Accuracy > 0 {
		v.Set("accuracy", f(pos.Accuracy))
	}
	v.Set("altitude", f(pos.Altitude))
	v.Set("speed", f(pos.SpeedKnots))
	v.Set("bearing", f(pos.Bearing))
	if pos.Battery > 0 {
		v.Set("batt", strconv.Itoa(pos.Battery))
	}
	v.Set("charge", strconv.FormatBool(pos.Charging))
	if pos.Alarm != "" {
		v.Set("alarm", pos.Alarm)
	}
	if p.Token != "" && !p.BearerToken {
		v.Set("token", p.Token)
	}
	return v
}

func (p *Phone) headers() string {
	h := "Host: " + p.Addr + "\r\nUser-Agent: osmandsim\r\n"
	if p.Token != "" && p.BearerToken {
		h += "Authorization: Bearer " + p.Token + "\r\n"
	}
	if !p.KeepAlive {
		h += "Connection: close\r\n"
	}
	return h
}

// Send posts a position as form parameters, as Traccar Client 9 does; it
// returns the server's status code.
func (p *Phone) Send(pos Position) (int, error) {
	body := p.params(pos).Encode()
	return p.Raw("POST / HTTP/1.1\r\n" + p.headers() + "Content-Type: application/x-www-form-urlencoded\r\nContent-Length: " +
		strconv.Itoa(len(body)) + "\r\n\r\n" + body)
}

// SendQuery sends a position as a GET with a query string.
func (p *Phone) SendQuery(pos Position) (int, error) {
	return p.Raw("GET /?" + p.params(pos).Encode() + " HTTP/1.1\r\n" + p.headers() + "\r\n")
}

// SendJSON posts a position in the older Traccar Client's JSON form.
func (p *Phone) SendJSON(pos Position) (int, error) {
	t := pos.Time
	if t.IsZero() {
		t = time.Now()
	}
	level := -1.0
	if pos.Battery > 0 {
		level = float64(pos.Battery) / 100
	}
	loc := map[string]any{
		"timestamp": t.UTC().Format("2006-01-02T15:04:05.000Z"), "event": "motionchange", "is_moving": pos.SpeedKnots > 0, "odometer": 0,
		"coords": map[string]any{"latitude": pos.Lat, "longitude": pos.Lon, "accuracy": pos.Accuracy,
			"speed": pos.SpeedKnots / 1.94384, "heading": pos.Bearing, "altitude": pos.Altitude},
		"battery":  map[string]any{"level": level, "is_charging": pos.Charging},
		"activity": map[string]any{"type": "in_vehicle", "confidence": 100},
		"extras":   map[string]any{},
	}
	if pos.Alarm != "" {
		loc["extras"] = map[string]any{"alarm": pos.Alarm}
	}
	b, _ := json.Marshal(map[string]any{"location": loc, "device_id": p.ID})
	path := "/"
	if p.Token != "" && !p.BearerToken {
		path += "?token=" + url.QueryEscape(p.Token)
	}
	return p.Raw("POST " + path + " HTTP/1.1\r\n" + p.headers() + "Content-Type: application/json\r\nContent-Length: " +
		strconv.Itoa(len(b)) + "\r\n\r\n" + string(b))
}

// Raw sends one request as written and returns the answer's status code.
func (p *Phone) Raw(req string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		var c net.Conn
		var err error
		d := &net.Dialer{Timeout: 5 * time.Second}
		if p.TLS != nil {
			c, err = tls.DialWithDialer(d, "tcp", p.Addr, p.TLS)
		} else {
			c, err = d.Dial("tcp", p.Addr)
		}
		if err != nil {
			return 0, err
		}
		p.conn, p.br = c, bufio.NewReader(c)
	}
	_ = p.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(p.conn, req); err != nil {
		p.drop()
		return 0, err
	}
	resp, err := http.ReadResponse(p.br, nil)
	if err != nil {
		p.drop()
		return 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.Close || !p.KeepAlive {
		p.drop()
	}
	return resp.StatusCode, nil
}

func (p *Phone) drop() {
	_ = p.conn.Close()
	p.conn, p.br = nil, nil
}

// ForwardBody is a Traccar server's forward.type json body for one
// position of the device uniqueID (speed in knots); alarm, when set, is
// the position's alarm attribute.
func ForwardBody(uniqueID string, pos Position) []byte {
	t := pos.Time
	if t.IsZero() {
		t = time.Now()
	}
	ts := t.UTC().Format("2006-01-02T15:04:05.000+00:00")
	attrs := map[string]any{"batteryLevel": float64(pos.Battery), "motion": pos.SpeedKnots > 0, "distance": 0.0, "totalDistance": 1234.5}
	if pos.Alarm != "" {
		attrs["alarm"] = pos.Alarm
	}
	b, _ := json.Marshal(map[string]any{
		"position": map[string]any{"id": 1, "attributes": attrs, "deviceId": 7, "protocol": "osmand", "serverTime": ts,
			"deviceTime": ts, "fixTime": ts, "outdated": false, "valid": true, "latitude": pos.Lat, "longitude": pos.Lon,
			"altitude": pos.Altitude, "speed": pos.SpeedKnots, "course": pos.Bearing, "address": nil, "accuracy": pos.Accuracy,
			"network": nil, "geofenceIds": nil},
		"device": map[string]any{"id": 7, "attributes": map[string]any{}, "groupId": 0, "name": "Vehicle " + uniqueID,
			"uniqueId": uniqueID, "status": "online", "lastUpdate": ts, "positionId": 1, "phone": nil, "model": nil,
			"contact": nil, "category": nil, "disabled": false},
	})
	return b
}

// Forward posts one forwarded position to addr as a Traccar server does
// (header is an optional forward.header line, such as
// "Authorization: Bearer …"), and returns the status code.
func Forward(addr, uniqueID string, pos Position, header string) (int, error) {
	body := ForwardBody(uniqueID, pos)
	h := ""
	if header != "" {
		h = strings.TrimSpace(header) + "\r\n"
	}
	p := &Phone{Addr: addr, ID: uniqueID, KeepAlive: true}
	defer p.Close()
	return p.Raw("POST / HTTP/1.1\r\nHost: " + addr + "\r\n" + h + "Content-Type: application/json\r\nContent-Length: " +
		strconv.Itoa(len(body)) + "\r\n\r\n" + string(body))
}
