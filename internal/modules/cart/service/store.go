package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// Store is the persistence surface the service needs.
//
// The interface is defined on the CONSUMING side, that is, here (ADR 0001's
// pattern). The service DOES NOT import the repository package; the concrete
// store satisfies these signatures structurally and the wiring is done in
// module.go. That way the unit tests can be written without a real database,
// with a fake store of a few hundred lines.
//
// # Transaction boundary
//
// [Store.WithTx] runs the given function in a single database transaction and
// carries the transaction with the context the function receives. That is why
// every call inside the transaction must be made with THE CTX GIVEN TO THE
// FUNCTION; if the outer ctx is used, that call falls outside the transaction
// and the atomicity is silently lost.
//
// [Store.LockCart] locks the cart until the end of the transaction and can only
// be called inside [Store.WithTx]. Every flow that changes the cart does its
// read with this method: a cart read without a lock can be stale at the moment
// of the write, and two concurrent additions could open two lines for the same
// variant.
//
// [Store.CartsForErasure] takes the same lock for the same reason, on the carts
// of one person; the two anonymizing writes that follow it work from the
// identifiers it returned, so all three belong to a single [Store.WithTx] (see
// erasure.go).
//
// The four *ForDisclosure methods are the opposite case and are deliberately
// outside every one of these: no transaction, no lock, no write. They answer
// what the module holds about a person, and that answer must not be able to
// change the rows it describes or to queue behind a checkout holding a cart's
// lock. The argument for accepting the reads not sharing one snapshot — and for
// why the ONE figure that would have suffered from that is read inside the same
// statement as the rows it describes — is in repository/disclosure.go.
//
// [Store.WithReadTx] is for reads that do not write but make MORE THAN ONE query
// (see [Service.GetCart]). It takes no lock; the only guarantee it gives is that
// all of the queries inside it see the SAME state of the cart. The reason it is
// a separate method is the isolation level: at PostgreSQL's default READ
// COMMITTED level every STATEMENT takes its own snapshot, that is, wrapping the
// queries in an ordinary transaction DOES NOT PREVENT the torn view; what
// prevents it is REPEATABLE READ.
//
// # Lock order
//
// The lock is taken in the same order IN EVERY FLOW: first the CART, then the
// child lines. The children are not locked separately — the cart lock already
// serializes all of the write paths of that cart, and a single lock makes it
// impossible for the order to be reversed (and therefore for a deadlock to
// happen).
type Store interface {
	// WithTx runs fn in a single transaction; if fn returns an error the
	// transaction is rolled back.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
	// WithReadTx runs fn in a read-only transaction with a SINGLE SNAPSHOT.
	WithReadTx(ctx context.Context, fn func(ctx context.Context) error) error

	// CreateCart records a new cart.
	CreateCart(ctx context.Context, cart models.Cart) (models.Cart, error)
	// GetCart returns the cart by its identifier; NotFound if there is none.
	GetCart(ctx context.Context, id string) (models.Cart, error)
	// LockCart locks the cart for the duration of the transaction and returns
	// its current state.
	LockCart(ctx context.Context, id string) (models.Cart, error)
	// ListCarts filters and paginates the carts; the second value is the total
	// count.
	ListCarts(ctx context.Context, filter models.CartFilter) ([]models.Cart, int64, error)
	// CartsByIDs fetches a set of identifiers in a SINGLE query (no N+1).
	CartsByIDs(ctx context.Context, ids []string) ([]models.Cart, error)
	// UpdateCartContact writes the cart's email and customer fields as ABSOLUTE
	// values.
	UpdateCartContact(ctx context.Context, id string, contact models.CartContact) (models.Cart, error)
	// UpdateCartTotals writes the totals and stamps which shape they were
	// calculated for.
	UpdateCartTotals(ctx context.Context, id string, totals models.CartTotals) (models.Cart, error)
	// BumpCartRevision increments the cart's shape counter by one.
	BumpCartRevision(ctx context.Context, id string) (models.Cart, error)
	// MarkCartCompleted stamps the cart as completed.
	MarkCartCompleted(ctx context.Context, id string) (models.Cart, error)
	// SoftDeleteCart soft deletes the cart.
	SoftDeleteCart(ctx context.Context, id string) error

	// CreateLineItem records a new cart line.
	CreateLineItem(ctx context.Context, item models.LineItem) (models.LineItem, error)
	// GetLineItem returns the line by its identifier; another cart's line is
	// NotFound.
	GetLineItem(ctx context.Context, cartID, lineID string) (models.LineItem, error)
	// GetLineItemByVariant returns the living line of the variant in the cart.
	GetLineItemByVariant(ctx context.Context, cartID, variantID string) (models.LineItem, error)
	// ListLineItems returns the cart's lines in creation order.
	ListLineItems(ctx context.Context, cartID string) ([]models.LineItem, error)
	// SetLineItemQuantity writes the line's quantity as an ABSOLUTE value.
	SetLineItemQuantity(ctx context.Context, cartID, lineID string, quantity int64) (models.LineItem, error)
	// SetLineItemTotals writes ALL of the line amounts of one calculation round
	// in a SINGLE call; it does not touch the quantities. If there is a line
	// that cannot be written, NotFound is returned and none of them count as
	// written (the call is inside the transaction).
	SetLineItemTotals(ctx context.Context, cartID string, lines []models.LineItemTotals) error
	// SoftDeleteLineItem soft deletes the line.
	SoftDeleteLineItem(ctx context.Context, cartID, lineID string) error
	// SoftDeleteLineItemsByCart soft deletes all of the cart's lines.
	SoftDeleteLineItemsByCart(ctx context.Context, cartID string) error

	// UpsertCartAddress writes the cart's address of the given type.
	UpsertCartAddress(ctx context.Context, addr models.CartAddress) (models.CartAddress, error)
	// ListCartAddresses returns the cart's addresses.
	ListCartAddresses(ctx context.Context, cartID string) ([]models.CartAddress, error)
	// SoftDeleteCartAddressesByCart soft deletes all of the cart's addresses.
	SoftDeleteCartAddressesByCart(ctx context.Context, cartID string) error

	// CartsForErasure returns the identifiers of the person's carts and LOCKS
	// them; it can only be called inside [Store.WithTx]. An empty identifier
	// means "do not match on this one", and both empty matches nothing.
	CartsForErasure(ctx context.Context, customerID, email string) ([]string, error)
	// AnonymizeCartContacts nulls the e-mail of the given carts and returns the
	// number of rows written.
	AnonymizeCartContacts(ctx context.Context, cartIDs []string) (int64, error)
	// AnonymizeCartAddresses nulls the personal columns of the given carts'
	// addresses and returns the number of rows written.
	AnonymizeCartAddresses(ctx context.Context, cartIDs []string) (int64, error)

	// CartsForDisclosure returns the person's carts, newest first, at most
	// limit of them, and — as the second value — HOW MANY matched in total.
	// The two numbers differ when the bound cut, and the disclosure has to be
	// able to say so; a caller that ignores the count produces a document that
	// is silently short (see disclosure.go).
	//
	// It takes no lock and belongs to no transaction: it is a read that must
	// not change or block what it describes.
	CartsForDisclosure(
		ctx context.Context, customerID, email string, limit int64,
	) ([]models.PersonalCart, int64, error)
	// CartAddressesForDisclosure returns every address of the given carts,
	// soft-deleted rows included.
	CartAddressesForDisclosure(ctx context.Context, cartIDs []string) ([]models.PersonalAddress, error)
	// CartLineItemNotesForDisclosure returns the lines of the given carts that
	// carry a non-empty metadata document; the empty ones are left in the
	// database rather than traveling as records with nothing in them.
	CartLineItemNotesForDisclosure(ctx context.Context, cartIDs []string) ([]models.PersonalNote, error)
	// CartShippingNotesForDisclosure returns the shipping methods of the given
	// carts that carry non-empty provider data.
	CartShippingNotesForDisclosure(ctx context.Context, cartIDs []string) ([]models.PersonalNote, error)

	// CreateShippingMethod adds a shipping method to the cart.
	CreateShippingMethod(ctx context.Context, method models.ShippingMethod) (models.ShippingMethod, error)
	// ListShippingMethods returns the cart's shipping methods.
	ListShippingMethods(ctx context.Context, cartID string) ([]models.ShippingMethod, error)
	// SoftDeleteShippingMethod soft deletes the shipping method.
	SoftDeleteShippingMethod(ctx context.Context, cartID, methodID string) error
	// SoftDeleteShippingMethodsByCart deletes all of the cart's shipping
	// methods.
	SoftDeleteShippingMethodsByCart(ctx context.Context, cartID string) error
}
