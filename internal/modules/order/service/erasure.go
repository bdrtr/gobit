package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/bdrtr/gobit/core/erasure"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// This file is the order module's answer to an erasure request (ADR 0029,
// core/erasure).
//
// # The answer is ANONYMIZED, with one bounded refusal
//
// The order does not delete and does not refuse outright. Deleting is not
// available: the lines, the totals and the status are what a sale IS, they are
// read by the spending rule, by every report and by the invoice that was drawn
// from them, and a shop whose past year evaporates because a customer asked to
// be forgotten has lost its own books rather than the person's data. Refusing
// outright is not available either, and ADR 0032 is the reason it LOOKS like it
// might be: the invoice refuses in the schema, on three arguments, and not one
// of them reaches this record. The invoice's buyer_name is NOT NULL while these
// address columns are nullable TEXT with no CHECK; ADR 0024's immutability
// clause binds the invoice NUMBER; and "an order is a commercial record" is a
// retention judgement, which ADR 0029 lists by name among the things gobit does
// NOT owe. What is left is the answer that keeps the sale and drops the person:
// anonymize.
//
// The one refusal is bounded by a measurable fact rather than by a period
// somebody picked — see [models.OrderErasureCandidate.Unsettled].
//
// # Why the report has to name what stayed
//
// gobit never rewrites a free-form column. ADR 0029 leaves the judgement of
// whether a metadata blob holds personal data in a given deployment with the
// controller, and a framework that read a shop's own notes in order to classify
// them would have taken that judgement back. The consequence is that
// "anonymized" is only honest if the answer SAYS which columns were left, which
// is what [erasure.Result.Kept] carries and why [personalColumns] is a single
// table that produces both the declaration and the kept list: two lists written
// separately drift, and the day they drift the module reports a column it did
// not look at as clean.

// ErasureHolder is the name this module answers an erasure request under.
//
// It has to stay equal to order.ModuleName and it is a SECOND string because
// this package cannot import the package that wires it (the ADR 0001
// direction); the repetition is the price the module's other cross-package
// name constants pay for the same isolation. It is defined in terms of [EntityName] so that the module's
// name is written once in this package, and the sweep overwrites the field with
// the registry's name anyway (internal/workflows/erasing) — this value is what
// a caller holding the service directly gets.
const ErasureHolder = EntityName

// The tables the declaration names.
//
// They are spelled once because the declaration repeats them and a mistyped
// table name in a declaration is not a compile error — it is an auditor sent to
// a table that does not exist. [OrderListing] happens to hold the same
// characters as [tableOrders] and is deliberately not reused: it names the
// scope of a pagination cursor, and two meanings behind one constant is how the
// day comes that renaming one breaks the other.
const (
	tableOrders         = "orders"
	tableOrderLineItems = "order_line_items"
	tableOrderAddresses = "order_addresses"
	tableOrderReturns   = "order_returns"
	tableOrderExchanges = "order_exchanges"
	tableOrderClaims    = "order_claims"
)

// The column names that appear on more than one table.
//
// Six of this module's eight tables carry a metadata jsonb; order_summaries and
// order_return_items carry none, and that is half of why the declaration below
// leaves those two tables out entirely — nobody can have typed anything into a
// table that has no open column. Three tables carry a free note and two a typed
// reason; naming the columns once keeps the declaration's rows short enough to
// read as a table.
const (
	columnMetadata = "metadata"
	columnNote     = "note"
	columnReason   = "reason"
)

// personalColumn is one declared place this module keeps personal data, plus
// whether the erasure rewrites it.
//
// The two facts sit in one row on purpose. The module's PersonalData answers "where
// could this person be" and [erasure.Result.Kept] answers "where could they
// still be afterwards"; the second is the first minus the columns the
// anonymizing statements null. Kept as separate lists they would agree on the
// day they were written and diverge on the day a column was added to one of
// them — and the failure would be silent, because the report would still look
// complete.
//
// What this table CANNOT prove is that the erased flags match the SQL in
// queries/erasure.sql. Nothing in Go can: the statements are text. That gap is
// closed by the integration test, which re-reads every declared column that is
// not in the kept list and fails if the database still holds a value.
type personalColumn struct {
	// holding is what the declaration says about the column.
	holding erasure.Holding
	// erased reports that the anonymizing statements set the column to NULL.
	erased bool
}

