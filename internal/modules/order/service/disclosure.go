package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// This file is the order module's answer to "what do you hold about this
// person" (ADR 0034, core/personaldata).
//
// # Why the module owes the read as well as the write
//
// It already knows how to find her. queries/erasure.sql resolves a person by
// customer id OR e-mail, deliberately including the guest who has only the
// second, and every line of that resolution is wired to a statement that sets
// her columns to NULL. Without this file the module would destroy her orders'
// address on request and refuse to show it to her — and that asymmetry is a
// fact about the code, not a policy anybody chose. ADR 0034 is the record of
// choosing the other way.
//
// # The field list is DERIVED, so the two answers cannot drift
//
// A disclosure lists EXACTLY the columns [personalColumns] declares, and it does
// not list them by being written out a second time: [recordOf] walks the
// declaration for the table it is building and asks [disclosureValues] for each
// declared column's value. Reaching PAST the declaration would hand the person
// data the declaration told the controller was not there; stopping SHORT of it
// would contradict the same document. Both mistakes are unreachable here — the
// one thing that can go wrong is a declared column with no way to read it, and
// that is refused loudly rather than dropped quietly, because a dossier missing
// a field looks exactly like a dossier of somebody who has no such field.
//
// The Kind travels ON the field for the reason ADR 0034 gives: most of this
// module's holdings are [personaldata.Open] free text — the metadata blobs, the
// cancellation reason, the reason, note and caption columns of the after-sales
// records — and whoever reads the dossier has to know which values gobit can
// vouch for and which are the embedder's own, unread.
//
// # An order that was already erased is still disclosed
//
// After an erasure the order's rows are STILL THERE with their personal columns
// nulled, and a disclosure of such an order reports the records with their
// emptied fields rather than answering that there is nothing. Three reasons, in
// the order they decided it:
//
//  1. The question is what the database still HOLDS. The row exists, the shop's
//     own tables carry it, and a person told "we have nothing" about a row that
//     is in front of her is being told something false about a record that is
//     hers.
//  2. It is the only way she can CHECK the erasure. A dossier that lists
//     orders.email with no value says the column exists, was searched, and is
//     empty; a dossier that omits it is indistinguishable from one that never
//     looked.
//  3. What survives an erasure is not nothing. orders.customer_id and
//     order_addresses.country_code stay by decision (see [whyAnonymized]), and
//     every free-form column stays because gobit does not rewrite one — so an
//     "already erased" order is exactly where the remaining free text lives.
//
// # Which rows become records, and why the rule is read off the declaration
//
// One record per row, and a row is disclosed when its table declares anything
// the framework itself writes the person into, or when the row actually holds
// something. That single sentence is [rowIsDisclosed] and it is derived, not
// listed: orders and order_addresses have [personaldata.Named] holdings, so
// their rows are disclosed whether or not anything is left in them — which is
// the erased-order case above. The line, the return, the exchange and the claim
// declare nothing but free text, so a row where every declared column is empty
// holds nothing about anybody, and emitting it would bury the one note that has
// content under twenty records that say nothing. The same holds for the
// replacement, the credit, the claim's evidence and the line's cancellation,
// declared since D188. The day somebody declares a
// Named column on order_returns, its rows start being disclosed unconditionally
// with no edit here.
//
// # What this must never become
//
// It is a READ. It takes no lock, writes nothing, has no side effect, and the
// one transaction it opens is [Store.WithReadTx] — read-only, lock-free, and
// there only so that the queries of one dossier see ONE instant. Without it
// an erasure landing between the second query and the fifth would produce a
// document that contradicts itself, and a person comparing two halves of her own
// file cannot be told that the difference is a scheduling accident.

// disclosureValues says how to read one table's declared columns off a row of
// that table.
//
// It is a map from column name to accessor rather than a switch for one reason:
// [recordOf] has to be able to tell a column it CANNOT read from one that reads
// as empty, and a switch's default branch silently produces the second. The keys
// are checked against the declaration by TestTheDisclosureCanReadEveryDeclaredColumn,
// which is the test ADR 0034 asks for where a holder cannot derive one list
// from the other — here the list of columns IS derived and only the accessors
// are written by hand, so that is the seam the test holds shut.
type disclosureValues[T any] map[string]func(T) any

