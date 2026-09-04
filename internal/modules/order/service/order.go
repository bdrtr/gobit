package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// CreateOrderItemInput is the snapshot of an order line.
//
// All the amounts are INTEGER minor units (plan Section 8) and they arrive
// ALREADY COMPUTED; this module computes neither price nor tax.
type CreateOrderItemInput struct {
	// VariantID is the product variant the line points at; it is REQUIRED. It
	// belongs to the product module, its existence is not validated here
	// (Principle 2.2).
	VariantID string
	// Title is the display name of the line; it is REQUIRED and it is COPIED
	// from the variant.
	Title string
	// Quantity is the number of units on the line; it has to be positive.
	Quantity int64
	// UnitPrice is the unit price (minor unit).
	UnitPrice int64
	// Subtotal is the subtotal of the line: UnitPrice x Quantity.
	Subtotal int64
	// DiscountTotal is the discount falling on the line; it is given as a
	// POSITIVE number and it is subtracted.
	DiscountTotal int64
	// TaxTotal is the tax falling on the line.
	TaxTotal int64
	// Total is the total of the line: Subtotal - DiscountTotal + TaxTotal.
	Total int64
	// Metadata is the caller's free extra data.
	Metadata map[string]any
}

// CreateOrderInput is the input of a new order; it is the SNAPSHOT of the cart.
//
// The order module DOES NOT KNOW and DOES NOT IMPORT the cart module (ADR
// 0001). The side that reads the cart and builds this snapshot is the
// complete_cart WORKFLOW (ADR 0006); that is why the lines and the totals
// arrive here ALREADY COMPUTED.
type CreateOrderInput struct {
	// RegionID is the region of the order; it is REQUIRED.
	RegionID string
	// CustomerID is the owner of the order; it is OPTIONAL. When it is left
	// empty the order belongs to a GUEST.
	//
	// Today the field is an OWNERSHIP CLAIM coming from the body of the
	// storefront cart and this module cannot validate it. Because the spending
	// limit is tied to exactly this field, the field arriving empty means that
	// the rule is never even asked for; the whole boundary is in the
	// [Service.spendingRuleFor] godoc.
	CustomerID string
	// Email is the contact address of the order; it is optional but on a guest
	// order it is the only way to follow it.
	Email string
	// CurrencyCode is the ISO 4217 code; it is REQUIRED.
	CurrencyCode string
	// CartID is the cart the order was born from; it is optional and it only
	// documents the ORIGIN.
	CartID string
	// IdempotencyKey prevents the same order from being written twice; it is
	// optional.
	//
	// When it is given the call becomes IDEMPOTENT: if it is called a second
	// time with the same key no new order is opened, the EXISTING order is
	// returned. Because a saga can retry a step (plan Section 2.6) this field
	// has to be filled in the complete_cart flow; when it is left empty every
	// call produces a new order.
	IdempotencyKey string
	// Subtotal is the sum of the line subtotals (minor unit).
	Subtotal int64
	// DiscountTotal is the total discount; it is given as a POSITIVE number and
	// it is subtracted from the total.
	DiscountTotal int64
	// TaxTotal is the total tax (minor unit).
	TaxTotal int64
	// ShippingTotal is the total shipping amount (minor unit).
	ShippingTotal int64
	// Total is the amount to be paid:
	// Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
	Total int64
	// Items are the lines of the order; AT LEAST ONE line is required.
	Items []CreateOrderItemInput
	// Metadata is the caller's free extra data.
	Metadata map[string]any
}