// personalColumns is every place the order module keeps personal data.
//
// # What is deliberately NOT here
//
//   - order_line_items.title and unit_price: a catalog copy and a price. What
//     was sold is a fact about the sale, not about the buyer.
//   - orders.region_id, orders.cart_id, order_returns.received_location_id:
//     identifiers of records in OTHER modules — a region, a cart, a warehouse.
//     Each of those modules answers for its own rows, and a cart is discarded
//     or reused after checkout. orders.customer_id IS declared, and the
//     difference is what the id points at: the customer id is the installation's
//     stable handle for the PERSON, while a cart id names one shopping session.
//   - order_addresses.address_type, orders.display_id and every amount and
//     stamp: they describe the sale.
//   - order_summaries and order_return_items in their entirety: they carry
//     money and quantities and nothing that names anybody. Saying so is worth
//     more than saying nothing, which is why they are named here rather than
//     silently absent.
//
// The order of the entries is the order they appear in the report, so it is
// stable and readable: table by table, in the order the migrations created the
// tables.
var personalColumns = []personalColumn{
	{
		holding: erasure.Holding{
			Table: tableOrders, Column: "customer_id", Kind: erasure.Named,
			Why: "the customer module's identifier for the buyer; a guest order has none",
		},
		// It is the ONLY indexed handle this module has (orders_customer_idx).
		// Nulling it would leave the second sweep — the one idempotence
		// requires to answer the same thing — unable to find the rows it
		// already erased.
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrders, Column: "email", Kind: erasure.Named,
			Why: "the address the buyer gave; on a guest order it is the only handle to them",
		},
		erased: true,
	},
	{
		holding: erasure.Holding{
			Table: tableOrders, Column: "cancel_reason", Kind: erasure.Open,
			Why: "free text written when the order was canceled; it may quote or name the buyer",
		},
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrders, Column: columnMetadata, Kind: erasure.Open,
			Why: "the caller's own data on the order; gobit does not look inside it",
		},
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrders, Column: "idempotency_key", Kind: erasure.Open,
			Why: "the caller's own replay handle for the order; gobit neither builds it nor reads what is in it",
		},
		// It is declared Open rather than left out because the value is the
		// CALLER'S text and not this module's: the workflow that places the
		// order chooses it, [normalizeCreateOrder] only checks that it is
		// non-empty, unpadded and within the identifier length, and the interop
		// snapshot passes whatever JSON carried through unchanged
		// ([interopSnapshot]). A shop free to write "checkout-<the buyer's
		// e-mail>" into it is exactly the case erasure.Open exists for, and ADR
		// 0029 leaves the judgement of what such a field holds with the
		// controller.
		//
		// It is not erased for the reason it exists: orders_idempotency_key_uniq
		// is what makes a retried saga step return the order it already opened,
		// and a NULL there is outside the partial index — a retry arriving after
		// the erasure would open a SECOND order for a person who asked to be
		// forgotten.
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderLineItems, Column: columnMetadata, Kind: erasure.Open,
			Why: "the caller's own data on a line — a personalisation, an engraving, a gift note",
		},
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderAddresses, Column: "source_address_id", Kind: erasure.Named,
			Why: "which entry of the buyer's address book this copy was taken from",
		},
		erased: true,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderAddresses, Column: "first_name", Kind: erasure.Named,
			Why: "the buyer's given name as it was written on the order",
		},
		erased: true,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderAddresses, Column: "last_name", Kind: erasure.Named,
			Why: "the buyer's family name as it was written on the order",
		},
		erased: true,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderAddresses, Column: "company", Kind: erasure.Named,
			Why: "the company on the address; a one-person business is a person",
		},
		erased: true,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderAddresses, Column: "address_1", Kind: erasure.Named,
			Why: "the street the order was shipped to or billed to",
		},
		erased: true,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderAddresses, Column: "address_2", Kind: erasure.Named,
			Why: "the rest of the street address — the building, the floor, the flat",
		},
		erased: true,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderAddresses, Column: "city", Kind: erasure.Named,
			Why: "the city of the address",
		},
		erased: true,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderAddresses, Column: "province", Kind: erasure.Named,
			Why: "the province or district of the address",
		},
		erased: true,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderAddresses, Column: "postal_code", Kind: erasure.Named,
			Why: "the postal code, which in a small district reaches a household on its own",
		},
		erased: true,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderAddresses, Column: "country_code", Kind: erasure.Named,
			Why: "the country the order went to; it is the one address column the erasure keeps",
		},
		// The row itself has to survive — an absent address row already means
		// "this order never had one", and a shop selling a download writes
		// none — so what is left has to stay readable AS an address. A country
		// on its own does not reach a person.
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderAddresses, Column: "phone", Kind: erasure.Named,
			Why: "the number given for the delivery",
		},
		erased: true,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderAddresses, Column: columnMetadata, Kind: erasure.Open,
			Why: "the caller's own data on the address — delivery instructions are typed here",
		},
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderReturns, Column: columnReason, Kind: erasure.Open,
			Why: "why the goods came back, in whoever's words opened the record",
		},
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderReturns, Column: columnNote, Kind: erasure.Open,
			Why: "a free note on the return; an operator writes what the customer said here",
		},
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderReturns, Column: columnMetadata, Kind: erasure.Open,
			Why: "the caller's own data on the return",
		},
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderExchanges, Column: columnNote, Kind: erasure.Open,
			Why: "a free note on the exchange request",
		},
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderExchanges, Column: columnMetadata, Kind: erasure.Open,
			Why: "the caller's own data on the exchange",
		},
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderClaims, Column: columnReason, Kind: erasure.Open,
			Why: "what the claim is about — damage or shortage — in free text",
		},
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderClaims, Column: columnNote, Kind: erasure.Open,
			Why: "a free note on the claim",
		},
		erased: false,
	},
	{
		holding: erasure.Holding{
			Table: tableOrderClaims, Column: columnMetadata, Kind: erasure.Open,
			Why: "the caller's own data on the claim",
		},
		erased: false,
	},
}