// The accessors for the tables this module discloses.
//
// Every one of them returns nil for a column that holds nothing, and nil is the
// answer to two questions at once: the column is SQL NULL, or it holds the empty
// string. The models make no distinction — the repository's stringValue folds
// NULL to "" on the way up — and neither does a data subject: both mean the shop
// is not keeping that about her. A field carrying "" would say the opposite,
// that an empty string is what is stored about this person.
var (
	orderValues = disclosureValues[models.Order]{
		"customer_id":     func(o models.Order) any { return textValue(o.CustomerID) },
		"email":           func(o models.Order) any { return textValue(o.Email) },
		"cancel_reason":   func(o models.Order) any { return textValue(o.CancelReason) },
		columnMetadata:    func(o models.Order) any { return jsonValue(o.Metadata) },
		"idempotency_key": func(o models.Order) any { return textValue(o.IdempotencyKey) },
	}

	lineItemValues = disclosureValues[models.OrderLineItem]{
		columnMetadata:   func(i models.OrderLineItem) any { return jsonValue(i.Metadata) },
		columnProperties: func(i models.OrderLineItem) any { return propertiesValue(i.Properties) },
	}

	addressValues = disclosureValues[models.OrderAddress]{
		"source_address_id": func(a models.OrderAddress) any { return textValue(a.SourceAddressID) },
		columnFirstName:     func(a models.OrderAddress) any { return textValue(a.FirstName) },
		columnLastName:      func(a models.OrderAddress) any { return textValue(a.LastName) },
		columnCompany:       func(a models.OrderAddress) any { return textValue(a.Company) },
		columnAddress1:      func(a models.OrderAddress) any { return textValue(a.Address1) },
		columnAddress2:      func(a models.OrderAddress) any { return textValue(a.Address2) },
		columnCity:          func(a models.OrderAddress) any { return textValue(a.City) },
		columnProvince:      func(a models.OrderAddress) any { return textValue(a.Province) },
		columnPostalCode:    func(a models.OrderAddress) any { return textValue(a.PostalCode) },
		columnCountryCode:   func(a models.OrderAddress) any { return textValue(a.CountryCode) },
		columnPhone:         func(a models.OrderAddress) any { return textValue(a.Phone) },
		columnMetadata:      func(a models.OrderAddress) any { return jsonValue(a.Metadata) },
	}

	returnValues = disclosureValues[models.Return]{
		columnReason:   func(r models.Return) any { return textValue(r.Reason) },
		columnNote:     func(r models.Return) any { return textValue(r.Note) },
		columnMetadata: func(r models.Return) any { return jsonValue(r.Metadata) },
	}

	exchangeValues = disclosureValues[models.Exchange]{
		columnNote:     func(e models.Exchange) any { return textValue(e.Note) },
		columnMetadata: func(e models.Exchange) any { return jsonValue(e.Metadata) },
	}

	claimValues = disclosureValues[models.Claim]{
		columnReason:   func(c models.Claim) any { return textValue(c.Reason) },
		columnNote:     func(c models.Claim) any { return textValue(c.Note) },
		columnMetadata: func(c models.Claim) any { return jsonValue(c.Metadata) },
	}

	replacementValues = disclosureValues[models.Replacement]{
		columnNote: func(r models.Replacement) any { return textValue(r.Note) },
	}

	creditLineValues = disclosureValues[models.OrderCreditLine]{
		columnReason: func(c models.OrderCreditLine) any { return textValue(c.Reason) },
		columnNote:   func(c models.OrderCreditLine) any { return textValue(c.Note) },
	}

	claimEvidenceValues = disclosureValues[models.ClaimEvidence]{
		"caption": func(e models.ClaimEvidence) any { return textValue(e.Caption) },
	}

	lineCancellationValues = disclosureValues[models.OrderLineCancellation]{
		columnReason: func(c models.OrderLineCancellation) any { return textValue(c.Reason) },
		columnNote:   func(c models.OrderLineCancellation) any { return textValue(c.Note) },
	}
)

// whyNothingFound is the sentence that goes with [personaldata.Nothing].
//
// The contract REQUIRES it, and the requirement is not ceremony: "we found
// nothing" is an answer a person may well dispute, and the only thing that makes
// it checkable is a sentence saying where the search went. So it names the two
// handles and says that they were matched independently rather than together,
// which is what reaches a guest.
//
// The last clause is the one that costs something to admit and is written down
// anyway. An order anonymized by an earlier erasure has no e-mail left, and for
// a guest order that address WAS the only handle; nothing can find such a row
// again. Reporting a confident "you are not here" while that blind spot exists
// would be the same over-claim the erasure's kept list exists to prevent, in the
// other direction.
const whyNothingFound = "no order carries this customer id or this e-mail address; the search " +
	"covered every order the database still holds — no order can be hidden from it — and matched the " +
	"customer id and the address independently rather than together, so a guest order that has no " +
	"customer id is still reached by its address alone. One limit is worth stating rather than " +
	"leaving to be assumed: an order whose personal columns were erased in an earlier request no " +
	"longer carries an e-mail address, so a GUEST order erased that way has no handle left by which " +
	"this module — or anybody else — could find it again."

