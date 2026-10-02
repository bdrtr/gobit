// Package accounts is the shop's answer to "who opens a customer account".
//
// contrib/identity-session ships storefront self-registration and refuses to
// guess two things: which record a customer IS, and how a message reaches an
// address. Both are the shop's, and the module leaves the endpoints unmounted
// until somebody binds them (ADR 0133).
//
// This is that somebody, written the way a real project would write it — which is
// why it lives in the starter rather than in the module. A shop with its own
// users table binds something else entirely and the flow works the same.
//
// # What it is NOT
//
// The verification half here writes the link to the LOG. That is a development
// stand-in and it is named so the fact is unmissable: a real shop sends mail with
// its own client, its own templates and its own sending domain, and a deployment
// that keeps this one is putting sign-up links into its log files. The type is
// called [LogOnlyVerification] for that reason and it warns on every send.
package accounts

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/query"
)

// The container names this package resolves.
//
// They are spelled as literals rather than imported, which is what a module
// outside gobit's own tree has to do: the customer module lives under internal/
// and publishes its cross-module surface by NAME and primitive signature (ADR
// 0001, Principle 2.4). A rename on either side is caught by the resolution
// failing at startup, which is the price the pattern charges.
const (
	customerService = "customer.service"
	queryCatalog    = "core.query"
)

// customerEntity is the query layer's name for a customer record.
const customerEntity = "customer"

// customers is the narrow slice of the customer module this package calls.
//
// Declared HERE, with primitive types only, and never imported from the customer
// module: a consumer that imported it would name a type that is a different type
// in its own package, and the concrete service would stop satisfying the
// interface. Repeating the signature verbatim is the mechanism.
type customers interface {
	RegisterGuestCustomer(ctx context.Context, email, firstName, lastName, phone string) (string, error)
	ConvertGuestToAccount(ctx context.Context, customerID string) error
	ChangeCustomerEmail(ctx context.Context, customerID, email string) error
}

// Module is a gobit module whose only job is to hold the container.
//
// The seams are constructed before the container exists — an installation writes
// Options in main() — so something has to bridge that, and a module is what gets
// handed the container. It registers no service and owns no table.
type Module struct {
	log *slog.Logger
	c   *container.Container
}

// New makes the module and the two seams at once.
func New(log *slog.Logger) *Module {
	if log == nil {
		log = slog.Default()
	}

	return &Module{log: log}
}

// Name is the module's unique name.
func (m *Module) Name() string { return "starter_accounts" }

// Register captures the container; nothing is resolved yet.
//
// Resolving here would be wrong for the reason gobit's own module contract gives:
// at Register time another module's services may not be registered, so this waits
// until a request needs them.
func (m *Module) Register(_ context.Context, c *container.Container) error {
	m.c = c

	return nil
}

// Migrations returns nothing; this module owns no table.
func (m *Module) Migrations() fs.FS { return nil }

// Routes binds nothing; this module publishes no endpoint.
func (m *Module) Routes(_ chi.Router) {}

// CustomerIDForEmail answers the customer that address belongs to, or "".
//
// It reads through the QUERY layer rather than the service, because the service's
// cross-module surface has no lookup by address — and the query layer's customer
// provider accepts an "email" filter, which is the published path for exactly
// this (ADR 0004).
func (m *Module) CustomerIDForEmail(ctx context.Context, email string) (string, error) {
	catalog, err := container.Resolve[query.Query](m.c, queryCatalog)
	if err != nil {
		return "", fmt.Errorf("starter: %q could not be resolved: %w", queryCatalog, err)
	}

	// An ACCOUNT, not any record with the address. A guest checkout leaves a
	// customer row behind, and taking it for an account told somebody who once
	// bought as a guest that they already had an account they could not sign in
	// to (D221).
	records, err := catalog.Graph(ctx, query.GraphSpec{
		Entity:  customerEntity,
		Fields:  []string{query.IDField},
		Filters: map[string]any{"email": email, "has_account": true},
		Limit:   1,
	})
	if err != nil {
		return "", fmt.Errorf("starter: the customer of an address could not be read: %w", err)
	}
	if len(records) == 0 {
		return "", nil
	}

	id, _ := records[0][query.IDField].(string)

	return id, nil
}

// OpenAccount creates a customer account for an address that has just been
// proven: a customer row, then converted to an account, because has_account is
// the one field gobit's customer module tells an account from a guest by. A row
// left a guest was shown as one in the panel, missed by a segment counting
// accounts, and outside the index that keeps one address to one account (D221).
// A conversion refused — another account took the address meanwhile — fails the
// verification, and the guest row it leaves is harmless.
//
// The name and phone are left empty on purpose. Nobody typed them, and inventing
// a placeholder would put a value into a person's record that they never gave.
func (m *Module) OpenAccount(ctx context.Context, email string) (string, error) {
	service, err := container.Resolve[customers](m.c, customerService)
	if err != nil {
		return "", fmt.Errorf("starter: %q could not be resolved: %w", customerService, err)
	}

	id, err := service.RegisterGuestCustomer(ctx, email, "", "", "")
	if err != nil {
		return "", fmt.Errorf("starter: the customer could not be opened: %w", err)
	}
	if err := service.ConvertGuestToAccount(ctx, id); err != nil {
		return "", fmt.Errorf("starter: the customer could not be made an account: %w", err)
	}

	return id, nil
}

// ChangeAccountEmail moves an account's record to an address its owner has
// just proven (ADR 0377). The customer module refuses an address another
// account holds, and the refusal is passed on with its kind, so the identity
// module can tell it from a failure.
func (m *Module) ChangeAccountEmail(ctx context.Context, customerID, email string) error {
	service, err := container.Resolve[customers](m.c, customerService)
	if err != nil {
		return fmt.Errorf("starter: %q could not be resolved: %w", customerService, err)
	}
	if err := service.ChangeCustomerEmail(ctx, customerID, email); err != nil {
		return fmt.Errorf("starter: the account could not be moved to the new address: %w", err)
	}

	return nil
}