// PersonalDataHoldings returns everything this module says it keeps about
// people.
//
// The module type turns it into an [erasure.Declaration]; the list lives here
// because the erasure that acts on it lives here, and a declaration written in
// a second place is a declaration that can disagree with the code that erases.
//
// A fresh slice is returned on every call: the declaration is read by an audit
// that has no reason to be careful with it, and handing out the package's own
// slice would let one caller's append reach every later one.
func PersonalDataHoldings() []erasure.Holding {
	out := make([]erasure.Holding, 0, len(personalColumns))
	for i := range personalColumns {
		out = append(out, personalColumns[i].holding)
	}

	return out
}

// keptAfterAnonymize lists the "table.column" entries an anonymized order still
// holds.
func keptAfterAnonymize() []string {
	out := make([]string, 0, len(personalColumns))
	for i := range personalColumns {
		if personalColumns[i].erased {
			continue
		}
		out = append(out, columnPath(personalColumns[i].holding))
	}

	return out
}

// keptWhenRetained lists every declared column, which is what a retained order
// still holds: nothing was rewritten on it.
func keptWhenRetained() []string {
	out := make([]string, 0, len(personalColumns))
	for i := range personalColumns {
		out = append(out, columnPath(personalColumns[i].holding))
	}

	return out
}

// columnPath spells one holding the way [erasure.Result.Kept] wants it.
func columnPath(h erasure.Holding) string { return h.Table + "." + h.Column }

// whyAnonymized explains the kept list of an anonymized answer.
//
// It is one sentence in three clauses because the list has three groups, and a
// controller repeating this to a data subject has to be able to say what each
// group is. The free-form clause is the one that must not be dropped: without
// it "anonymized" would cover the THIRTEEN columns of [personalColumns] that
// are declared [erasure.Open] and that no statement rewrites. It is not to be
// confused with the other count in this file: eleven is how many columns the
// erasure nulls.
const whyAnonymized = "the name, address, phone and e-mail on the order were set to NULL, together " +
	"with the pointer that recorded which entry of the buyer's address book the order address was " +
	"copied from, and what is " +
	"listed stayed: gobit never rewrites a free-form column, because ADR 0029 leaves the judgement of " +
	"whether a metadata blob or a typed note holds personal data in this deployment with the " +
	"controller; orders.customer_id stayed because it is the only indexed handle by which a repeated " +
	"request can find these rows again, and it reaches the person only through the customer module's " +
	"record, which answers the same request on its own account; order_addresses.country_code stayed so " +
	"that the surviving row is still readable as an address the order HAD, which a deleted row could " +
	"not be, and a country does not identify anybody on its own."

