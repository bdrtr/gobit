// Package service is the business logic of the cart module.
//
// The module's responsibility in a single sentence: to know WHAT a cart has —
// in which region, on whose behalf, with which lines, with which address and
// shipping method. HOW MUCH the cart COMES TO IS NOT this module's job.
//
// # Why the totals are not computed here
//
// The subtotal's price comes from pricing, the tax from region/tax; the flow
// that brings the two together touches more than one module and, as plan
// Section 2.5 requires, belongs to a WORKFLOW (calculate_totals). ADR 0006 also
// determines the form of that access: the workflow does not import the modules,
// it defines the narrow interface in its own package and resolves the service
// from the container by name. That is why the cart service DOES NOT CALL any
// price or tax source; it only STORES and VALIDATES the result arriving through
// [Service.SetTotals].
//
// The validation is there to prevent a calculation error in the workflow from
// being written to the database silently, and it is threefold: the service (a
// readable error), the database CHECK constraint (the last defense) and
// [models.Cart.TotalsStale] (the visibility of staleness).
//
// # Concurrency
//
// EVERY flow that changes the cart runs in a single database transaction and
// starts its work by taking the cart's row lock (SELECT ... FOR UPDATE). This
// makes the "read first, write later" race structurally impossible: of two calls
// trying to add a line to the same cart at the same time, the second one waits
// until the first one's transaction is done and, under READ COMMITTED, reads the
// CURRENT state of the cart. Two additions racing for the same variant therefore
// do not produce two lines; the second one sees the line the first one opened
// and increments its quantity.
//
// The lock order is single and the same in every flow: first the CART, then the
// child lines. Had the order changed from flow to flow, two flows would ask for
// the same two rows in opposite orders and lock each other out, and the database
// would kill one of the transactions.
//
// # Immutability
//
// A cart whose [models.Cart.CompletedAt] is set IS IMMUTABLE: it is the record
// the order history rests on. Every writing method checks this under the lock
// and returns errors.Conflict.
//
// # Module isolation
//
// This module knows no other module (Principle 2.1/2.4, ADR 0001). RegionID,
// CustomerID and VariantID are other modules' identifiers; they are stored as
// free text, no foreign key is given (Principle 2.2) and their existence is not
// validated in this module — the validation is the job of the workflow that
// knows those modules.
package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// EntityName is the entity name the module offers to the Query layer. The
// provider is registered in the container under the name "<EntityName>.query"
// (ADR 0004).
const EntityName = "cart"

// Error codes. Clients may branch on these; the messages may change, the codes
// do not.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "cart_invalid_input"
	// CodeCompleted reports that a completed cart was asked to be changed.
	CodeCompleted = "cart_completed"
	// CodeTotalsInconsistent reports that the totals to be written do not
	// satisfy the identity (total = subtotal - discount + tax + shipping).
	CodeTotalsInconsistent = "cart_totals_inconsistent"
	// CodeTotalsStale reports that the totals do not belong to the current shape
	// of the cart.
	CodeTotalsStale = "cart_totals_stale"
	// CodeCartEmpty reports that a cart without lines was asked to be completed.
	CodeCartEmpty = "cart_empty"
	// CodeCustomerMismatch reports that the cart was asked to be handed over to
	// another customer.
	CodeCustomerMismatch = "cart_customer_mismatch"
	// CodeLineItemNotFound reports that a line that is not in the cart was
	// referred to.
	CodeLineItemNotFound = "cart_line_item_not_found"
	// CodeNotReady reports that the service was built with a missing dependency.
	CodeNotReady = "cart_service_not_ready"
)

// Pagination limits (plan Section 8: limit/offset).
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int64 = 50
	// MaxLimit is the largest page size that can be asked for in one request.
	MaxLimit int64 = 100
)

// maxTextLen is the upper bound for free-text fields. The bound prevents a
// single request from writing text of unbounded size to the database.
const maxTextLen = 512

// maxIDLen is the upper bound for the identifiers arriving from the outside.
//
// These identifiers (region_id, customer_id, variant_id) go into the
// carts_region_idx and carts_customer_idx indexes. An unbounded string would
// inflate the index by an arbitrary amount per cart and would let a single
// request determine the cost of filtering.
const maxIDLen = 255

