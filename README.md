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
| `sim/gt06sim` | GT06 / Concox tracker: login, 0x22 location, 0x13 status, 0x16 alarm, cell (LBS) and WiFi frames, 0x8A time request, 0x94 information (external voltage, status text, door, IMSI and ICCID), 0x23 heartbeat, 0xA0 4G location, 0xA1 4G cells; waits for acknowledgements; answers 0x80 commands |
| `sim/teltonikasim` | Teltonika tracker: IMEI handshake; Codec 8, 8E and 16 AVL packets over TCP or UDP; Codec 13 and 15 messages; Codec 12 answers (or 13/15); Codec 14 ACK, and the nACK when the IMEI does not match; setparam/getparam, with the interval taken from parameter 10050 |
| `sim/tk103sim` | TK103 / Coban (GPS103) tracker: login, heartbeat, position reports with keywords; records commands |
| `sim/queclinksim` | Queclink @Track tracker (GV300 family, long connection), ASCII or HEX (`Hex`): heartbeats, +RESP/+BUFF or +RSP/+EVT reports, acknowledgements carrying the command's serial |
| `sim/meitracksim` | Meitrack tracker: AAA event reports, CCE and CCC batches, pictures (D00 answered in eights, D01, D03; event 39 announces one), answers to "@@" commands with the same data identifier |
| `sim/ruptelasim` | Ruptela FM tracker: identification, records and extended records, heartbeats, trouble codes; answers SMS commands over GPRS, set-IO, version (3) and file commands (37); records connection and odometer commands |
| `sim/jt808sim` | JT/T 808 terminal (Jimi-like): registration (2011, 2013 or 2019 layout), authentication, heartbeats, locations and batches (also in subpackages), ADAS/DSM alarm locations (0x64/0x65); answers locate, vehicle control, text, reboot (0x8105) and parameter (0x8103) commands, JT/T 1078 queries (0x1003, 0x1205) and the RSA key exchange with encrypted bodies |
| `sim/h02sim` | H02 tracker (Sinotrack ST-901 class): V1 and VP1 positions, VP1 and V3 cells, NBR, ALRM, BC history, heartbeats, 32- and 45-byte binary records, Cantrack Y-model records; V4 answers to commands; TCP or UDP |
| `sim/wialonsim` | Wialon IPS 2.0 or 1.1 tracker: login, ping, short and extended data, black box, driver messages, pictures; answers custom commands with a message, records driver messages and uploads |
| `sim/galileoskysim` | Galileosky terminal: head packet, records with the main tags, waits for each confirmation; answers command packets with the same number |
| `sim/suntechsim` | Suntech tracker, Universal or older (ST300/SA200) protocol: STT, ALT, EMG, ALV; answers output, locate and reboot commands with RES/CMD;Res |
| `sim/calampsim` | CalAmp LMU over UDP (LM Direct): event and mini event reports waiting for the server's ACK; acknowledges user data |
| `sim/atracksim` | ATrack tracker: keep-alive, binary or ASCII positions, waits for the answer; answers AT$ commands with $OK and the tag, $INFO, and a position for AT$GPOS |
| `sim/eelinksim` | Eelink 2.0 tracker: login, heartbeat, location, warning; answers 0x80 instructions with the UID (relay, reset, collect, WHERE) |
| `sim/watchsim` | 3G/SeTracker watch: LK, UD positions with a cell, AL alarms; answers commands by repeating the keyword |
| `sim/osmandsim` | A phone running a tracking app on the OsmAnd HTTP protocol (form POST, query GET, older JSON body, SOS), and a Traccar server forwarding positions |
| `sim/ais140sim` | AIS-140 device: login, data, health, emergency and emergency SMS packets with XOR checksums; Table 4D OTA commands with the device password; activation and health check answers; TCP, TLS or UDP; a second (control centre) backend that records what devices forward to it |
| `sim` | `Run` drives one simulated tracker of any of these protocols: connect, log in, report every interval while the vehicle drives a slow circle, report the commands received |
| `cmd/trackersim` | Runs `sim.Run` from the command line |

| `interop` | A test-only module (not published with this one): every simulator against a real Traccar server in Docker (see Interop) |

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
| `-protocol` | `gt06` | ais140, atrack, calamp, eelink, galileosky, gt06, h02, jt808, meitrack, osmand, queclink, ruptela, suntech, teltonika, tk103, watch or wialon |
| `-server` | `127.0.0.1:5024` | server address, host:port (UDP for calamp) |
| `-imei` | `359710049099999` | device IMEI; the terminal phone number for jt808 (its last 12 digits); the device ID for h02, osmand and suntech; the mobile ID for calamp; the watch ID for watch (its last 10 digits) |
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

## Interop

The `interop` module checks the simulators against an independent
implementation: the Traccar server (traccar/traccar:6.16.0, Apache-2.0)
in Docker. It is skipped unless asked for:

```
cd interop
SIGMA_TRACKERS_INTEROP=1 go test ./...
```

It starts the image with every protocol's port published, makes an
administrator, and removes the container at the end.
`SIGMA_TRACKERS_TRACCAR_CONTAINER=<name>` uses a running container instead.

| Check | How | Result (2026-10-12) |
|---|---|---|
| Positions, every protocol | three positions in both hemispheres, read back through Traccar's route report; TCP, and UDP for Teltonika, H02 and CalAmp; Wialon 1.1 and 2.0, ATrack binary and ASCII, Suntech Universal and ST300 | all arrive with the coordinates, speed and course sent |
| Alarms | an SOS from GT06, TK103, Queclink, Meitrack, JT808, H02, OsmAnd, AIS-140 and Suntech | each arrives as an `sos` alarm event |
| Command frames | every command Traccar can send for these protocols (16 sessions), captured through a recording proxy and compared with the trackers codec's frame for the same command in the same session | equal, or a difference listed in the test with its reason; where the difference is a choice of text, a trackers profile gives Traccar's form, and the test checks it |

Two findings about Traccar 6.16.0 itself, written into the test so a fix
shows up as a change:

- Its AIS-140 ("its") encoder cannot send: the API refuses every command
  with "unsupported message type".
- It answers Wialon IPS 2.0 short data (`#SD#`) with `#AD#1`, where the
  protocol document gives `#ASD#1`. wialonsim takes either.

The trackers README lists each command difference.

## Not yet

| Item | Why |
|---|---|
| SMS delivery | a phone network's job; the AIS-140 SMS texts are built, not sent |
| GNSS behaviour beyond a steady circle (multipath, fix loss, jumps) | not done yet |
| Checking the simulators' firmware answers against real devices | needs hardware |

## Related repositories

- [trackers](https://github.com/cB-Sigma/trackers): the protocol codecs.

## Status

v0.2.0 (see [CHANGELOG.md](CHANGELOG.md)). It keeps the v0.1.0 API and
adds to it. The API may change before v1.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md).

## Licence

Apache-2.0 (see [LICENSE](LICENSE)). Copyright 2026 Abhinav Gautam.
[NOTICE](NOTICE) credits the third-party test data and names the
trademarks used.