// CreateOrder produces an order out of the snapshot of the cart.
//
// # Validation layers
//
// The order goes from the cheap to the expensive and ALL of it is done before
// writing; there is no such thing as a partially written order.
//
//  1. Identifier and text fields: the region, the currency, the e-mail, the
//     line titles.
//  2. Range: no amount can be negative and none can exceed the upper bound
//     (overflow protection).
//  3. Scope: the order has to carry AT LEAST ONE line. What is born out of an
//     order without lines is an order in which nothing was sold.
//  4. Line subtotal: Subtotal = UnitPrice x Quantity. A line priced with the
//     wrong quantity would be caught at no other gate.
//  5. Line identity: Total = Subtotal - DiscountTotal + TaxTotal and the line
//     discount cannot exceed the subtotal.
//  6. Order subtotal: Subtotal is the SUM of the subtotals of the lines.
//     Because a discount and a tax can also arise at the order level (a
//     campaign, shipping tax) only the subtotal is subject to this rule.
//  7. Order identity: Total = Subtotal - DiscountTotal + TaxTotal +
//     ShippingTotal and the discount cannot exceed the subtotal.
//
// The two checks in the seventh item MUST be present TOGETHER: the identity on
// its own is not enough, because when an excessive discount is swallowed by the
// tax and the shipping the identity HOLDS. Example: subtotal=1000,
// discount=3000, shipping=2500 -> total=500; the customer pays 500 for goods
// worth 1000 together with shipping worth 2500 and the identity check does not
// see it.
//
// The validation being done here as well is necessary even though the side that
// does the computation is another module: the order is the PERMANENT record of
// the amount and an amount written wrongly cannot be corrected afterwards (the
// record does not change). The wrong computation of a saga step must not be
// written onto the order silently.
//
// # Spending limit
//
// If the customer has a spending limit (for the source of the rule see
// [SpendingPolicy]) the order is not opened when it goes ABOVE the limit and
// the call returns errors.Conflict ([CodeSpendingLimitExceeded]).
//
// WHO IT IS APPLIED TO: only to orders whose [CreateOrderInput.CustomerID] is
// filled in. When the field is empty the rule is not even ASKED FOR and the
// order is opened independently of the limit. On today's surface that field is
// an unvalidated declaration, that is, the limit is applied not to "every
// purchase" but to "a purchase that declares its customer"; the whole boundary
// and why it could not be closed in this module are in the
// [Service.spendingRuleFor] godoc and in ADR 0008.
//
// HOW THE SPEND IS COUNTED: it is the sum of the orders the customer placed
// within the window the rule reports. CANCELED and soft-deleted orders do not
// enter the sum; the 'pending' ones DO (an order whose payment fails is
// canceled by the saga and at that moment takes itself out of the sum). The
// amount REFUNDED per order is SUBTRACTED from the sum — if the money came back
// to the company the budget has to come back too. The whole query and its
// rationale are in the queries/spending.sql document.
//
// WHERE THE CHECK IS: inside the transaction in which the order is written and
// under the customer lock (see [Service.writeOrder]). This structurally closes
// the "check first, then write" race: a second order coming in for the same
// customer waits and reads the sum together with the row the first one wrote.
//
// CURRENCY: if the currency of the order differs from the currency of the limit
// the order is not opened ([CodeSpendingCurrencyMismatch]). NO conversion is
// done; for the rationale see [spendingRule.checkCurrency].
//
// # Idempotency
//
// If [CreateOrderInput.IdempotencyKey] was given the call is idempotent: a
// second call with the same key does not open a new order, it returns the
// existing one. The protection is twofold — first the key is looked up (the
// cheap path), then the unique index in the database rejects one of two racing
// concurrent calls and the rejected call reads the record again and returns it
// (see [Service.replayedOrder]).
//
// A repeated call does not count the spend a SECOND TIME: the cheap path finds
// the order by its key and never enters the limit check at all.
//
// # Order: write -> publish
//
// The order, its lines, its summary and the validation of its number are in a
// SINGLE transaction. That is, a committed order is always COMPLETE: its number
// is valid, its region and (if any) its customer are written in their own
// columns and it is never taken back. No side effect is produced before the
// write; that is the reason no compensation path is needed.
//
// The "order.placed" event is published LAST, once the order is final; a
// publishing failure does not drop the order (rationale:
// [Service.publishOrderPlaced]).
func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (models.Order, error) {
	normalized, err := normalizeCreateOrder(in)
	if err != nil {
		return models.Order{}, err
	}

	// The cheap path: if the key has already been used, do not even attempt to
	// write.
	if normalized.IdempotencyKey != "" {
		existing, lookupErr := s.store.GetOrderByIdempotencyKey(ctx, normalized.IdempotencyKey)
		switch {
		case lookupErr == nil:
			s.log.InfoContext(ctx, "an order with the same idempotency key already exists, returning the existing record",
				"order_id", existing.ID, "display_id", existing.DisplayID)
			return existing, nil
		case !errors.IsNotFound(lookupErr):
			return models.Order{}, lookupErr
		}
	}

	// The spending rule is read BEFORE any row is written: a definitive
	// rejection such as a currency mismatch must leave no trace behind it. The
	// APPLICATION of the rule to the spend, on the other hand, is done inside
	// the write transaction and under the customer lock (see spending.go).
	rule, err := s.spendingRuleFor(ctx, normalized.CustomerID)
	if err != nil {
		return models.Order{}, err
	}
	if err := rule.checkCurrency(normalized.CurrencyCode); err != nil {
		return models.Order{}, err
	}

	created, err := s.writeOrder(ctx, normalized, rule)
	if err != nil {
		if replay, ok := s.replayedOrder(ctx, normalized.IdempotencyKey, err); ok {
			return replay, nil
		}
		return models.Order{}, err
	}

	s.publishOrderPlaced(ctx, created, len(normalized.Items))
	return created, nil
}