// Service is the cart module's outward-facing service. It is safe for
// concurrent use.
type Service struct {
	store Store
	log   *slog.Logger
}

// Options are the service's dependencies.
type Options struct {
	// Repo is the persistence surface; it is required.
	Repo Store
	// Logger, when nil is given, makes the logs be discarded.
	Logger *slog.Logger
}

// New produces a service with the given dependencies.
//
// A missing dependency returns an error at BUILD time; no nil check is done at
// runtime. A service without a store would produce a panic on every call, and
// there is no reason at all for that to show up on the first request rather
// than at startup.
func New(opts Options) (*Service, error) {
	if opts.Repo == nil {
		return nil, errors.Internal(CodeNotReady, "the cart service cannot be built without a store")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: opts.Repo, log: log}, nil
}

// Page holds the pagination parameters of the list requests.
type Page struct {
	// Limit is the maximum number of rows to return; if it is 0, [DefaultLimit]
	// is applied.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}

// normalize validates the pagination parameters and applies the defaults.
func (p Page) normalize() (Page, error) {
	if p.Limit < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "limit cannot be negative: %d", p.Limit)
	}
	if p.Offset < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "offset cannot be negative: %d", p.Offset)
	}
	if p.Limit > MaxLimit {
		return Page{}, errors.Invalid(CodeInvalidInput,
			"limit can be at most %d: %d", MaxLimit, p.Limit)
	}
	if p.Limit == 0 {
		p.Limit = DefaultLimit
	}
	return p, nil
}

// mutate IS THE COMMON FRAME of the flows that change the cart.
//
// In order: open a single transaction -> LOCK the cart -> reject it if it is
// completed -> do the work -> increment the cart's shape counter. The frame
// being in a single place guarantees two things: (a) no write path can skip the
// lock or the immutability check, (b) no structural change that makes the totals
// stale is left unstamped.
//
// The ctx given to fn CARRIES THE TRANSACTION; every call inside must be made
// with this ctx, otherwise that call falls outside the transaction and the
// atomicity is silently lost.
//
// The returned cart is the state AFTER the counter was incremented: the copy
// given to fn is stale by then and returning it would be showing the caller one
// revision short.
func (s *Service) mutate(ctx context.Context, cartID string, fn func(ctx context.Context, cart models.Cart) error) (models.Cart, error) {
	if err := requireID("cart_id", cartID); err != nil {
		return models.Cart{}, err
	}

	var updated models.Cart
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		cart, err := s.store.LockCart(ctx, cartID)
		if err != nil {
			return err
		}
		if cart.Completed() {
			return completedError(cart.ID)
		}
		if err := fn(ctx, cart); err != nil {
			return err
		}
		updated, err = s.store.BumpCartRevision(ctx, cartID)
		return err
	})
	if err != nil {
		return models.Cart{}, err
	}
	return updated, nil
}

// completedError is the typed error of an attempt to write to a completed cart.
func completedError(cartID string) error {
	return errors.Conflict(CodeCompleted,
		"the cart is completed and cannot be changed: %s", cartID)
}

// requireID validates that an identifier arriving from the outside is usable.
//
// The identifier IS NOT TRIMMED, it is rejected: trimming pulls the identifier
// the caller sent apart from the identifier that is stored, and the difference
// only becomes visible after the data is corrupted. The same rationale holds in
// core/link's identifier contract as well.
func requireID(label, value string) error {
	if value == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	if strings.TrimSpace(value) != value {
		return errors.Invalid(CodeInvalidInput, "%s cannot contain leading/trailing whitespace: %q", label, value)
	}
	if len(value) > maxIDLen {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes: %d", label, maxIDLen, len(value))
	}
	return nil
}

// requireText validates a required text field.
func requireText(label, value string) error {
	if value == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	return checkTextLen(label, value)
}

// checkTextLen validates the length bound of a text field.
func checkTextLen(label, value string) error {
	if len(value) > maxTextLen {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes: %d", label, maxTextLen, len(value))
	}
	return nil
}

