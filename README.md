# trackers-sim

Simulated GPS vehicle trackers and AIS-140 devices in Go, for testing tracker servers without hardware.

[![Go Reference](https://pkg.go.dev/badge/github.com/cB-Sigma/trackers-sim.svg)](https://pkg.go.dev/github.com/cB-Sigma/trackers-sim) [![test](https://github.com/cB-Sigma/trackers-sim/actions/workflows/test.yml/badge.svg)](https://github.com/cB-Sigma/trackers-sim/actions/workflows/test.yml) [![Licence: Apache-2.0](https://img.shields.io/badge/licence-Apache--2.0-blue.svg)](LICENSE) [![Go](https://img.shields.io/badge/go-1.24.0-00ADD8.svg)](go.mod)

Each simulator connects to a server, logs in the way its protocol does,
sends positions, waits for the server's acknowledgements and answers
commands as the device does. One runner drives any of them, and one
command runs a simulated tracker from the shell. The frames are built
for the codecs of [trackers](https://github.com/cB-Sigma/trackers), and
the tests decode them with those codecs. Written for SigmaM2M, a oneM2M
platform; it has no dependency on it.

## What is simulated

| Package | Device |
|---|---|
| `sim/gt06sim` | GT06 / Concox tracker: login, location, status, alarm, cell (LBS) and WiFi frames; waits for acknowledgements; answers 0x80 commands |
| `sim/teltonikasim` | Teltonika tracker: IMEI handshake, Codec 8 and 8E AVL packets over TCP or UDP, Codec 12 command answers |
| `sim/tk103sim` | TK103 / Coban (GPS103) tracker: login, heartbeat, position reports with keywords; records commands |
| `sim/queclinksim` | Queclink @Track ASCII tracker (GV300 family, long connection): heartbeats, +RESP and +BUFF reports, +ACK answers carrying the command's serial |
| `sim/meitracksim` | Meitrack tracker: AAA event reports, CCE batches, answers to "@@" commands with the same data identifier |
| `sim/ruptelasim` | Ruptela FM tracker: identification, records and extended records, heartbeats, trouble codes; answers SMS commands over GPRS and set-IO commands |
| `sim/jt808sim` | JT/T 808 terminal (Jimi-like): registration, authentication, heartbeats, locations and batches, 2013 or 2019 header; answers locate, vehicle control and text commands |
| `sim/h02sim` | H02 tracker (Sinotrack ST-901 class): V1 text and 32-byte binary positions, heartbeats, cells; V4 answers to commands; TCP or UDP |
| `sim/osmandsim` | A phone running a tracking app on the OsmAnd HTTP protocol (form POST, query GET, older JSON body, SOS), and a Traccar server forwarding positions |
| `sim/ais140sim` | AIS-140 device: login, data, health, emergency and emergency SMS packets with XOR checksums; Table 4D OTA commands with the device password; activation and health check answers; TCP, TLS or UDP; a second (control centre) backend that records what devices forward to it |
| `sim` | `Run` drives one simulated tracker of any of these protocols: connect, log in, report every interval while the vehicle drives a slow circle, report the commands received |
| `cmd/trackersim` | Runs `sim.Run` from the command line |

Not simulated: SMS delivery, GNSS behaviour beyond a steady circle, and
device firmware quirks other than the ones the package comments name.

## Install

```
go get github.com/cB-Sigma/trackers-sim@latest
```

Requires Go 1.24.0 or later. The command:

```
go install github.com/cB-Sigma/trackers-sim/cmd/trackersim@latest
```

## Command line

```
trackersim -list
trackersim -protocol gt06 -server 127.0.0.1:5024 -imei 359710049099999 -every 5s
trackersim -protocol ais140 -server 127.0.0.1:5023 -imei 861693034634154 -password 1234
trackersim -protocol osmand -server 127.0.0.1:5055 -imei phone-1 -count 10
```

| Flag | Default | Meaning |
|---|---|---|
| `-list` | | list the protocols and exit |
| `-protocol` | `gt06` | ais140, gt06, h02, jt808, meitrack, osmand, queclink, ruptela, teltonika or tk103 |
| `-server` | `127.0.0.1:5024` | server address, host:port |
| `-imei` | `359710049099999` | device IMEI; the terminal phone number for jt808 (its last 12 digits); the device ID for h02 and osmand |
| `-password` | | the device's command password (ais140, queclink) |
| `-every` | `10s` | reporting interval |
| `-count` | `0` | stop after this many reports (0: until interrupted) |
| `-lat`, `-lon` | `12.9716`, `77.5946` | start position |
| `-speed` | `40` | speed in km/h |
| `-heading` | `0` | start heading in degrees |

Output of a GT06 run against a server that acknowledges and sends one
locate command:

```
$ trackersim -protocol gt06 -server 127.0.0.1:5024 -every 1s -count 3
gt06 tracker 359710049099999 on 127.0.0.1:5024
2026-10-11T08:58:03Z report 12.971600,77.594600 40 km/h 0°
2026-10-11T08:58:04Z report 12.971700,77.594600 40 km/h 0°
2026-10-11T08:58:04Z command WHERE#
2026-10-11T08:58:05Z report 12.971800,77.594600 40 km/h 0°
```

## In Go tests

Run a simulated tracker against your server (from `sim.Example`, where
the server is a few lines on the gt06 codec of trackers):

```go
n := 0
err := sim.Run(context.Background(), sim.Config{Protocol: "gt06", Server: ln.Addr().String(), IMEI: "359710049099999",
	Every: 50 * time.Millisecond, Count: 3, OnReport: func(sim.Pos) { n++ }})
fmt.Println(n, err)
// Output: 3 <nil>
```

For finer control, use a protocol's simulator directly (from the
`tk103sim` test):

```go
tr, err := tk103sim.Dial(addr, "359586015829802")
if err != nil {
	t.Fatal(err)
}
defer tr.Close()
if err := tr.Login(); err != nil {
	t.Fatal(err)
}
at := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
err = tr.Report("help me", tk103sim.Pos{Time: at, Lat: 12.9716, Lon: 77.5946, Speed: 36, Heading: 90, Valid: true})
// tr.Commands() lists the commands the server sent.
```

## Demo values

Every IMEI, ICCID, phone number, password, address and position in this
module is a demo value; some come from public test data (see
PROVENANCE.md). Do not use them for real devices.

## Documentation

Reference: https://pkg.go.dev/github.com/cB-Sigma/trackers-sim

## Testing

```
go vet ./...
go test -race ./...
```

`sim` runs every protocol's simulator against the codec of trackers;
each per-protocol package has a test of its frames against that codec.
The simulators are written from the same documents as the codecs, so
these tests show the two agree, not that either matches a real device.

## Related repositories

- [trackers](https://github.com/cB-Sigma/trackers): the protocol codecs.

## Status

v0.x. The API may change before v1.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md).

## Licence

Apache-2.0 (see [LICENSE](LICENSE)). Copyright 2026 Abhinav Gautam.
[NOTICE](NOTICE) credits the third-party test data and names the
trademarks used.
