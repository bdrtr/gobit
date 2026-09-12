// Package storefront serves a guest shop from the same process gobit runs in.
//
// # What it proves
//
// That the published surface is enough to put a BROWSER in front of gobit. The
// module imports core/http, core/module and core/container and nothing else from
// this repository; it reads no database and holds no service. Every figure on
// every page comes from /store/v1, fetched by the browser with the shop's
// publishable key — which is the same path a storefront on another host would
// take, minus the cross-origin question.
//
// # Why the same process
//
// Because a page served from here is SAME-ORIGIN with /store/v1, so the browser
// sends its requests without a preflight and no installation has to open CORS to
// make the example run. A storefront on its own port is a legitimate shape and a
// different lesson; it would begin with a configuration step that has nothing to
// do with shopping.
//
// # What it deliberately does not do
//
// It shows a catalog, opens a cart and adds a line. It does not take payment,
// does not ask for an address and does not sign anybody in: gobit issues no
// customer identity (ADR 0008), and a guest cart is the storefront's own default
// path. `docs/first-run.md` carries the whole sequence, payment included, as
// commands.
package storefront

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/module"
)

// The paths this module serves. They sit outside every prefix gobit guards, and
// that is what makes them the example's own: /admin/v1, /store/v1 and /admin/ui
// carry rings this module neither installs nor could.
const (
	// ListPath is the catalog.
	ListPath = "/shop"
	// ProductPath is one product, addressed by its HANDLE — which is what a
	// storefront address carries, and the store endpoint accepts either.
	ProductPath = ListPath + "/products/{handle}"
	// CartPath is the shopper's cart.
	CartPath = ListPath + "/cart"
	// ScriptPath serves the one script these three pages run.
	ScriptPath = ListPath + "/storefront.js"
)

// Options is what the shop cannot discover for itself.
//
// Both values are minted by the operator and neither is readable from the store
// surface: no /store/v1 endpoint lists sales channels, and a publishable key is
// returned once, by the admin endpoint that creates it. `docs/first-run.md`
// step 5 is where both come from.
type Options struct {
	// PublishableKey binds every request this shop's browser makes to a sales
	// channel. It is NOT a secret — it is visible in the page, by design, and
	// its only authority is naming the channel (see docs/security.md).
	PublishableKey string
	// SalesChannelID is the channel whose catalog is shown. It travels in the
	// PATH of the catalog endpoints, which is why the shop has to be told it
	// rather than deriving it from the key: the key's channel set is not
	// readable from the browser.
	SalesChannelID string
}

// Module is the shop.
type Module struct{ opts Options }

var _ module.Module = (*Module)(nil)

// New builds the shop, or refuses.
//
// It refuses rather than starting a shop that cannot answer: a missing key means
// every request the page makes comes back 401, and a missing channel means the
// catalog address has a hole in it. Both would look like an empty shop, which is
// the failure this repository keeps naming — a screen that shows nothing and does
// not say why.
func New(opts Options) (*Module, error) {
	if strings.TrimSpace(opts.PublishableKey) == "" {
		return nil, fmt.Errorf("the storefront needs a publishable key; " +
			"docs/first-run.md step 5 mints one and prints it once")
	}
	if strings.TrimSpace(opts.SalesChannelID) == "" {
		return nil, fmt.Errorf("the storefront needs a sales channel id; " +
			"it is the channel the key was bound to, and the catalog address carries it")
	}

	return &Module{opts: opts}, nil
}

// Name is the module's name.
func (m *Module) Name() string { return "storefront" }

// Register puts nothing in the container: this module holds no service and reads
// no database. Everything it shows is fetched by the browser.
func (m *Module) Register(_ context.Context, _ *container.Container) error { return nil }

// Migrations returns nil; the shop owns no table.
func (m *Module) Migrations() fs.FS { return nil }

// Routes binds the three pages and the script they run.
//
// They go inside one chi group so the content policy is installed ONCE, which is
// the shape ADR 0157 settled for the admin panel after installing it per handler
// turned out to cover the handlers rather than the address.
func (m *Module) Routes(r chi.Router) {
	r.Group(func(g chi.Router) {
		g.Use(securityHeaders)

		g.Get(ListPath, m.showList)
		g.Get(ProductPath, m.showProduct)
		g.Get(CartPath, m.showCart)
		g.Get(ScriptPath, m.serveScript)
	})
}
