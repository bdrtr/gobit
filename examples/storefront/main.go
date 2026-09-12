// Command storefront runs gobit with a guest shop in front of it.
//
// It is examples/starter's sibling and answers a different question. The starter
// shows what a project's main.go looks like when it adds a module and a plugin;
// this one shows that a BROWSER can shop against gobit without a second process,
// a second origin or a build step.
//
// # Running it
//
// The shop needs two values the operator mints, and neither is readable from the
// store surface: a publishable key and the sales channel it is bound to.
// `docs/first-run.md` produces both, along with everything a catalog needs to be
// worth showing.
//
//	STOREFRONT_PUBLISHABLE_KEY=pk_… STOREFRONT_SALES_CHANNEL_ID=sc_… go run .
//
// Then open /shop. Everything else gobit ships — the commerce modules, their
// migrations, the admin API, the panel — comes with the one call below.
package main

import (
	"fmt"
	"os"

	"github.com/bdrtr/gobit"

	"example.com/gobit-storefront/storefront"
)

// The environment the shop reads. They are the example's own settings and not
// gobit's, which is why they carry the example's prefix: gobit's configuration
// is loaded by the facade and knows nothing about a storefront.
const (
	keyEnv     = "STOREFRONT_PUBLISHABLE_KEY"
	channelEnv = "STOREFRONT_SALES_CHANNEL_ID"
)

func main() {
	shop, err := storefront.New(storefront.Options{
		PublishableKey: os.Getenv(keyEnv),
		SalesChannelID: os.Getenv(channelEnv),
	})
	if err != nil {
		// The refusal is at startup and it names what is missing. A shop that
		// started without these would answer 401 on every request the page makes
		// and look like an empty catalog.
		fmt.Fprintf(os.Stderr, "storefront: %v\n(set %s and %s)\n", err, keyEnv, channelEnv)
		os.Exit(1)
	}

	if err := gobit.New().Add(shop).Main(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "storefront:", err)
		os.Exit(1)
	}
}