// checkAmount validates that an amount is within the allowed range.
//
// The upper bound is not arbitrary: it makes overflow structurally impossible
// (see [models.MaxAmount] and [models.MaxTotal]).
func checkAmount(label string, value, upper int64) error {
	if value < models.MinAmount {
		return errors.Invalid(CodeInvalidInput,
			"%s cannot be negative: %d", label, value)
	}
	if value > upper {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d: %d", label, upper, value)
	}
	return nil
}

// checkQuantity validates that a quantity is within the allowed range.
func checkQuantity(quantity int64) error {
	if quantity < models.MinQuantity {
		return errors.Invalid(CodeInvalidInput,
			"the quantity must be at least %d: %d", models.MinQuantity, quantity)
	}
	if quantity > models.MaxQuantity {
		return errors.Invalid(CodeInvalidInput,
			"the quantity can be at most %d: %d", models.MaxQuantity, quantity)
	}
	return nil
}

// normalizeCurrency validates the currency code and converts it to UPPERCASE.
//
// The code is unified before it is stored: "try" and "TRY" are the same
// currency, and if they were stored as two separate strings, comparing the
// totals would silently give a wrong result.
func normalizeCurrency(code string) (string, error) {
	if strings.TrimSpace(code) == "" {
		return "", errors.Invalid(CodeInvalidInput, "currency_code cannot be empty")
	}
	return alphaCode("currency_code", "ISO 4217", code, 3)
}

// normalizeCountry validates the country code and converts it to UPPERCASE; an
// empty one is accepted.
func normalizeCountry(code string) (string, error) {
	if strings.TrimSpace(code) == "" {
		return "", nil
	}
	return alphaCode("country_code", "ISO 3166-1 alpha-2", code, 2)
}

// alphaCode validates a fixed-length letter code and converts it to UPPERCASE.
//
// The currency code and the country code SHARE THE SAME rule, and that is
// exactly the reason for the common helper: when the rule was written separately
// in two places, one of them stayed incomplete — on the country code only the
// length was being checked, whether it was letters was not being asked, and a
// code like "12" or "T1" could get into the cart's address. Because in Phase 7
// the country code will be the KEY of the tax region and shipping option
// mapping, the error of a malformed code would blow up long after the cart, at
// the mapping stage.
//
// The leading and trailing whitespace IS TRIMMED, the code is converted to
// UPPERCASE. A non-letter character, however, is not dropped, it is REJECTED:
// whitespace and letter case are spelling variants of the same code, but
// reducing an input like "T1" to "T" and storing it would make the difference
// visible only after the data was corrupted.
//
// [requireID] is stricter and rejects an identifier with whitespace too; an
// identifier is a reference arriving from the outside, whereas a code is user
// input.
func alphaCode(label, standard, code string, length int) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if len(normalized) != length {
		return "", errors.Invalid(CodeInvalidInput,
			"%s must be a %d-letter %s code: %q", label, length, standard, code)
	}
	for _, r := range normalized {
		if r < 'A' || r > 'Z' {
			return "", errors.Invalid(CodeInvalidInput,
				"%s can contain letters only: %q", label, code)
		}
	}
	return normalized, nil
}

// normalizeEmail validates the email and converts it to lowercase; an empty one
// is accepted.
//
// The validation is DELIBERATELY shallow: full RFC 5322 validation is famous for
// rejecting valid addresses, and whether the address is really deliverable can
// only be told by a send. The only thing sought here is that the field be in a
// USABLE shape as an email.
func normalizeEmail(email string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return "", nil
	}
	if err := checkTextLen("email", normalized); err != nil {
		return "", err
	}
	at := strings.IndexByte(normalized, '@')
	if at <= 0 || at == len(normalized)-1 || strings.ContainsAny(normalized, " \t\n") {
		return "", errors.Invalid(CodeInvalidInput, "the email does not look valid: %q", email)
	}
	if strings.Count(normalized, "@") != 1 {
		return "", errors.Invalid(CodeInvalidInput, "the email does not look valid: %q", email)
	}
	return normalized, nil
}