// writeOrder writes the order, its lines and its summary in a SINGLE
// transaction.
//
// The three cannot be separated: an order without lines would be "an order in
// which nothing was sold", and an order without a summary would be a record on
// which the reader is forced to tell NULL apart from zero. If any step of the
// transaction fails, none of them is written.
//
// The order NUMBER is validated here too, INSIDE THE TRANSACTION. The number
// comes from the IDENTITY column of the database; a zero or negative value
// means that the column or the sequence is broken and it means an order the
// customer will not be able to find anywhere. The check being inside the
// transaction makes sure that such an order is not VISIBLE even for an instant:
// the commit never happens.
//
// # The spending limit is applied here as well
//
// The FIRST job of the transaction is the spending limit
// ([Service.enforceSpendingLimit]) and this is mandatory: the limit looks at a
// SUM that also covers this order, which is not written yet. Had the check been
// done outside the transaction, two concurrent orders would read the sum at the
// same time, both would look below the limit and both would be written.
func (s *Service) writeOrder(ctx context.Context, in CreateOrderInput, rule spendingRule) (models.Order, error) {
	var created models.Order

	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.enforceSpendingLimit(ctx, rule, in); err != nil {
			return err
		}

		order, err := s.store.CreateOrder(ctx, models.Order{
			ID:             models.NewOrderID(),
			Status:         models.OrderPending,
			RegionID:       in.RegionID,
			CustomerID:     in.CustomerID,
			Email:          in.Email,
			CurrencyCode:   in.CurrencyCode,
			CartID:         in.CartID,
			IdempotencyKey: in.IdempotencyKey,
			Subtotal:       in.Subtotal,
			DiscountTotal:  in.DiscountTotal,
			TaxTotal:       in.TaxTotal,
			ShippingTotal:  in.ShippingTotal,
			Total:          in.Total,
			Metadata:       in.Metadata,
		})
		if err != nil {
			return err
		}
		if !models.ValidDisplayID(order.DisplayID) {
			return errors.Internal(CodeDisplayIDInvalid,
				"the order did not receive a usable number (display_id=%d); the write was rolled back: %s",
				order.DisplayID, order.ID)
		}

		// The loop is walked by index: the line input is large and copying it by
		// value would carry a few hundred bytes for nothing on every turn.
		for i := range in.Items {
			if _, err := s.store.CreateLineItem(ctx, models.OrderLineItem{
				ID:            models.NewLineItemID(),
				OrderID:       order.ID,
				VariantID:     in.Items[i].VariantID,
				Title:         in.Items[i].Title,
				Quantity:      in.Items[i].Quantity,
				UnitPrice:     in.Items[i].UnitPrice,
				Subtotal:      in.Items[i].Subtotal,
				DiscountTotal: in.Items[i].DiscountTotal,
				TaxTotal:      in.Items[i].TaxTotal,
				Total:         in.Items[i].Total,
				Metadata:      in.Items[i].Metadata,
			}); err != nil {
				return err
			}
		}

		if _, err := s.store.CreateSummary(ctx, models.OrderSummary{
			ID:      models.NewSummaryID(),
			OrderID: order.ID,
		}); err != nil {
			return err
		}

		created = order
		return nil
	})
	if err != nil {
		return models.Order{}, err
	}
	return created, nil
}