// PersonalDataOf shows one person everything this module holds about her
// (ADR 0034).
//
// # What it does
//
// It resolves the subject by BOTH handles, in ONE read-only snapshot, and turns
// every row it finds into a [personaldata.Record]: the order, each of its
// addresses, each of its lines, and each return, exchange and claim opened on
// it. The fields of a record are the columns [personalColumns] declares for that
// record's table and nothing else.
//
// # The three states
//
//   - [personaldata.Disclosed] when at least one order was found. It always
//     carries records, and that is structural rather than a promise: an order
//     row is disclosed unconditionally ([rowIsDisclosed]), so a non-empty
//     resolution cannot produce an empty dossier.
//   - [personaldata.Nothing] when neither handle matched an order, with
//     [whyNothingFound] saying where the search went and what it cannot reach.
//   - [personaldata.Unresolvable] never. This module has a search path and uses
//     it; the state exists for a holder that keeps personal data and cannot tell
//     whose — the review module's byline is the case ADR 0034 names — and
//     answering it here would report a gap that does not exist. The one thing
//     this module genuinely cannot reach is written into the Nothing sentence
//     instead, where the person actually reads it.
//
// # Why it opens a transaction when it takes no lock
//
// [Store.WithReadTx] is read-only and lock-free; what it buys is that the six
// queries behind one dossier see the same instant. An erasure or a cancellation
// landing between them would otherwise produce a document whose order header and
// address came from two different moments, and a person reading her own file has
// no way to know that the disagreement was a scheduling accident. It is the same
// reasoning [Service.GetOrder] rests on, with more at stake.
//
// # The subject is never logged
//
// The log line carries counts and the state and no identifier of the person, for
// the reason [Service.Erase]'s does: writing the e-mail of somebody who asked
// what is held about her into a log would put a copy of her in a place this
// module cannot answer for.
func (s *Service) PersonalDataOf(
	ctx context.Context, subject personaldata.Subject,
) (personaldata.Disclosure, error) {
	customerID, email, err := normalizeSubject(subject, disclosureRequest)
	if err != nil {
		return personaldata.Disclosure{}, err
	}

	var rows dossierRows
	err = s.store.WithReadTx(ctx, func(ctx context.Context) error {
		var readErr error
		if rows.orders, readErr = s.store.OrdersForDisclosure(ctx, customerID, email); readErr != nil {
			return readErr
		}
		// The person has no order here, so the child reads have nothing to
		// look for. Skipping them is not an optimization: called with an empty
		// list they would either scan or have to be given an "everything"
		// meaning, and the second is how a dossier ends up holding somebody
		// else's rows.
		if len(rows.orders) == 0 {
			return nil
		}

		return s.readDossierChildren(ctx, orderIDsOf(rows.orders), &rows)
	})
	if err != nil {
		return personaldata.Disclosure{}, err
	}

	if len(rows.orders) == 0 {
		s.log.InfoContext(ctx, "a disclosure request was answered",
			"holder", ErasureHolder, "state", string(personaldata.Nothing), "records", 0)

		return personaldata.Disclosure{
			Holder: ErasureHolder,
			State:  personaldata.Nothing,
			Why:    whyNothingFound,
		}, nil
	}

	records, err := disclosureRecords(&rows)
	if err != nil {
		return personaldata.Disclosure{}, err
	}

	s.log.InfoContext(ctx, "a disclosure request was answered",
		"holder", ErasureHolder, "state", string(personaldata.Disclosed),
		"orders", len(rows.orders), "records", len(records))

	// Why stays empty. The contract reserves it for a state that needs
	// explaining, and what would be said here — that the Open values were never
	// inspected — is already on every one of those fields as its Kind, where it
	// cannot come loose from the value it describes.
	return personaldata.Disclosure{
		Holder:  ErasureHolder,
		State:   personaldata.Disclosed,
		Records: records,
	}, nil
}

