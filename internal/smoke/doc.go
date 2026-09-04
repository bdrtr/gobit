// Package smoke boots the application as a REAL process and exercises its behavior.
//
// EVERY test in the package is tagged `//go:build smoke`: it compiles the binary,
// brings up Postgres and Redis with testcontainers and starts real processes.
// To run them: make smoke
//
// This file is deliberately UNTAGGED and holds no production code. The reason is
// technical: if every file in the package were tagged, not a single file would be
// left that compiles without the tag, and repository-wide commands such as
// `go vet ./...`, `go test ./...` and `golangci-lint run ./...` would fail for this
// package with "build constraints exclude all Go files". The same remedy lives in
// internal/e2e/doc.go.
//
// # Why it was not folded into the integration tag
//
// Every scenario here runs a binary compiled with `go build` and waits for the
// whole startup (migrations included). Folding it into the integration tag would
// have meant that the hundreds of tests which never start a process pay that cost
// on every run as well; hence the separate tag, the separate Makefile target and
// the separate CI job.
package smoke
