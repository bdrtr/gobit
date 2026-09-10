// Command starter is the smallest program that runs gobit with something of its
// own added.
//
// It is what a customer project's main.go looks like: a dependency in go.mod, a
// module the project wrote, a plugin it compiled in, and one call. Everything
// gobit ships — sixteen commerce modules, their migrations, their routes, the
// admin panel and the operator subcommands — comes with that call.
//
// # Why the session module is here
//
// Since ADR 0125 every storefront route naming a customer refuses until a
// verifier is bound, and gobit issues none (ADR 0008). Adding
// contrib/identity-session is what makes those routes work, and it is one line —
// which is the sentence ADR 0127 exists to make true. Without it this starter
// serves guests and nothing else, which is a correct shop and not a whole one.
package main

import (
	"fmt"
	"os"

	"github.com/bdrtr/gobit"
	identitysession "github.com/bdrtr/gobit/contrib/identity-session"

	"example.com/gobit-starter/loyalty"
)

// version is filled in at build time with -ldflags, the same way gobit's own
// binary does it.
var version = "dev"

func main() {
	shop := gobit.New().
		Version(version).
		Add(loyalty.New()).
		// The secret comes from the environment and has no default, which is the
		// module's own rule: a generated one would log every shopper out on each
		// deploy, and a constant one in source would let anybody mint a session
		// for any customer of every installation that never changed it.
		Add(identitysession.New(identitysession.Options{
			Secret: []byte(os.Getenv("SESSION_SECRET")),
		}))

	if err := shop.Main(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