// disclosureRecords turns the rows of one person into her records.
//
// # The order of the records
//
// Per order, and within an order table by table in the order [personalColumns]
// declares the tables. The declaration is what a controller reads beside the
// dossier — it is the map, this is the walk of it — and two documents about the
// same person that list the same tables in two different sequences are two
// documents somebody has to reconcile by hand.
//
// # Why a child record's identifier names its order
//
// [personaldata.Record] has a table, an identifier and the declared fields, and
// no parent. It cannot have one: order_id is not personal data and is not
// declared, so it may not travel as a FIELD. But a return note reading "she said
// the shoe was too small" is worth very little to the person if nothing says
// which purchase it belongs to, so the child's identifier is written as
// "<order id>/<row id>". That names the row, which is all Record.ID promises,
// and it names it in a way that says where the row hangs. Nothing new about the
// person is disclosed by it: the order id is already the identifier of another
// record in the same dossier.
func disclosureRecords(rows *dossierRows) ([]personaldata.Record, error) {
	linesByOrder := groupByOrder(rows.lines, func(i models.OrderLineItem) string { return i.OrderID })
	returnsByOrder := groupByOrder(rows.returns, func(r models.Return) string { return r.OrderID })
	exchangesByOrder := groupByOrder(rows.exchanges, func(e models.Exchange) string { return e.OrderID })
	claimsByOrder := groupByOrder(rows.claims, func(c models.Claim) string { return c.OrderID })
	creditsByOrder := groupByOrder(rows.creditLines, func(c models.OrderCreditLine) string { return c.OrderID })

	// A replacement, a claim's evidence and a line's cancellation name no
	// order; each hangs on it through the parent the same snapshot read.
	orderOfClaim := orderOfParent(rows.claims, func(c models.Claim) (string, string) { return c.ID, c.OrderID })
	orderOfExchange := orderOfParent(rows.exchanges,
		func(e models.Exchange) (string, string) { return e.ID, e.OrderID })
	orderOfLine := orderOfParent(rows.lines,
		func(l models.OrderLineItem) (string, string) { return l.ID, l.OrderID })

	replacementsByOrder, err := groupThroughParent(tableOrderReplacements, rows.replacements,
		func(r models.Replacement) string {
			if r.ClaimID != "" {
				return orderOfClaim[r.ClaimID]
			}
			return orderOfExchange[r.ExchangeID]
		},
		func(r models.Replacement) string { return r.ID })
	if err != nil {
		return nil, err
	}
	evidenceByOrder, err := groupThroughParent(tableOrderClaimEvidence, rows.evidence,
		func(e models.ClaimEvidence) string { return orderOfClaim[e.OrderClaimID] },
		func(e models.ClaimEvidence) string { return e.ID })
	if err != nil {
		return nil, err
	}
	cancellationsByOrder, err := groupThroughParent(tableOrderLineCancellations, rows.cancellations,
		func(c models.OrderLineCancellation) string { return orderOfLine[c.OrderLineItemID] },
		func(c models.OrderLineCancellation) string { return c.ID })
	if err != nil {
		return nil, err
	}

	dossier := &dossierRecords{records: make([]personaldata.Record, 0, len(rows.orders))}
	for i := range rows.orders {
		id := rows.orders[i].ID

		appendRecords(dossier, tableOrders, "", []models.Order{rows.orders[i]}, orderValues,
			func(o models.Order) string { return o.ID })
		appendRecords(dossier, tableOrderLineItems, id,
			linesByOrder[id], lineItemValues, func(l models.OrderLineItem) string { return l.ID })
		appendRecords(dossier, tableOrderAddresses, id,
			rows.addresses[id], addressValues, func(a models.OrderAddress) string { return a.ID })
		appendRecords(dossier, tableOrderReturns, id,
			returnsByOrder[id], returnValues, func(r models.Return) string { return r.ID })
		appendRecords(dossier, tableOrderExchanges, id,
			exchangesByOrder[id], exchangeValues, func(e models.Exchange) string { return e.ID })
		appendRecords(dossier, tableOrderClaims, id,
			claimsByOrder[id], claimValues, func(c models.Claim) string { return c.ID })
		appendRecords(dossier, tableOrderReplacements, id,
			replacementsByOrder[id], replacementValues, func(r models.Replacement) string { return r.ID })
		appendRecords(dossier, tableOrderCreditLines, id,
			creditsByOrder[id], creditLineValues, func(c models.OrderCreditLine) string { return c.ID })
		appendRecords(dossier, tableOrderClaimEvidence, id,
			evidenceByOrder[id], claimEvidenceValues, func(e models.ClaimEvidence) string { return e.ID })
		appendRecords(dossier, tableOrderLineCancellations, id,
			cancellationsByOrder[id], lineCancellationValues,
			func(c models.OrderLineCancellation) string { return c.ID })
	}
	if dossier.err != nil {
		return nil, dossier.err
	}

	return dossier.records, nil
}

