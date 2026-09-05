package adminui

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// Panel paths and the container names it resolves.
const (
	// URLPrefix is the panel's path prefix.
	//
	// It is NOT under the admin API prefix, and that is deliberate: placed
	// there, every page reached from the address bar would get a 401 (the guard
	// reads only the Authorization header), the HTML routes would leak into the
	// OpenAPI document, and the router-walking authorization test would demand
	// a 403 from every page (ADR 0011).
	//
	// Adding this prefix to the guard stack is MANDATORY: scoping matches on
	// segment boundaries, so this tree joins neither identity nor quota on its
	// own.
	URLPrefix = "/admin/ui"

	// LoginPath is the full path of the login page. It is the only panel path
	// that does not require identity, and the only member of the exempt list.
	LoginPath = URLPrefix + "/login"
	// LogoutPath ends the session.
	LogoutPath = URLPrefix + "/logout"
	// StylesheetPath serves the panel's stylesheet.
	//
	// It sits INSIDE the panel tree so the whole panel stays under one prefix,
	// and it is exempt from identity for one reason: the login page needs it
	// too, and a login screen that renders unstyled because its stylesheet was
	// behind the login is a poor first impression of a framework.
	StylesheetPath = URLPrefix + "/panel.css"
	// ProductsPath is the catalog list.
	ProductsPath = URLPrefix + "/products"
	// ProductPath is one product's page; {id} is the product's identity.
	ProductPath = ProductsPath + "/{id}"
	// OrdersPath is the order list.
	OrdersPath = URLPrefix + "/orders"
	// OrderPath is one order's page; {id} is the order's identity.
	OrderPath = OrdersPath + "/{id}"
	// CustomersPath is the customer list.
	CustomersPath = URLPrefix + "/customers"
	// CustomerPath is one customer's page; {id} is the customer's identity.
	CustomerPath = CustomersPath + "/{id}"
	// InventoryPath is the inventory list.
	//
	// There is no single-item path: an item's detail is its per-location levels
	// and the panel already shows those on the variant page.
	InventoryPath = URLPrefix + "/inventory"
	// SalesPath is the sales report: the lines sold in a period.
	//
	// It is a GET and nothing else. The report changes nothing, and the period
	// it covers travels in the QUERY STRING rather than in a posted form or in
	// the session — an operator answering "what sold last month" sends the
	// answer to somebody else, and a page whose state lives in a session cannot
	// be sent anywhere. There is no single-line path: one sold line has no
	// detail beyond what the row already shows, and its context is the order,
	// which the row links to.
	SalesPath = URLPrefix + "/sales"

	// ServiceQuery is the cross-module read layer's container name.
	ServiceQuery = "core.query"
	// ServiceAuth is the identity service's container name.
	ServiceAuth = "auth.service"
	// InteropAuth is the authenticator's container name.
	InteropAuth = "auth.interop"
)

// Error codes. Clients may branch on these; messages change, codes do not.
const (
	// CodeNotReady reports that the panel was built without a dependency.
	CodeNotReady = "adminui_not_ready"
	// CodeDependencyMissing reports that an expected service is absent from the
	// container.
	CodeDependencyMissing = "adminui_dependency_missing"
	// CodeTemplateInvalid reports that a template could not be parsed or
	// rendered.
	CodeTemplateInvalid = "adminui_template_invalid"
	// CodeAmountInvalid reports an amount the panel could not read.
	CodeAmountInvalid = "adminui_amount_invalid"
	// CodeNotBound reports that the guard ring has not been bound to a panel
	// yet; such a request is REJECTED (see [Ring]).
	CodeNotBound = "adminui_not_bound"
)