// Erase answers an erasure request about one person (ADR 0029).
//
// # What it does
//
// In a SINGLE transaction it finds the person's orders, decides for each one
// whether it is settled ([models.OrderErasureCandidate.Unsettled]), and sets
// the name, the address, the phone, the address-book pointer and the e-mail of
// every settled one to NULL. The row, the lines, the totals, the status, the
// number, the country and the customer id survive: a sale still has to add up
// after its buyer is forgotten.
//
// # It is idempotent, and where that has a limit
//
// A second call returns the same outcome. The anonymizing statements are
// unconditional over the same set of orders, so they rewrite the same rows to
// the same values and report the same count, and the stamp keeps the moment of
// the FIRST erasure (migration 000009).
//
// The limit is worth stating plainly, because it is a property of the data and
// not of this code: for a GUEST order — one with no customer id — the e-mail is
// the only handle, and erasing it is erasing the handle. A second call for such
// a subject finds nothing, so it answers [erasure.Anonymized] with zero rows
// and an empty kept list. The outcome is unchanged, which is what the contract
// requires; the FIRST report is the one that names what stayed, and the
// controller has to keep it.
//
// # Retained
//
// If any of the person's orders is still being performed, the answer is
// [erasure.Retained] and the reason names the order and the fact — pending, an
// outstanding amount, or an open return, exchange or claim. The person's
// SETTLED orders are still anonymized in the same transaction; retaining
// everything because one order is open would keep more than the fact justifies.
//
// An order that has already been erased is never retained again, whatever
// happens to it afterwards; the argument is at the branch that decides it.
//
// # The subject is never logged
//
// The log line carries counts and the outcome and no identifier of the person.
// Writing the e-mail of somebody who asked to be forgotten into a log would put
// it back into the installation through the one door the erasure does not
// reach.
func (s *Service) Erase(ctx context.Context, subject erasure.Subject) (erasure.Result, error) {
	customerID, email, err := normalizeSubject(subject)
	if err != nil {
		return erasure.Result{}, err
	}

	var (
		rows       int64
		anonymized int
		unsettled  int
		// held is the first unsettled order, in the id order the query locks
		// them in, so the sentence is the same on every run.
		held     models.OrderErasureCandidate
		heldFact models.UnsettledFact
	)

	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		candidates, err := s.store.OrdersForErasure(ctx, customerID, email)
		if err != nil {
			return err
		}

		ids := make([]string, 0, len(candidates))
		// By index: the candidate carries twelve fields and copying it per turn
		// would move them for nothing.
		for i := range candidates {
			fact := candidates[i].Unsettled()
			// An order that was ALREADY erased is never retained, however
			// unsettled it has since become. The carve-out exists to keep an
			// order that is still being performed in possession of the contact
			// it is performed with; on a row whose contact is already NULL
			// there is nothing left to keep, and answering Retained for it
			// would report data this module no longer holds. The path is
			// reachable: an operator can open a return on an order months
			// after its buyer was forgotten, which flips a settled order back
			// to unsettled.
			if fact != models.Settled && candidates[i].ErasedAt == nil {
				unsettled++
				if unsettled == 1 {
					held, heldFact = candidates[i], fact
				}

				continue
			}
			ids = append(ids, candidates[i].OrderID)
		}
		if len(ids) == 0 {
			return nil
		}

		contacts, err := s.store.AnonymizeOrderContacts(ctx, ids)
		if err != nil {
			return err
		}
		addresses, err := s.store.AnonymizeOrderAddresses(ctx, ids)
		if err != nil {
			return err
		}

		rows = contacts + addresses
		anonymized = len(ids)

		return nil
	})
	if err != nil {
		return erasure.Result{}, err
	}

	result := erasureResult(rows, anonymized, unsettled, held, heldFact)
	s.log.InfoContext(ctx, "an erasure request was answered",
		"holder", ErasureHolder, "outcome", string(result.Outcome),
		"anonymized_orders", anonymized, "retained_orders", unsettled, "rows", result.Rows)

	return result, nil
}

// erasureResult turns the counts into the answer the controller reads.
//
// It is split out of [Service.Erase] because it is the part with no database in
// it: given the four numbers the outcome, the kept list and the sentence are
// decided by rules, and rules that can be read without a transaction around
// them are rules that can be checked.
func erasureResult(
	rows int64, anonymized, unsettled int,
	held models.OrderErasureCandidate, fact models.UnsettledFact,
) erasure.Result {
	result := erasure.Result{Holder: ErasureHolder, Rows: int(rows)}

	switch {
	case unsettled > 0:
		result.Outcome = erasure.Retained
		result.Kept = keptWhenRetained()
		result.Why = whyRetained(held, fact, unsettled, anonymized)
	case rows == 0:
		// The person had nothing here. The kept list stays EMPTY rather than
		// listing the free-form columns: those columns hold nothing about
		// somebody who never bought anything, and a report naming them would
		// send a controller looking through rows that do not exist.
		result.Outcome = erasure.Anonymized
	default:
		result.Outcome = erasure.Anonymized
		result.Kept = keptAfterAnonymize()
		result.Why = whyAnonymized
	}

	return result
}

