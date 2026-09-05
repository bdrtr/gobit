// Package service is the business logic of the order module.
//
// The module's responsibility in a single sentence: knowing permanently WHAT an
// order is — with which number, in which region, on whose behalf, with which
// lines and at which amount it was placed. After an order is written its
// amounts and its lines DO NOT CHANGE; the only things that change are its
// status and the stamps tied to it.
//
// # What it does not know
//
// The order DOES NOT KNOW the cart and DOES NOT IMPORT the cart module
// (Principle 2.1/2.4, ADR 0001). The input given to [Service.CreateOrder] is
// the SNAPSHOT of the cart: the lines and the totals are brought by the caller,
// already computed. The side that reads the cart and leaves it here is the
// complete_cart WORKFLOW (plan Section 2.5, ADR 0006).
//
// It does not know the payment either: the collected and the refunded amount
// are written through [models.OrderSummary] by the flow that knows the payment
// result.
//
// # Why the totals are validated here too
//
// Even though the side that computes the total is somebody else, a WRONG
// calculation being written onto the order silently is this module's problem:
// the order is the permanent record of the amount and an amount written wrongly
// cannot be corrected afterwards (the record does not change). That is why the
// validation is three layers deep: the service (a readable error), the database
// CHECK constraint (the last defense) and the line/order subtotals matching
// each other.
//
// # Concurrency
//
// Every flow that changes the STATUS of the order runs in a single database
// transaction and starts its work by taking the order's row lock
// (SELECT ... FOR UPDATE). That makes the "read first, then write" race
// structurally impossible: of two calls trying to cancel and to complete the
// same order at the same time, the second one waits until the first one's
// transaction ends and reads the CURRENT status of the order.
//
// The order NUMBER (display_id), on the other hand, is produced not with a lock
// but with the database's IDENTITY column: for two newly opened orders there is
// no COMMON row to lock, and every "read the largest, add one" solution in the
// application layer would race (see the migration comment).
//
// # Module isolation
//
// This module knows no other module. RegionID, CustomerID, CartID and VariantID
// are the identifiers of other modules; they are stored as free text, no
// foreign key is given (Principle 2.2) and their existence is not validated
// here — validating is the job of the workflow that knows those modules.
package service

import (
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// EntityName is the entity name the module offers to the Query layer. The
// provider is registered in the container under the name "<EntityName>.query"
// (ADR 0004).
const EntityName = "order"

// Error codes. Clients may branch on these; the messages may change, the codes
// do not.
const (
	// CodeInvalidInput reports that the input did not pass the validation.
	CodeInvalidInput = "order_invalid_input"
	// CodeTotalsInconsistent reports that the totals that are to be written do
	// not satisfy the identity (total = subtotal - discount + tax + shipping).
	CodeTotalsInconsistent = "order_totals_inconsistent"
	// CodeOrderEmpty reports that an order without lines was to be opened.
	CodeOrderEmpty = "order_empty"
	// CodeNotPending reports that a status transition was requested on an order
	// that is NOT in the pending status.
	CodeNotPending = "order_not_pending"
	// CodeNotCompleted reports that an order that is not completed was to be
	// archived.
	CodeNotCompleted = "order_not_completed"
	// CodeDisplayIDInvalid reports that the order did not receive a usable
	// number.
	CodeDisplayIDInvalid = "order_display_id_invalid"
	// CodeInconsistentState reports that the record is in an undefined state.
	CodeInconsistentState = "order_inconsistent_state"
	// CodeSummaryInvalid reports that the summary amounts are unacceptable.
	CodeSummaryInvalid = "order_summary_invalid"
	// CodeRefundExceedsOrder reports that the amount of the return/claim record
	// exceeds the total of the order.
	CodeRefundExceedsOrder = "order_refund_exceeds_total"
	// CodeAfterSalesTransition reports that a transition was requested on an
	// after-sales record from a status that does not allow it.
	CodeAfterSalesTransition = "order_after_sales_invalid_transition"
	// CodeReturnQuantityExceeded reports that more units of a line were asked
	// back than were bought.
	CodeReturnQuantityExceeded = "order_return_quantity_exceeded"
	// CodeReturnLineUnknown reports that a return line points at a line that is
	// not on the order.
	CodeReturnLineUnknown = "order_return_line_unknown"
	// CodeSpendingLimitExceeded reports that the order exceeds the customer's
	// spending limit within the period.
	CodeSpendingLimitExceeded = "order_spending_limit_exceeded"
	// CodeCatalogReadFailed reports that a cross-module read through the Query
	// layer failed.
	CodeCatalogReadFailed = "order_catalog_read_failed"
	// CodeOrderNotFound reports that the order does not exist.
	CodeOrderNotFound = "order_not_found"
	// CodeSpendingCurrencyMismatch reports that the currency of the order
	// differs from the currency of the spending limit; the two amounts cannot
	// be compared without conversion.
	CodeSpendingCurrencyMismatch = "order_spending_currency_mismatch"
	// CodeSpendingPolicyUnavailable reports that the spending rule COULD NOT BE
	// READ. "There is no rule" and "we could not learn the rule" are different
	// situations; in the second one the order is not opened.
	CodeSpendingPolicyUnavailable = "order_spending_policy_unavailable"
	// CodeSpendingPolicyInvalid reports that the body of the spending rule does
	// not conform to the contract.
	CodeSpendingPolicyInvalid = "order_spending_policy_invalid"
	// CodeNotReady reports that the service was constructed with a missing
	// dependency.
	CodeNotReady = "order_service_not_ready"
)

// Pagination limits (plan Section 8: limit/offset).
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int64 = 50
	// MaxLimit is the largest page size that can be requested in one call.
	MaxLimit int64 = 100
)

