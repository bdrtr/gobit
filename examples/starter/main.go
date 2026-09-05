// Command starter is the smallest program that runs gobit with something of its
// own added.
//
// It is what a customer project's main.go looks like: a dependency in go.mod, a
// module the project wrote, a plugin it compiled in, and one call. Everything
// gobit ships — sixteen commerce modules, their migrations, their routes, the
// admin panel and the operator subcommands — comes with that call.
package main

import (
	"fmt"
	"os"

	"github.com/bdrtr/gobit"

	"example.com/gobit-starter/loyalty"
)

// version is filled in at build time with -ldflags, the same way gobit's own
// binary does it.
var version = "dev"

func main() {
	shop := gobit.New().
		Version(version).
		Add(loyalty.New())

	if err := shop.Main(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
