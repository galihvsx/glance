// Command glance version stamp.
//
// Version is the release version of the glance binary, reported by
// /health. It defaults to "dev" for local builds and is stamped at
// release-build time:
//
//	go build -ldflags "-X main.Version=v0.1.0" ./cmd/glance
//
// (The Dockerfile passes its VERSION build arg through the same way.)
package main

// Version of this binary. Overridden via -ldflags at build time.
var Version = "dev"