// maxTextLen is the upper bound for free text fields. The bound prevents a
// single request from writing text of unbounded size into the database.
const maxTextLen = 512

// maxIDLen is the upper bound for identifiers coming from outside.
//
// These identifiers (region_id, customer_id, cart_id, variant_id) go into the
// orders_region_idx and orders_customer_idx indexes. An unbounded string would
// inflate the index by an arbitrary amount per order and would let a single
// request decide the cost of filtering.
const maxIDLen = 255

// maxOrderItems is the maximum number of lines a single order can carry.
//
// The bound prevents a single request from opening thousands of INSERTs in one
// transaction: because the lines are written in the same transaction, an
// unbounded list would occupy the transaction and the connection it holds for
// an arbitrary length of time.
const maxOrderItems = 500

// Service is the public service of the order module. It is safe for concurrent
// use.
type Service struct {
	store    Store
	events   EventPublisher
	spending SpendingPolicy
	catalog  Catalog
	log      *slog.Logger
}

// Options are the dependencies of the service.
type Options struct {
	// Repo is the persistence surface; it is required.
	Repo Store
	// Events is the event bus; it is required. The "order.placed" event is
	// published from here (plan Phase 6 DoD).
	Events EventPublisher
	// Spending is the source of the spending limit rule; it is OPTIONAL.
	//
	// When nil no limit at all is applied and the order opening path behaves as
	// if this field had never been added: there is neither an extra read nor a
	// lock. In a pure B2C installation there is no such concept as a "spending
	// limit", so that is the right default; the side that fills the field is
	// the module's wiring (see module.go).
	Spending SpendingPolicy
	// Catalog is the Query-layer surface; it is OPTIONAL.
	//
	// It is only used to read an order's payment through the "order_payment"
	// link. When nil that read FAILS rather than answering "no payment": "this
	// order was never paid" and "nobody could ask" must not look the same.
	// Every other path of the module works without it.
	Catalog Catalog
	// Logger discards the logs when it is given as nil.
	Logger *slog.Logger
}

// New produces a service with the given dependencies.
//
// A missing dependency returns an error at CONSTRUCTION time; no nil check is
// done at run time. The event bus being required as well is deliberate: had it
// been optional, in an installation where registering the bus was forgotten the
// order would be written silently but "order.placed" would never be published,
// and the omission would only become visible once it was noticed that the
// subscribers were not working — that is, in production.
//
// [Options.Spending] is the ONLY exception to this rule and it has to be: the
// spending limit is a concept specific to B2B, in a pure B2C installation there
// is no such thing as the source of the rule, and making it mandatory would
// bring down every installation without the b2b module at startup.
func New(opts Options) (*Service, error) {
	if opts.Repo == nil {
		return nil, errors.Internal(CodeNotReady, "order service cannot be built without a store")
	}
	if opts.Events == nil {
		return nil, errors.Internal(CodeNotReady,
			"order service cannot be built without an event bus")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		store:    opts.Repo,
		events:   opts.Events,
		spending: opts.Spending,
		catalog:  opts.Catalog,
		log:      log,
	}, nil
}

// Page holds the pagination parameters of list requests.
type Page struct {
	// Limit is the maximum number of rows to return; when 0 [DefaultLimit] is
	// applied.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
	// After is the opaque position from a previous page's NextCursor; the zero
	// value is the first page.
	//
	// It is what makes a deep page cheap: offset asks the database to walk and
	// DISCARD every row it skips, so its cost grows with depth, while a cursor
	// goes into the index condition and stays flat.
	After corepage.Cursor
}

