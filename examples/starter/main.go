// Command starter is the smallest program that runs gobit with something of its
// own added.
//
// It is what a customer project's main.go looks like: a dependency in go.mod, a
// module the project wrote, a plugin it compiled in, and one call. Everything
// gobit ships — sixteen commerce modules, their migrations, their routes, the
// admin panel and the operator subcommands — comes with that call.
//
// # What the accounts package is for
//
// Self-registration needs two things gobit will not guess: which record a
// customer is, and how a message reaches an address. The starter answers both in
// its own accounts package, which is what a real project does — and a project
// with its own users table answers them differently and the flow works the same.
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
	"strings"

	"github.com/bdrtr/gobit"
	identitysession "github.com/bdrtr/gobit/contrib/identity-session"

	"example.com/gobit-starter/accounts"
	"example.com/gobit-starter/loyalty"
)

// version is filled in at build time with -ldflags, the same way gobit's own
// binary does it.
var version = "dev"

func main() {
	// The accounts adapter is a module AND a seam: it is added so that gobit hands
	// it the container, and passed to the session module so that self-registration
	// has somebody to ask about customers. One value, two roles, because the thing
	// that needs the container is the thing that answers the questions.
	shopAccounts := accounts.New(nil)

	shop := gobit.New().
		Version(version).
		Add(loyalty.New()).
		// The secret comes from the environment and has no default, which is the
		// module's own rule: a generated one would log every shopper out on each
		// deploy, and a constant one in source would let anybody mint a session
		// for any customer of every installation that never changed it.
		//
		// SESSION_SECRET_RETIRED is how a rotation is done without logging
		// anybody out (ADR 0129): move the old key there, put a new one in
		// SESSION_SECRET, deploy. One TTL later the old key can be dropped. A
		// key that LEAKED is dropped outright instead, which does log everybody
		// out and is the correct price.
		Add(shopAccounts).
		// Binding Accounts and Verification is what MOUNTS storefront
		// self-registration (ADR 0133). Without them the module still signs people
		// in and an operator still writes credentials; what is absent is the pair of
		// endpoints a shopper uses to open an account, and absent is the only honest
		// state for a flow that cannot finish.
		//
		// The verification here writes the link to the LOG. It is named
		// LogOnlyVerification and warns on every send, because a real shop sends
		// mail with its own client — and a deployment that keeps this one is putting
		// sign-up links into its log files.
		Add(identitysession.New(identitysession.Options{
			Secret:         []byte(os.Getenv("SESSION_SECRET")),
			RetiredSecrets: retiredSecrets(os.Getenv("SESSION_SECRET_RETIRED")),
			Accounts:       shopAccounts,
			Verification:   accounts.NewLogOnlyVerification(nil),
		}))

	if err := shop.Main(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

// retiredSecrets reads the keys a rotation left behind, comma separated.
//
// An empty variable means no rotation is in flight, which is the ordinary state
// and has to cost nothing: a list with one empty entry would be refused at
// startup, correctly, and an installation that never rotated would never start.
func retiredSecrets(list string) [][]byte {
	var out [][]byte
	for _, key := range strings.Split(list, ",") {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			out = append(out, []byte(trimmed))
		}
	}

	return out
}
