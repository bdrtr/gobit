// Package e2e holds the end-to-end system tests that run against the real modules.
//
// EVERY test in the package is tagged `//go:build integration`: they require a real
// PostgreSQL instance (and therefore Docker). To run them:
// make test-integration
//
// This file is deliberately UNTAGGED and contains no production code. The reason is
// technical: if every file in the package were tagged, not a single file would be
// left that compiles without the tag, and repository-wide commands such as
// `go vet ./...`, `go test ./...` and `golangci-lint run ./...` would fail for this
// package with "build constraints exclude all Go files".
//
// # Why this does not live under internal/workflows
//
// The tests here wire up the REAL modules, which means they import packages under
// internal/modules. ADR 0006 forbids a module import in every file under
// internal/workflows and internal/arch enforces that; the system tests must stand
// OUTSIDE that scope. Their whole point is to prove that the modules and the
// workflows work together.
package e2e
