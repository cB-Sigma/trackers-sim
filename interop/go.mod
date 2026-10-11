// Test-only module: the simulators of github.com/cB-Sigma/trackers-sim
// against a real Traccar server in Docker, and the command frames of
// github.com/cB-Sigma/trackers against Traccar's encoders (see doc.go).
// Nothing imports it; it is not part of the published module.
module github.com/cB-Sigma/trackers-sim/interop

go 1.24.0

replace github.com/cB-Sigma/trackers-sim => ../

require (
	github.com/cB-Sigma/trackers v0.2.0
	github.com/cB-Sigma/trackers-sim v0.0.0
)