// whyRetained writes the sentence a controller repeats to the data subject.
//
// It names the ORDER NUMBER and the fact, because "retained" with no reason is
// useless to whoever has to answer: the person is owed something they can check
// and, by implication, the point at which to ask again.
//
// The kept list of a retained answer is EVERY declared column
// ([keptWhenRetained]) and the sentence has to split it in two, because the two
// halves have different futures. Eleven of the columns are the ones the
// anonymizing statements null, and those really do stay only until the order
// settles. The other fifteen are the free-form columns gobit never rewrites,
// plus orders.customer_id and order_addresses.country_code, which the erasure
// keeps on purpose; settlement changes nothing for them. Saying "everything
// listed stays until it settles" would promise the person that thirteen
// metadata blobs and notes disappear on a day that will never come — the same
// over-claim in the opposite direction to the one [whyAnonymized]'s free-form
// clause exists to prevent.
func whyRetained(
	held models.OrderErasureCandidate, fact models.UnsettledFact, unsettled, anonymized int,
) string {
	var b strings.Builder

	fmt.Fprintf(&b, "order %d was kept in full because %s", held.DisplayID, fact)
	if fact == models.UnsettledOutstanding {
		fmt.Fprintf(&b, " (%d %s, in minor units)", held.Owed(), held.CurrencyCode)
	}
	b.WriteString("; an order that is still being performed is performed WITH the contact and the " +
		"address on it, so the name, the address, the phone, the e-mail and the address-book pointer " +
		"listed stay until it settles and are set to NULL then; the rest of the list stays whatever " +
		"happens to the order, because gobit never rewrites a free-form column (ADR 0029 leaves the " +
		"judgement of whether a metadata blob or a typed note holds personal data in this deployment " +
		"with the controller), and because orders.customer_id is the only indexed handle by which a " +
		"repeated request finds these rows again while order_addresses.country_code is what keeps the " +
		"surviving row readable as an address the order HAD")

	if unsettled > 1 {
		fmt.Fprintf(&b, ", and %d further unsettled %s of the same person %s kept for the same reason",
			unsettled-1, orderNoun(unsettled-1), pastTenseOfBe(unsettled-1))
	}
	if anonymized > 0 {
		fmt.Fprintf(&b, "; the person's %d settled %s %s anonymized in the same transaction",
			anonymized, orderNoun(anonymized), pastTenseOfBe(anonymized))
	}
	b.WriteString(".")

	return b.String()
}

// orderNoun agrees the noun with the count so the sentence reads.
//
// It is worth the two lines because the sentence is read by a PERSON who asked
// to be forgotten, not by a machine: "1 settled orders were anonymized" is the
// kind of wording that makes a reader doubt the number as well.
func orderNoun(n int) string {
	if n == 1 {
		return "order"
	}

	return "orders"
}

// pastTenseOfBe agrees the verb with the count, for the reason [orderNoun]
// exists.
func pastTenseOfBe(n int) string {
	if n == 1 {
		return "was"
	}

	return "were"
}

// normalizeSubject validates the subject and puts its identifiers into the form
// the columns hold.
//
// The e-mail is folded to lower case with [normalizeEmail], the same function
// that folded it on the way IN: the column holds what that function produced,
// so matching with anything else would silently miss the rows. An address that
// cannot pass it is rejected rather than run as a filter that can only match
// nothing — a subject that reaches this module malformed is a caller's mistake,
// and answering "anonymized, zero rows" to it would report an erasure that
// never looked anywhere.
//
// A subject with NO identifier is refused. Erasing everybody is not an erasure
// request; the sweep refuses it first (internal/workflows/erasing) and this is
// the last defense, because a holder reached directly would otherwise take a
// zero-value subject and lock every order in the installation.
func normalizeSubject(subject erasure.Subject) (customerID, email string, err error) {
	if err := optionalID("customer_id", subject.CustomerID); err != nil {
		return "", "", err
	}
	email, err = normalizeEmail(subject.Email)
	if err != nil {
		return "", "", err
	}
	if subject.CustomerID == "" && email == "" {
		return "", "", errors.Invalid(CodeErasureSubjectEmpty,
			"an erasure request has to name somebody: give a customer id, an e-mail address, or both")
	}

	return subject.CustomerID, email, nil
}