// dossierRows is everything one disclosure read, in one snapshot.
type dossierRows struct {
	orders        []models.Order
	addresses     map[string][]models.OrderAddress
	lines         []models.OrderLineItem
	returns       []models.Return
	exchanges     []models.Exchange
	claims        []models.Claim
	replacements  []models.Replacement
	creditLines   []models.OrderCreditLine
	evidence      []models.ClaimEvidence
	cancellations []models.OrderLineCancellation
}

// readDossierChildren reads every child table of the given orders into rows.
func (s *Service) readDossierChildren(ctx context.Context, ids []string, rows *dossierRows) error {
	var err error
	if rows.addresses, err = s.store.OrderAddressesByOrderIDs(ctx, ids); err != nil {
		return err
	}
	if rows.lines, err = s.store.LineItemsForDisclosure(ctx, ids); err != nil {
		return err
	}
	if rows.returns, err = s.store.ReturnsForDisclosure(ctx, ids); err != nil {
		return err
	}
	if rows.exchanges, err = s.store.ExchangesForDisclosure(ctx, ids); err != nil {
		return err
	}
	if rows.claims, err = s.store.ClaimsForDisclosure(ctx, ids); err != nil {
		return err
	}
	if rows.replacements, err = s.store.ReplacementsForDisclosure(ctx, ids); err != nil {
		return err
	}
	if rows.creditLines, err = s.store.CreditLinesForDisclosure(ctx, ids); err != nil {
		return err
	}
	if rows.evidence, err = s.store.ClaimEvidenceForDisclosure(ctx, ids); err != nil {
		return err
	}
	rows.cancellations, err = s.store.LineCancellationsForDisclosure(ctx, ids)

	return err
}

// orderOfParent maps each parent row's id to the order it belongs to.
func orderOfParent[T any](rows []T, ids func(T) (string, string)) map[string]string {
	out := make(map[string]string, len(rows))
	for i := range rows {
		id, orderID := ids(rows[i])
		out[id] = orderID
	}

	return out
}

// groupThroughParent indexes rows that name no order by the order their parent
// names, keeping the order the query returned them in (D188).
//
// A row whose parent the snapshot did not hold is refused rather than placed
// nowhere: leaving it out would hand the person a dossier short of a record
// with nothing in it saying so.
func groupThroughParent[T any](
	table string, rows []T, orderOf, rowID func(T) string,
) (map[string][]T, error) {
	out := make(map[string][]T)
	for i := range rows {
		orderID := orderOf(rows[i])
		if orderID == "" {
			return nil, errors.Internal(CodeDisclosureRowUnplaced,
				"the disclosure read %s row %s and none of the orders it read holds its parent; "+
					"the dossier would be short of it", table, rowID(rows[i]))
		}
		out[orderID] = append(out[orderID], rows[i])
	}

	return out, nil
}

// dossierRecords is the dossier as it is built, with the first error that
// stopped it: once err is set every later append does nothing, so the walk
// reads as the list of tables it is and is checked once at the end.
type dossierRecords struct {
	records []personaldata.Record
	err     error
}

// appendRecords adds the disclosable rows of one table to the dossier.
//
// The identifier of a child row is qualified with the order's, which is the
// decision argued on [disclosureRecords]; the order itself passes an empty
// orderID and is named by its own id.
func appendRecords[T any](
	dossier *dossierRecords, table, orderID string,
	rows []T, values disclosureValues[T], rowID func(T) string,
) {
	for i := range rows {
		if dossier.err != nil {
			return
		}
		id := rowID(rows[i])
		if orderID != "" {
			id = orderID + "/" + id
		}
		record, disclosed, err := recordOf(table, id, rows[i], values)
		if err != nil {
			dossier.err = err
			return
		}
		if disclosed {
			dossier.records = append(dossier.records, record)
		}
	}
}