// replayedOrder returns the existing order of an idempotent call that lost the
// race.
//
// The scenario is this: two calls arrive with the same key at the same time,
// neither finds a record in the lookup of the cheap path, both attempt to write
// and the unique index of the database rejects the second one. It would be
// wrong for the rejected call to return an error — the requested order EXISTS
// and that is exactly the promise of the key.
//
// The criterion is not the error CODE but its CLASS (Conflict), and the key is
// additionally required to really correspond to an order. The reason the code is
// not used is the separation of layers: the package that produces the code is
// the repository, an implementation the service DOES NOT import (the ADR 0001
// pattern; the store is only seen through the [Store] interface). Reading by the
// key is a definitive criterion anyway — if the record exists, that is the order
// the call wanted; if it does not, the original error is passed up as it is.
//
// The criterion being the CLASS also works correctly with the spending limit:
// the call that loses the race sees, under the lock, a sum that now counts the
// winner's order too and it may fall with a limit overrun instead of a
// uniqueness violation. Both are Conflicts and in both the right answer is the
// same — the order the key promised has been written.
func (s *Service) replayedOrder(ctx context.Context, key string, cause error) (models.Order, bool) {
	if key == "" || !errors.IsConflict(cause) {
		return models.Order{}, false
	}
	existing, err := s.store.GetOrderByIdempotencyKey(ctx, key)
	if err != nil {
		return models.Order{}, false
	}
	s.log.InfoContext(ctx, "a concurrent idempotent call lost the race, returning the existing record",
		"order_id", existing.ID, "display_id", existing.DisplayID)
	return existing, true
}

// GetOrder returns the order together with its lines and its summary.
//
// The children are fetched with TWO fixed queries; whatever the number of lines
// is, the number of queries does not change (there is no N+1). If the order does
// not exist or is soft deleted, errors.NotFound is returned.
//
// The three queries run on a SINGLE SNAPSHOT ([Store.WithReadTx]); no lock is
// taken. The only thing that is guaranteed is that all three see the SAME state
// of the order: without a transaction the header could carry the NEW status
// while the summary showed the OLD amounts.
func (s *Service) GetOrder(ctx context.Context, orderID string) (models.OrderDetail, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.OrderDetail{}, err
	}
	return s.loadDetail(ctx, func(ctx context.Context) (models.Order, error) {
		return s.store.GetOrder(ctx, orderID)
	})
}

// GetOrderByDisplayID returns the order by its human readable number, together
// with its lines and its summary.
//
// It is the entry gate of the support flow: what the customer holds is not the
// identifier but the number.
func (s *Service) GetOrderByDisplayID(ctx context.Context, displayID int64) (models.OrderDetail, error) {
	if !models.ValidDisplayID(displayID) {
		return models.OrderDetail{}, errors.Invalid(CodeInvalidInput,
			"display_id has to be at least %d: %d", models.MinDisplayID, displayID)
	}
	return s.loadDetail(ctx, func(ctx context.Context) (models.Order, error) {
		return s.store.GetOrderByDisplayID(ctx, displayID)
	})
}

