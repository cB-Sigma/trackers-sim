# Changelog

## v0.2.0

### Added

- `interop`, a test-only module that runs the simulators against a real
  Traccar server (the traccar/traccar:6.16.0 Docker image, Apache-2.0).
  It runs only with `SIGMA_TRACKERS_INTEROP=1` and Docker.
  - Positions: every protocol's simulator logs in at Traccar's port for
    that protocol and sends three positions, with both hemispheres covered.
    The test reads them back through Traccar's route report and compares
    coordinates, speed and course. Teltonika and H02 also run over UDP.
  - Alarms: an SOS from the GT06, TK103, Queclink, Meitrack, JT808, H02,
    OsmAnd and AIS-140 simulators must arrive as an `sos` alarm event in
    Traccar.
  - Commands: each named or text command is sent through Traccar's REST
    API to a simulator behind a recording proxy. The test compares the
    frame Traccar's encoder wrote with the frame the trackers codec makes
    for the same command in the same session. Each difference is listed in
    the test with its reason. Where the difference comes from a choice of
    command text, the test also checks that a trackers profile can produce
    Traccar's form.

- New simulators: `wialonsim` (Wialon IPS 2.0 and 1.1), `galileoskysim`,
  `suntechsim` (Universal and older protocol), `calampsim` (LM Direct over
  UDP), `atracksim` (binary and ASCII), `eelinksim` and `watchsim`. All
  are registered in `sim.Run` and `trackersim -protocol`.
- `teltonikasim`: sends Codec 16, and Codec 13 and 15 messages. It answers
  in Codec 13/14/15, including the Codec 14 nACK, and handles
  setparam/getparam; `Interval()` reports the set interval.
- `queclinksim`: HEX format with `Tracker.Hex`.
- `jt808sim`:
  - 2011 registration and subpackaged batches.
  - Reboot and parameter answers (`Reboots`, `Params`, `Interval`).
  - ADAS/DSM alarm locations (`SafetyAlarm`).
  - JT/T 1078 answers, and the RSA key exchange with encrypted bodies.
- `gt06sim`: time request (0x8A), information frames (0x94), the 0x23
  heartbeat, 4G location (0xA0) and 4G cells (0xA1).
- `h02sim`: VP1, VP1 and V3 cells, ALRM, BC history, 45-byte and Y-model
  records.
- `meitracksim`: pictures (D00 in eights, D01, D03, event 39) and CCC
  batches.
- `ruptelasim`: version and file answers, trouble codes, connection and
  odometer commands.
- `ais140sim`: the RLP and CTPL data-packet layouts (`NormalVendor`).
- `wialonsim`: short data takes `#AD#1` as well as `#ASD#1`, because
  Traccar 6.16.0 answers short data that way.

### Not yet

- SMS delivery, and GNSS behaviour beyond a steady circle with fixed gaps.
