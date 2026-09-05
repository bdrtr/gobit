package http

import (
	"net/http"
	"net/http/pprof"
)

// ProfilingHandler serves the Go runtime profiles under /debug/pprof/.
//
// # Why the routes are registered here rather than by importing for the side effect
//
// The usual way to switch pprof on is a blank import, which registers the
// endpoints on [http.DefaultServeMux] — a package-level global that anything in
// the process can end up serving. That makes the profiles reachable from
// wherever the default mux is mounted rather than from the listener that asked
// for them. An explicit mux keeps them where they were put, and an arch test
// keeps the default mux from being served at all.
//
// # What these endpoints give away
//
// A heap profile carries the CONTENTS of live memory: a token in flight, a
// customer's order, a password on its way to be hashed. The goroutine dump
// carries every stack, and /debug/pprof/cmdline prints the command line, which
// in some deployments holds every secret the process has.
//
// That is the whole reason the listener this handler goes on is separate, is
// off unless an address is configured, and is refused a routable address in a
// shared environment (see config.Config.ProfilingAddr).
func ProfilingHandler() http.Handler {
	mux := http.NewServeMux()

	// Index serves the NAMED profiles itself — heap, goroutine, allocs, block,
	// mutex, threadcreate — by reading the last path segment. The four routes
	// after it are the ones it does not cover.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return mux
}