// loadDetail reads the order and its children on a single snapshot.
//
// The query that finds the order is a parameter because that is the only
// difference: reading by the identifier and reading by the number do the same
// child queries at the same isolation level. Writing the two separately would
// invite the classic divergence where a field added to one is forgotten in the
// other.
func (s *Service) loadDetail(ctx context.Context, find func(ctx context.Context) (models.Order, error)) (models.OrderDetail, error) {
	var detail models.OrderDetail

	err := s.store.WithReadTx(ctx, func(ctx context.Context) error {
		order, err := find(ctx)
		if err != nil {
			return err
		}
		items, err := s.store.ListLineItems(ctx, order.ID)
		if err != nil {
			return err
		}
		summary, err := s.store.GetSummary(ctx, order.ID)
		if err != nil {
			return err
		}
		detail = models.OrderDetail{Order: order, Items: items, Summary: summary}
		return nil
	})
	if err != nil {
		return models.OrderDetail{}, err
	}
	return detail, nil
}

// ListOrdersInput is the input of the order listing.
type ListOrdersInput struct {
	// CustomerID, when given, returns only the orders of that customer.
	CustomerID *string
	// RegionID, when given, returns only the orders of that region.
	RegionID *string
	// Status, when given, filters the orders by status.
	Status *models.OrderStatus
	// Page holds the pagination parameters.
	Page Page
}

// ListOrders returns the orders in pages (WITHOUT LOADING their children).
//
// The second return value is the count of ALL the rows matching the filter. The
// lines are not loaded here: fetching the children of dozens of orders per page
// would make the list heavy and open to N+1. The children only come with
// [Service.GetOrder].
func (s *Service) ListOrders(ctx context.Context, in ListOrdersInput) ([]models.Order, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}

	filter := models.OrderFilter{Limit: page.Limit, Offset: page.Offset}
	if in.CustomerID != nil {
		if err := requireID("customer_id", *in.CustomerID); err != nil {
			return nil, 0, err
		}
		filter.CustomerID = in.CustomerID
	}
	if in.RegionID != nil {
		if err := requireID("region_id", *in.RegionID); err != nil {
			return nil, 0, err
		}
		filter.RegionID = in.RegionID
	}
	if in.Status != nil {
		if !in.Status.Valid() {
			return nil, 0, errors.Invalid(CodeInvalidInput,
				"undefined order status: %q", in.Status.String())
		}
		filter.Status = in.Status
	}
	return s.store.ListOrders(ctx, filter)
}

// ListOrdersByIDs returns the orders of the given identifiers in a SINGLE
// query. No record is returned for an identifier that is not found; that is not
// an error.
func (s *Service) ListOrdersByIDs(ctx context.Context, ids []string) ([]models.Order, error) {
	if len(ids) == 0 {
		return []models.Order{}, nil
	}
	return s.store.OrdersByIDs(ctx, ids)
}

// CancelOrder cancels the order and IS A SAGA COMPENSATION; it is IDEMPOTENT.
//
// # Why idempotent
//
// This method is the Compensate of the create_order step of the complete_cart
// saga and a saga can rerun a compensation (plan Section 2.6, the "best effort
// compensation" behavior of core/workflow). Returning an error on an order that
// is already canceled would mean that the second round of the compensation made
// the whole saga look failed — whereas the desired state has ALREADY been
// reached. That is why the second call succeeds silently and is logged at the
// DEBUG level.
//
// The reason of the first cancellation is PRESERVED; the reason of the second
// call is not written. The cancellation REALLY happened on the first call and
// the reason is the record of that moment; overwriting it would mean presenting
// the repetition of the compensation as the reason for the cancellation.
//
// # Why a completed order is a Conflict
//
// The payment of a completed (or archived) order has been collected and the
// shipping decision has been made. Closing it with a "canceled" stamp would turn
// a collected amount into an amount that is not tied to any order; the right way
// is a return/exchange ([Service.CreateReturn]), not a cancellation. Besides,
// the saga only compensates the order it created ITSELF, which is still
// 'pending': arriving here with a completed order means that the world is
// different from what the saga assumed and that must not be swallowed silently,
// it has to be VISIBLE.
func (s *Service) CancelOrder(ctx context.Context, orderID, reason string) error {
	if err := requireID("order_id", orderID); err != nil {
		return err
	}
	if err := checkTextLen("reason", reason); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.store.LockOrder(ctx, orderID)
		if err != nil {
			return err
		}

		switch order.Status {
		case models.OrderCanceled:
			s.log.DebugContext(ctx, "the order is already canceled, nothing was done",
				"order_id", orderID, "display_id", order.DisplayID)
			return nil
		case models.OrderCompleted, models.OrderArchived:
			return errors.Conflict(CodeNotPending,
				"a completed order cannot be canceled (%s, status: %s); the return/exchange path has to be used",
				orderID, order.Status)
		case models.OrderPending:
			// Handled below.
		default:
			return errors.Internal(CodeInconsistentState,
				"unknown order status %q (%s)", order.Status, orderID)
		}

		_, err = s.store.CancelOrder(ctx, orderID, reason)
		return err
	})
}

