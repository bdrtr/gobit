// Command server is the gobit binary as it ships.
//
// It is deliberately the SMALLEST program that can run gobit, because it is
// also the example an embedding project copies: anything this file were allowed
// to do that an outside program cannot do would be a lie about what the library
// offers. The lifecycle it starts lives behind
// [github.com/bdrtr/gobit.App] (ADR 0027).
package main

import (
	"fmt"
	"os"

	"github.com/bdrtr/gobit"
)

// version is filled in at build time with -ldflags (see the Makefile).
var version = "dev"

func main() {
	if err := gobit.New().Version(version).Main(os.Args[1:], os.Stdout); err != nil {
		// The error must be visible even when the logger was never built.
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
