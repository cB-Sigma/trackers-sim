# Provenance

## How the code was written

Written for SigmaM2M, a oneM2M platform, in Go, by the author of the
trackers module and from the same documents as its codecs. No code was
ported or copied from another implementation. The per-protocol
simulators were first written next to SigmaM2M's tracker connectors, to
test them without devices, each with its protocol's codec.
The `sim` runner, `tk103sim` and `cmd/trackersim` were written on
2026-10-06 when the protocol packages became the trackers module, and
the simulators moved into this separate module on 2026-10-11 so the
library does not depend on them. The tests of each per-protocol
simulator against the library's codec (`sim/<protocol>sim/*_test.go`,
`sim/internal/codectest`) were added then.

## Sources

Each simulator follows the documents its protocol's codec follows (the
trackers module's PROVENANCE.md lists them by title and version); its
package comment names the parts it imitates.

| Package | Followed |
|---|---|
| `gt06sim` | Concox GT06 protocol tables (login, location, status, alarm, LBS, WiFi, 0x80 commands and answers) |
| `teltonikasim` | Teltonika's protocol description: IMEI handshake, codecs 8 and 8E over TCP and UDP, codec 12 answers |
| `tk103sim` | The GPS103 (Coban) protocol document |
| `queclinksim` | Queclink GV300N @Track Air Interface Protocol V3.01 and GV300 V2.40 |
| `meitracksim` | Meitrack GPRS Protocol V1.8 and the GPRS (CCE ID) Protocol V1.0 |
| `ruptelasim` | Ruptela Protocol v1.113 and the Ruptela SMS command list |
| `jt808sim` | JT/T 808-2013 (English translation) and the 2019 header; Jimi IoT text answers |
| `h02sim` | HuaSunTeK GPS Tracker Communication Protocol V1.0.5 and Cantrack protocol A/1 |
| `osmandsim` | traccar.org/osmand and traccar.org/forward; Traccar Client and its SDK, read for the request forms (no code taken) |
| `ais140sim` | AIS-140 (ARAI) with Amendments 1 to 3: the login, data, health and emergency packet layouts, Table 4D commands and responses, the activation and health check answers (Amendment 2, clause 8.4) |

No specification or manual text is reproduced; comments describe
behaviour in our own words with clause, table or section numbers.

## Test data and demo values

All identities, passwords, addresses and positions are demo values. Some
default IDs come from public test data and are kept because tests in
SigmaM2M's server and console use the same values:

- `jt808sim` sends the terminal ID 866496077582164 in its 2019
  authentication, a value from Traccar's JT808 decoder tests
  (Apache-2.0).
- The `tk103sim` test uses the IMEI 359586015829802 from Traccar's GPS103
  decoder tests (Apache-2.0).
- The `cmd/trackersim` documentation uses 861693034634154, an IMEI from
  the AIS-140 packets of Traccar's decoder tests (Apache-2.0).
- `cmd/trackersim`'s default IMEI 359710049099999 is a demo value.

`ais140sim` logs in with a made-up ICCID (89910000000000000012). No
packet or value of the AIS-140 standard's samples is used. The tests in
this module use made-up values (IMEIs 86000000000xxxx, H02 ID
4200000011, phone-demo-1).