// recordOf builds one row's record from the DECLARATION, and reports whether it
// is disclosed at all.
//
// The loop is the whole point of this file: the columns come from
// [personalColumns] and the values come from the accessor table, so a disclosure
// that named a column the module never declared, or omitted one it did, would
// have to be written on purpose.
//
// A declared column with no accessor is an ERROR and not a skipped field. The
// module would otherwise hand a person a document that is silently short of a
// column her own declaration promises, and there is no way for her to see the
// difference between "this is empty" and "this was not read". The sweep turns
// the error into a named incompleteness in the dossier (ADR 0034 point 7), which
// is the answer that is true. The branch is reachable — it fires the moment a
// holding is added to the declaration and the accessor beside it is forgotten,
// which is precisely the drift the derivation is here to prevent, and the unit
// test makes it fire in CI instead of in front of a data subject.
func recordOf[T any](
	table, id string, row T, values disclosureValues[T],
) (personaldata.Record, bool, error) {
	holdings := holdingsOf(table)

	fields := make([]personaldata.Field, 0, len(holdings))
	holdsSomething := false
	for _, holding := range holdings {
		read, ok := values[holding.Column]
		if !ok {
			return personaldata.Record{}, false, errors.Internal(CodeDisclosureColumnUnread,
				"the %s module declares %s.%s as personal data and this disclosure has no way to "+
					"read it; the dossier would be short of a column the declaration promises, and "+
					"nothing in it would say so", EntityName, table, holding.Column)
		}

		value := read(row)
		if value != nil {
			holdsSomething = true
		}
		fields = append(fields, personaldata.Field{
			Column: holding.Column,
			Kind:   holding.Kind,
			Value:  value,
		})
	}

	if !holdsSomething && !rowIsDisclosed(table) {
		return personaldata.Record{}, false, nil
	}

	return personaldata.Record{Table: table, ID: id, Fields: fields}, true, nil
}

// holdingsOf returns the declared holdings of one table, in declaration order.
//
// It reads [personalColumns] rather than taking a copy of it, and the order it
// preserves is the order the fields appear in on the record: the declaration's
// own sequence is the one a controller has already read.
func holdingsOf(table string) []personaldata.Holding {
	out := make([]personaldata.Holding, 0, len(personalColumns))
	for i := range personalColumns {
		if personalColumns[i].Table == table {
			out = append(out, personalColumns[i])
		}
	}

	return out
}

// rowIsDisclosed reports whether a row of the table is disclosed even when every
// declared column on it is empty.
//
// The answer is read off the declaration: a table with a [personaldata.Named]
// holding is one gobit writes the person into itself, so its rows are hers by
// construction and an emptied one is what an ERASED order looks like — the case
// this file exists to report rather than hide. A table whose whole declaration
// is [personaldata.Open] holds only text somebody else typed, and a row where
// none of it was typed holds nothing about anybody; twenty such records would
// bury the one that has a note in it.
func rowIsDisclosed(table string) bool {
	return slices.ContainsFunc(personalColumns, func(c personaldata.Holding) bool {
		return c.Table == table && c.Kind == personaldata.Named
	})
}

// orderIDsOf lists the identifiers of the orders, for the child reads.
func orderIDsOf(orders []models.Order) []string {
	ids := make([]string, 0, len(orders))
	for i := range orders {
		ids = append(ids, orders[i].ID)
	}

	return ids
}

// groupByOrder indexes rows by the order they belong to, keeping the order the
// query returned them in.
//
// The queries come back sorted by order and then by creation, so the dossier
// lists a person's returns in the sequence they happened; a map keyed by order
// preserves that within each bucket and costs one pass instead of one scan per
// order.
func groupByOrder[T any](rows []T, orderID func(T) string) map[string][]T {
	out := make(map[string][]T)
	for i := range rows {
		id := orderID(rows[i])
		out[id] = append(out[id], rows[i])
	}

	return out
}

// textValue is the value of a text column, or nil when it holds nothing.
//
// NULL and the empty string collapse into one answer here because they collapse
// in the model on the way up (the repository's stringValue) and because they
// mean the same thing to the person asking: this is not kept about you. A field
// carrying "" would claim the opposite — that an empty string is what is stored.
func textValue(s string) any {
	if s == "" {
		return nil
	}

	return s
}

// jsonValue is the value of a jsonb column, or nil when the document is empty.
//
// The column is NOT NULL with a '{}' default, so "the caller wrote nothing here"
// arrives as an empty map rather than as a missing value; reporting it as a
// field would tell a person that an empty document is held about her.
func jsonValue(m map[string]any) any {
	if len(m) == 0 {
		return nil
	}

	return m
}

// propertiesValue carries a line's words (ADR 0223), or nothing when it has
// none.
func propertiesValue(properties map[string]string) any {
	if len(properties) == 0 {
		return nil
	}

	return properties
}
