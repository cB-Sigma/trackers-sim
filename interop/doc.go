// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Package interop runs the simulated trackers of github.com/cB-Sigma/trackers-sim
// against a real Traccar server (the traccar/traccar Docker image,
// Apache-2.0), an independent implementation of the same protocols:
//
//   - every simulator logs in at Traccar's port for its protocol, sends
//     positions and alarms, and the test reads them back through Traccar's
//     REST API (route report, events) and compares them with what was sent;
//   - commands are sent through Traccar's REST API, and the bytes Traccar's
//     encoder wrote are compared with the frame the trackers codec makes for
//     the same command. Where the two differ on purpose (a different
//     command text for the same device action), the difference is listed in
//     the test with the reason, so a new difference fails the test.
//
// The tests run only with SIGMA_TRACKERS_INTEROP=1 and docker on PATH. They
// start the image named by traccarImage and remove the container at the
// end; SIGMA_TRACKERS_TRACCAR_CONTAINER names an already running container
// to use instead (its API on 8082 and the protocol ports published).
package interop
