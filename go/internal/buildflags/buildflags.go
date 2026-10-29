// Package buildflags holds compile-time flags stamped into a binary with -ldflags.
//
// DEMO-ONLY: it exists for the Worker Versioning demo (docs/RUNBOOK-DEMO.md, scenario F). The worker-v2 image
// is built with DEMO_BUG=panic-before-authorize to simulate shipping a workflow bug. Because the value
// is fixed at compile time, it is identical on every replay of a given binary, so reading it from
// workflow code is deterministic. Default builds leave it empty.
package buildflags

// DemoBug is set with: go build -ldflags "-X bulkwire/internal/buildflags.DemoBug=panic-before-authorize".
var DemoBug = ""