// normalize validates the pagination parameters and applies the defaults.
func (p Page) normalize() (Page, error) {
	if p.Limit < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "the limit cannot be negative: %d", p.Limit)
	}
	if p.Offset < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "the offset cannot be negative: %d", p.Offset)
	}
	if p.Limit > MaxLimit {
		return Page{}, errors.Invalid(CodeInvalidInput,
			"the limit can be at most %d: %d", MaxLimit, p.Limit)
	}
	if p.Limit == 0 {
		p.Limit = DefaultLimit
	}
	return p, nil
}

// requireID validates that an identifier coming from outside is usable.
//
// The identifier IS NOT TRIMMED, it is rejected: trimming separates the
// identifier the caller sent from the identifier that is stored, and the
// difference only becomes visible after the data is corrupted. The same
// rationale holds in core/link's identifier contract as well.
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

// optionalID validates an identifier that may be left empty.
func optionalID(label, value string) error {
	if value == "" {
		return nil
	}
	return requireID(label, value)
}

// requireText validates a required text field.
func requireText(label, value string) error {
	if value == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	return checkTextLen(label, value)
}

// checkTextLen validates the length limit of a text field.
func checkTextLen(label, value string) error {
	if len(value) > maxTextLen {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes: %d", label, maxTextLen, len(value))
	}
	return nil
}

// checkAmount validates that an amount is within the permitted range.
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

// checkQuantity validates that a quantity is within the permitted range.
func checkQuantity(quantity int64) error {
	if quantity < models.MinQuantity {
		return errors.Invalid(CodeInvalidInput,
			"the quantity has to be at least %d: %d", models.MinQuantity, quantity)
	}
	if quantity > models.MaxQuantity {
		return errors.Invalid(CodeInvalidInput,
			"the quantity can be at most %d: %d", models.MaxQuantity, quantity)
	}
	return nil
}

// normalizeCurrency validates the currency code and converts it to UPPER case.
//
// The code is made unique before it is stored: "try" and "TRY" are the same
// currency, and if they were stored as two separate strings, comparing the
// totals would silently give a wrong result.
func normalizeCurrency(code string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if len(normalized) != 3 {
		return "", errors.Invalid(CodeInvalidInput,
			"currency_code must be a 3-letter ISO 4217 code: %q", code)
	}
	for _, r := range normalized {
		if r < 'A' || r > 'Z' {
			return "", errors.Invalid(CodeInvalidInput,
				"currency_code may contain letters only: %q", code)
		}
	}
	return normalized, nil
}

// normalizeEmail validates the e-mail and converts it to lower case; empty is
// accepted.
//
// The validation is DELIBERATELY shallow: full RFC 5322 validation is famous
// for rejecting valid addresses, and whether the address is really deliverable
// can only be told by sending. The only thing sought here is that the field is
// in a form that is USABLE as an e-mail.
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

// multiplyAmount multiplies the unit price by the quantity WITHOUT OVERFLOW.
//
// When the factors have passed the service validation the result is already
// below [models.MaxTotal]; the check is the last defense against a call that
// arrives with an abnormal quantity or price. An overflowing product silently
// produces a negative subtotal and could pass the consistency check BY MISTAKE.
func multiplyAmount(unitPrice, quantity int64) (int64, error) {
	if unitPrice == 0 || quantity == 0 {
		return 0, nil
	}
	if quantity < 0 || unitPrice < 0 {
		return 0, errors.Invalid(CodeInvalidInput,
			"the unit price and the quantity cannot be negative: %d x %d", unitPrice, quantity)
	}
	if quantity > models.MaxTotal/unitPrice {
		return 0, errors.Invalid(CodeInvalidInput,
			"the line subtotal exceeds the limit: %d x %d > %d", unitPrice, quantity, models.MaxTotal)
	}
	return unitPrice * quantity, nil
}

// addAmount adds two amounts WITHOUT OVERFLOW.
func addAmount(sum, value int64) (int64, error) {
	if value < 0 {
		return 0, errors.Invalid(CodeTotalsInconsistent, "amount cannot be negative: %d", value)
	}
	if sum > models.MaxTotal-value {
		return 0, errors.Invalid(CodeTotalsInconsistent,
			"the sum of the amounts exceeds the limit (%d)", models.MaxTotal)
	}
	return sum + value, nil
}