// CompleteOrder stamps the order as completed.
//
// # Why the second call is a Conflict
//
// Unlike [Service.CancelOrder] this method is NOT A COMPENSATION but a forward
// step, and it does not need to be idempotent. Had completing an already
// completed order been counted as a silent success, a flow in which the same
// order is closed twice would produce an error nowhere. Retry safety is solved
// not here but in the idempotency key of the workflow engine: a step that ends
// SUCCESSFULLY is not run again. The same rationale holds in the cart module's
// MarkCompleted as well; the behavior of the two modules on this subject is
// deliberately the same.
//
// A canceled order cannot be completed either: a cancellation is a TERMINAL
// state (see the transition diagram in the models document).
func (s *Service) CompleteOrder(ctx context.Context, orderID string) (models.Order, error) {
	return s.transition(ctx, orderID, models.OrderPending, "completion",
		func(ctx context.Context, id string) (models.Order, error) {
			return s.store.CompleteOrder(ctx, id)
		})
}

// ArchiveOrder takes a completed order into the archive.
//
// Archiving does not undo the completedness of the order; it only takes it out
// of the daily lists and does not touch the [models.Order.CompletedAt] stamp.
// Archiving an order that is not completed is rejected: making a job that has
// not been closed yet invisible would be the easiest way for it to be
// forgotten.
func (s *Service) ArchiveOrder(ctx context.Context, orderID string) (models.Order, error) {
	return s.transition(ctx, orderID, models.OrderCompleted, "archiving",
		func(ctx context.Context, id string) (models.Order, error) {
			return s.store.ArchiveOrder(ctx, id)
		})
}

// transition is the COMMON FRAME of the status transitions.
//
// In order: open a single transaction -> LOCK the order -> check whether it is
// in the expected status -> apply the transition. The frame being in a single
// place guarantees that no transition path can skip the lock or the status
// check.
//
// [Service.CancelOrder] DOES NOT USE this frame, because it carries the only
// exception: returning SUCCESS instead of an error on an order that is already
// in the target status (an idempotent compensation). Carrying that difference
// into the frame with a flag would force the reader to track the value of the
// flag at every call site.
func (s *Service) transition(
	ctx context.Context,
	orderID string,
	required models.OrderStatus,
	action string,
	apply func(ctx context.Context, id string) (models.Order, error),
) (models.Order, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.Order{}, err
	}

	var updated models.Order
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.store.LockOrder(ctx, orderID)
		if err != nil {
			return err
		}
		if !order.Status.Valid() {
			return errors.Internal(CodeInconsistentState,
				"unknown order status %q (%s)", order.Status, orderID)
		}
		if order.Status != required {
			return transitionError(action, orderID, required, order.Status)
		}
		updated, err = apply(ctx, orderID)
		return err
	})
	if err != nil {
		return models.Order{}, err
	}
	return updated, nil
}

// transitionError is the typed error of a transition attempt in an unexpected
// status.
//
// The code is chosen according to which status was EXPECTED: the client has to
// be able to tell "the order is not in the pending status" apart from "the
// order is not completed yet".
func transitionError(action, orderID string, required, actual models.OrderStatus) error {
	code := CodeNotPending
	if required == models.OrderCompleted {
		code = CodeNotCompleted
	}
	return errors.Conflict(code,
		"%s cannot be applied: the order has to be in the %q status, it is in the %q status (%s)",
		action, required, actual, orderID)
}