// Catalog is the panel's read surface, declared on the CONSUMER side (ADR 0001).
//
// The surface is not the whole Query layer but the single method the panel
// uses: keeping it narrow means a future change to some other Query method
// cannot break the panel at compile time. The panel imports no module; catalog
// data arrives through this interface (ADR 0004/0006).
type Catalog interface {
	// Graph fetches root records, resolves links and returns the joined view.
	Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// Session is the pair of methods the panel needs from the identity service.
//
// The surface is deliberately NARROW: the panel does not change passwords,
// create users or manage keys, and not seeing those methods makes calling one
// by accident impossible. The interface is declared on the consumer side (ADR
// 0001); the panel does not import the auth module.
type Session interface {
	// Login verifies credentials and returns the token with its expiry.
	Login(ctx context.Context, email, password string) (string, time.Time, error)
	// Logout drops ALL of the caller's sessions and returns the cut-off instant.
	Logout(ctx context.Context, principalID, principalKind string) (time.Time, error)
}

// UI is the admin panel. It is safe for concurrent use.
type UI struct {
	catalog Catalog
	// products is the product module's admin write surface.
	//
	// It is OPTIONAL and nil when the module is not registered: the panel then
	// shows the catalog and refuses to edit, rather than refusing to start. A
	// mandatory dependency here would make the product module a requirement for
	// the panel to exist at all, which is the coupling the fourth tree was
	// created to avoid.
	products ProductWriter
	// prices is the pricing module's admin write surface; OPTIONAL, like
	// products.
	prices PriceWriter
	// stock is the inventory module's admin surface; OPTIONAL, and the only
	// one that also READS — the cross-module read layer exposes a total per
	// item and stock is edited per location.
	stock         StockAdmin
	session       Session
	authenticator corehttp.Authenticator
	templates     *templateSet
	// secureCookie is the session cookie's Secure flag.
	//
	// It is on in SHARED environments, and the distinction is not invented: the
	// framework already separates development as "the only environment where
	// secrets and TLS requirements are relaxed". Local development runs over
	// plain HTTP, where a Secure cookie would never be sent and the panel could
	// not be opened at all.
	secureCookie bool
}

// FromContainer builds the panel on the container.
//
// The name is CONVENTIONAL and the wiring invariant in internal/arch uses it in
// the reverse direction: to catch a package the composition root builds but the
// invariant cannot see as a constructor. The same name holds in the
// internal/workflows tree; this package is that pattern's second instance
// (ADR 0011).
//
// Templates are parsed HERE, not on first request: a broken template must stop
// the server at startup. The composition root turns the error into an exit
// code, so the failure shows up in deployment rather than in front of a user.
func FromContainer(c *container.Container, secureCookie bool) (*UI, error) {
	if c == nil {
		return nil, errors.Internal(CodeNotReady,
			"admin panel cannot be built without a container")
	}

	catalog, err := resolveService[Catalog](c, ServiceQuery)
	if err != nil {
		return nil, err
	}
	session, err := resolveService[Session](c, ServiceAuth)
	if err != nil {
		return nil, err
	}
	authenticator, err := resolveService[corehttp.Authenticator](c, InteropAuth)
	if err != nil {
		return nil, err
	}

	templates, err := loadTemplates()
	if err != nil {
		return nil, err
	}

	// The write surface is resolved OPTIONALLY: an installation without the
	// product module still gets a panel, and the edit form answers 503 with a
	// sentence naming the reason. Treating it like the others would turn a
	// removable module into a hard requirement of the panel.
	products, err := optionalService[ProductWriter](c, ServiceProductAdmin)
	if err != nil {
		return nil, err
	}
	prices, err := optionalService[PriceWriter](c, ServicePricingAdmin)
	if err != nil {
		return nil, err
	}
	stock, err := optionalService[StockAdmin](c, ServiceInventoryAdmin)
	if err != nil {
		return nil, err
	}

	return &UI{
		catalog:       catalog,
		products:      products,
		prices:        prices,
		stock:         stock,
		session:       session,
		authenticator: authenticator,
		templates:     templates,
		secureCookie:  secureCookie,
	}, nil
}

// resolveService looks a service up by name and wraps the failure with the
// panel's code.
//
// The message names WHICH service could not be resolved: a panel that cannot be
// built means a server that stops at startup, and this line is the only thing
// there is to read at that moment.
func resolveService[T any](c *container.Container, name string) (T, error) {
	value, err := container.Resolve[T](c, name)
	if err != nil {
		var zero T
		return zero, errors.Wrap(err, errors.KindOf(err), CodeDependencyMissing,
			"admin panel could not resolve service %q", name)
	}
	return value, nil
}

// optionalService resolves a service the panel can live without.
//
// An ABSENT name gives the zero value and no error: the module was not
// installed, which is a legitimate configuration and not a failure. A name that
// IS registered but whose surface does not match still fails, and fails at
// startup — that is a wiring mistake, and silently degrading it would leave the
// panel showing "editing unavailable" while the module sat right there.
func optionalService[T any](c *container.Container, name string) (T, error) {
	var zero T
	if c == nil || !c.Has(name) {
		return zero, nil
	}

	return resolveService[T](c, name)
}
