package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// What the payment module keeps about a person, and its answers about one
// (ADR 0277).
//
// A person is here in three shapes: the store credit and loyalty ledgers are
// the balances the shop owes a customer, their sessions spend them, and a
// payment collection names the customer whose cart it collects for. Beside
// those sit free-form values nobody in this module reads: a collection's
// metadata, what a provider returned for an attempt and why it declined, the
// replay key a caller chose, an operator's reason for a refund or an issue of
// credit. A person is found by the customer id alone; a guest's collection
// names nobody.

// Holder names the payment module in its answers; the module's ModuleName is
// the same word, held to it by a test in the module package.
const Holder = "payment"

// The codes of a request the answers refuse, and of a declaration the
// disclosure cannot read.
const (
	// CodePersonalDataSubjectEmpty refuses a request that names nobody: an
	// answer about everybody is not an answer about a person.
	CodePersonalDataSubjectEmpty = "payment_personal_data_subject_empty"
	// CodeDisclosureColumnUnread reports a declared column the disclosure has
	// no way to read; the dossier would be short of it with nothing saying so.
	CodeDisclosureColumnUnread = "payment_disclosure_column_unread"
)

// The tables the declaration names.
const (
	tableCollections        = "payment_collections"
	tableSessions           = "payment_sessions"
	tableRefunds            = "refunds"
	tableManualSessions     = "payment_manual_sessions"
	tableGiftCardSessions   = "payment_gift_card_sessions"
	tableStoreCreditEntries = "payment_store_credit_entries"
	tableStoreCreditSession = "payment_store_credit_sessions"
	tableLoyaltyEntries     = "payment_loyalty_entries"
	tableLoyaltySessions    = "payment_loyalty_sessions"
)

// The column names that recur across those tables.
const (
	columnCustomerID     = "customer_id"
	columnIdempotencyKey = "idempotency_key"
	columnData           = "data"
	columnReason         = "reason"
	columnDeclineReason  = "decline_reason"
)

// whyReplayKey is the reason every copy of a caller's replay key is declared.
const whyReplayKey = "the replay key the caller chose for the payment attempt; gobit neither builds it nor reads it"

// personalColumns is every place this module keeps something about a person,
// table by table in the order the dossier lists them. Everything is kept on
// erasure: see [whyRetained].
//
// What is NOT here is judged column by column in the module's schema audit
// (internal/modules/payment's erasure_test.go): amounts, states and stamps of
// a payment describe the sale, the references are identifiers of this
// module's or the caller's records, and a gift card and its ledger belong to
// whoever holds the code rather than to a person.
var personalColumns = []personaldata.Holding{
	{
		Table: tableCollections, Column: columnCustomerID, Kind: personaldata.Named,
		Why:       "the customer whose cart the payment collects for; a guest's collection names nobody",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableCollections, Column: "metadata", Kind: personaldata.Open,
		Why:       "the caller's own data on the payment collection",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableSessions, Column: columnData, Kind: personaldata.Open,
		Why:       "what the payment provider returned for the attempt, stored whole and never read",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableSessions, Column: columnDeclineReason, Kind: personaldata.Open,
		Why:       "the provider's words for a declined attempt, which can describe the payer's card or account",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableSessions, Column: columnIdempotencyKey, Kind: personaldata.Open,
		Why: whyReplayKey, OnErasure: personaldata.Kept,
	},
	{
		Table: tableRefunds, Column: columnReason, Kind: personaldata.Open,
		Why:       "why an operator gave money back, in their own words",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableManualSessions, Column: columnData, Kind: personaldata.Open,
		Why:       "the data the caller gave the manual provider when the attempt was opened",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableManualSessions, Column: columnDeclineReason, Kind: personaldata.Open,
		Why:       "the decline the caller's data asked the manual provider for, in the caller's words",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableManualSessions, Column: columnIdempotencyKey, Kind: personaldata.Open,
		Why: whyReplayKey, OnErasure: personaldata.Kept,
	},
	{
		Table: tableGiftCardSessions, Column: columnIdempotencyKey, Kind: personaldata.Open,
		Why: whyReplayKey, OnErasure: personaldata.Kept,
	},
	{
		Table: tableStoreCreditEntries, Column: columnCustomerID, Kind: personaldata.Named,
		Why:       "whose store credit the entry moves",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableStoreCreditEntries, Column: "amount", Kind: personaldata.Named,
		Why:       "how much credit the entry gave this customer or took from them, in its currency; the balance is the sum",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableStoreCreditEntries, Column: columnReason, Kind: personaldata.Open,
		Why:       "why an operator issued the credit, in their own words",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableStoreCreditEntries, Column: "reference", Kind: personaldata.Open,
		Why:       "the operator's own reference on an issue of credit, such as a ticket",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableStoreCreditSession, Column: columnCustomerID, Kind: personaldata.Named,
		Why:       "whose store credit the session spends",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableStoreCreditSession, Column: columnDeclineReason, Kind: personaldata.Named,
		Why:       "gobit's sentence for a declined spend, which states the customer's balance at that moment",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableStoreCreditSession, Column: columnIdempotencyKey, Kind: personaldata.Open,
		Why: whyReplayKey, OnErasure: personaldata.Kept,
	},
	{
		Table: tableLoyaltyEntries, Column: columnCustomerID, Kind: personaldata.Named,
		Why:       "whose loyalty points the entry moves",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableLoyaltyEntries, Column: "points", Kind: personaldata.Named,
		Why:       "how many points the entry gave this customer or took from them; the balance is the sum",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableLoyaltySessions, Column: columnCustomerID, Kind: personaldata.Named,
		Why:       "whose loyalty points the session spends",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableLoyaltySessions, Column: columnDeclineReason, Kind: personaldata.Named,
		Why:       "gobit's sentence for a declined spend, which states the customer's points at that moment",
		OnErasure: personaldata.Kept,
	},
	{
		Table: tableLoyaltySessions, Column: columnIdempotencyKey, Kind: personaldata.Open,
		Why: whyReplayKey, OnErasure: personaldata.Kept,
	},
}

// PersonalDataHoldings returns a copy of everything this module declares.
func PersonalDataHoldings() []personaldata.Holding { return slices.Clone(personalColumns) }

// accessors reads one table's declared columns off a row of it; a declared
// column with no accessor is refused rather than left out of a record.
type accessors[T any] map[string]func(T) any

// The accessors of every table the disclosure reads. Each returns nil for a
// column that holds nothing.
var (
	collectionValues = accessors[models.PaymentCollection]{
		columnCustomerID: func(c models.PaymentCollection) any { return textValue(c.CustomerID) },
		"metadata":       func(c models.PaymentCollection) any { return mapValue(c.Metadata) },
	}
	sessionValues = accessors[models.PaymentSession]{
		columnData:           func(s models.PaymentSession) any { return rawValue(s.Data) },
		columnDeclineReason:  func(s models.PaymentSession) any { return textValue(s.DeclineReason) },
		columnIdempotencyKey: func(s models.PaymentSession) any { return textValue(s.IdempotencyKey) },
	}
	refundValues = accessors[models.Refund]{
		columnReason: func(r models.Refund) any { return textValue(r.Reason) },
	}
	manualSessionValues = accessors[models.ManualSession]{
		columnData:           func(s models.ManualSession) any { return rawValue(s.Data) },
		columnDeclineReason:  func(s models.ManualSession) any { return textValue(s.DeclineReason) },
		columnIdempotencyKey: func(s models.ManualSession) any { return textValue(s.IdempotencyKey) },
	}
	giftCardSessionValues = accessors[models.TenderSession]{
		columnIdempotencyKey: func(s models.TenderSession) any { return textValue(s.IdempotencyKey) },
	}
	storeCreditEntryValues = accessors[models.StoreCreditEntry]{
		columnCustomerID: func(e models.StoreCreditEntry) any { return textValue(e.CustomerID) },
		"amount":         func(e models.StoreCreditEntry) any { return e.Amount },
		columnReason:     func(e models.StoreCreditEntry) any { return textValue(e.Reason) },
		"reference":      func(e models.StoreCreditEntry) any { return textValue(e.Reference) },
	}
	tenderSessionValues = accessors[models.TenderSession]{
		columnCustomerID:     func(s models.TenderSession) any { return textValue(s.OwnerID) },
		columnDeclineReason:  func(s models.TenderSession) any { return textValue(s.DeclineReason) },
		columnIdempotencyKey: func(s models.TenderSession) any { return textValue(s.IdempotencyKey) },
	}
	loyaltyEntryValues = accessors[models.LoyaltyEntry]{
		columnCustomerID: func(e models.LoyaltyEntry) any { return textValue(e.CustomerID) },
		"points":         func(e models.LoyaltyEntry) any { return e.Points },
	}
)

// PersonalDataStore is what the answers read.
type PersonalDataStore interface {
	// WithReadTx runs fn in one read-only snapshot.
	WithReadTx(ctx context.Context, fn func(ctx context.Context) error) error
	CollectionsOfCustomer(ctx context.Context, customerID string) ([]models.PaymentCollection, error)
	SessionsOfCollections(ctx context.Context, collectionIDs []string) ([]models.PaymentSession, error)
	PaymentsOfCollections(ctx context.Context, collectionIDs []string) ([]models.Payment, error)
	RefundsOfPayments(ctx context.Context, paymentIDs []string) ([]models.Refund, error)
	ManualSessionsOfCollections(ctx context.Context, collectionIDs []string) ([]models.ManualSession, error)
	GiftCardSessionsOfCollections(ctx context.Context, collectionIDs []string) ([]models.TenderSession, error)
	StoreCreditEntriesOfCustomer(ctx context.Context, customerID string) ([]models.StoreCreditEntry, error)
	StoreCreditSessionsOfCustomer(ctx context.Context, customerID string) ([]models.TenderSession, error)
	LoyaltyEntriesOfCustomer(ctx context.Context, customerID string) ([]models.LoyaltyEntry, error)
	LoyaltySessionsOfCustomer(ctx context.Context, customerID string) ([]models.TenderSession, error)
}

// PersonalData answers what the module keeps about one person.
type PersonalData struct {
	store PersonalDataStore
	log   *slog.Logger
}

// NewPersonalData builds the answers over store; a nil log discards.
func NewPersonalData(store PersonalDataStore, log *slog.Logger) *PersonalData {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &PersonalData{store: store, log: log}
}

// The sentences of the answers.
const (
	whyNoCustomer = "this module finds a person by the customer id alone and the request carried none, so " +
		"nothing here was looked at; a guest's payment names no customer, and what its collection's " +
		"metadata or a provider's reply holds cannot be told apart by an e-mail address"
	whyNothingFound = "no payment collection, store credit or loyalty entry, and no session spending " +
		"either, names this customer id; a payment made as a guest names no customer and is not reached " +
		"from here"
	whyRetained = "a payment record is the shop's account of money that moved and a store credit or " +
		"loyalty ledger is the balance the shop owes this person, so nothing here is rewritten: the " +
		"customer id and the ledger amounts listed stay with their rows, and the free-form fields — a " +
		"collection's metadata, what a provider returned, a refund's or a credit's reason, the replay " +
		"key a caller chose — are kept unread; the embedder, as controller, answers for them"
)

// dossierRows is everything one answer read, in one snapshot.
type dossierRows struct {
	collections        []models.PaymentCollection
	sessions           []models.PaymentSession
	payments           []models.Payment
	refunds            []models.Refund
	manualSessions     []models.ManualSession
	giftCardSessions   []models.TenderSession
	storeCreditEntries []models.StoreCreditEntry
	storeCreditSession []models.TenderSession
	loyaltyEntries     []models.LoyaltyEntry
	loyaltySessions    []models.TenderSession
}

// count is how many rows about the person were read; payments are the path to
// refunds and hold nothing declared.
func (r *dossierRows) count() int {
	return len(r.collections) + len(r.sessions) + len(r.refunds) + len(r.manualSessions) +
		len(r.giftCardSessions) + len(r.storeCreditEntries) + len(r.storeCreditSession) +
		len(r.loyaltyEntries) + len(r.loyaltySessions)
}

// subjectOf trims the subject; one that names nobody is refused.
func subjectOf(s personaldata.Subject) (string, error) {
	customerID := strings.TrimSpace(s.CustomerID)
	if customerID == "" && strings.TrimSpace(s.Email) == "" {
		return "", errors.Invalid(CodePersonalDataSubjectEmpty,
			"the request names no customer id and no e-mail address; an answer about everybody is not "+
				"an answer about a person")
	}

	return customerID, nil
}

// read reads every row about the customer in one snapshot.
func (p *PersonalData) read(ctx context.Context, customerID string) (dossierRows, error) {
	var rows dossierRows
	err := p.store.WithReadTx(ctx, func(ctx context.Context) error {
		var err error
		if rows.collections, err = p.store.CollectionsOfCustomer(ctx, customerID); err != nil {
			return err
		}
		ids := make([]string, 0, len(rows.collections))
		for i := range rows.collections {
			ids = append(ids, rows.collections[i].ID)
		}
		if rows.sessions, err = p.store.SessionsOfCollections(ctx, ids); err != nil {
			return err
		}
		if rows.payments, err = p.store.PaymentsOfCollections(ctx, ids); err != nil {
			return err
		}
		paymentIDs := make([]string, 0, len(rows.payments))
		for i := range rows.payments {
			paymentIDs = append(paymentIDs, rows.payments[i].ID)
		}
		if rows.refunds, err = p.store.RefundsOfPayments(ctx, paymentIDs); err != nil {
			return err
		}
		if rows.manualSessions, err = p.store.ManualSessionsOfCollections(ctx, ids); err != nil {
			return err
		}
		if rows.giftCardSessions, err = p.store.GiftCardSessionsOfCollections(ctx, ids); err != nil {
			return err
		}
		if rows.storeCreditEntries, err = p.store.StoreCreditEntriesOfCustomer(ctx, customerID); err != nil {
			return err
		}
		if rows.storeCreditSession, err = p.store.StoreCreditSessionsOfCustomer(ctx, customerID); err != nil {
			return err
		}
		if rows.loyaltyEntries, err = p.store.LoyaltyEntriesOfCustomer(ctx, customerID); err != nil {
			return err
		}
		rows.loyaltySessions, err = p.store.LoyaltySessionsOfCustomer(ctx, customerID)

		return err
	})

	return rows, err
}

// Disclose shows one person what this module holds about them (ADR 0277).
//
// A subject with no customer id is [personaldata.Unresolvable]: a guest's
// collection names nobody, so its free-form values may hold that person and
// cannot be attributed. A customer id with no row is [personaldata.Nothing].
func (p *PersonalData) Disclose(ctx context.Context, s personaldata.Subject) (personaldata.Disclosure, error) {
	customerID, err := subjectOf(s)
	if err != nil {
		return personaldata.Disclosure{}, err
	}
	if customerID == "" {
		return personaldata.Disclosure{Holder: Holder, State: personaldata.Unresolvable, Why: whyNoCustomer}, nil
	}

	rows, err := p.read(ctx, customerID)
	if err != nil {
		return personaldata.Disclosure{}, err
	}
	if rows.count() == 0 {
		return personaldata.Disclosure{Holder: Holder, State: personaldata.Nothing, Why: whyNothingFound}, nil
	}

	records, err := disclosureRecords(&rows)
	if err != nil {
		return personaldata.Disclosure{}, err
	}
	p.log.InfoContext(ctx, "a disclosure request was answered",
		"holder", Holder, "state", string(personaldata.Disclosed), "records", len(records))

	return personaldata.Disclosure{Holder: Holder, State: personaldata.Disclosed, Records: records}, nil
}

// Erase answers an erasure request by keeping everything and saying so: a
// payment is the shop's account of money that moved and a ledger is a balance
// owed to the person ([whyRetained]).
func (p *PersonalData) Erase(ctx context.Context, s personaldata.Subject) (personaldata.Result, error) {
	customerID, err := subjectOf(s)
	if err != nil {
		return personaldata.Result{}, err
	}
	result := personaldata.Result{Holder: Holder, Outcome: personaldata.Retained}
	if customerID == "" {
		result.Why = whyNoCustomer

		return result, nil
	}

	rows, err := p.read(ctx, customerID)
	if err != nil {
		return personaldata.Result{}, err
	}
	result.Rows = rows.count()
	if result.Rows == 0 {
		result.Why = whyNothingFound

		return result, nil
	}
	result.Kept = personaldata.Declaration{Holdings: personalColumns}.Paths()
	result.Why = whyRetained

	return result, nil
}

// disclosureRecords turns the rows into records: each collection with the
// sessions, refunds and provider sessions it gathered, named
// "<collection id>/<row id>", then the ledgers and their sessions.
func disclosureRecords(rows *dossierRows) ([]personaldata.Record, error) {
	collectionOfPayment := make(map[string]string, len(rows.payments))
	for i := range rows.payments {
		collectionOfPayment[rows.payments[i].ID] = rows.payments[i].PaymentCollectionID
	}
	sessions := groupBy(rows.sessions, func(s models.PaymentSession) string { return s.PaymentCollectionID })
	refunds := groupBy(rows.refunds, func(r models.Refund) string { return collectionOfPayment[r.PaymentID] })
	manual := groupBy(rows.manualSessions, func(s models.ManualSession) string { return s.Reference })
	giftCards := groupBy(rows.giftCardSessions, func(s models.TenderSession) string { return s.Reference })

	dossier := &dossierRecords{}
	for i := range rows.collections {
		id := rows.collections[i].ID
		appendRecords(dossier, tableCollections, "", rows.collections[i:i+1], collectionValues,
			func(c models.PaymentCollection) string { return c.ID })
		appendRecords(dossier, tableSessions, id, sessions[id], sessionValues,
			func(s models.PaymentSession) string { return s.ID })
		appendRecords(dossier, tableRefunds, id, refunds[id], refundValues,
			func(r models.Refund) string { return r.ID })
		appendRecords(dossier, tableManualSessions, id, manual[id], manualSessionValues,
			func(s models.ManualSession) string { return s.ID })
		appendRecords(dossier, tableGiftCardSessions, id, giftCards[id], giftCardSessionValues,
			func(s models.TenderSession) string { return s.ID })
	}
	appendRecords(dossier, tableStoreCreditEntries, "", rows.storeCreditEntries, storeCreditEntryValues,
		func(e models.StoreCreditEntry) string { return e.ID })
	appendRecords(dossier, tableStoreCreditSession, "", rows.storeCreditSession, tenderSessionValues,
		func(s models.TenderSession) string { return s.ID })
	appendRecords(dossier, tableLoyaltyEntries, "", rows.loyaltyEntries, loyaltyEntryValues,
		func(e models.LoyaltyEntry) string { return e.ID })
	appendRecords(dossier, tableLoyaltySessions, "", rows.loyaltySessions, tenderSessionValues,
		func(s models.TenderSession) string { return s.ID })
	if dossier.err != nil {
		return nil, dossier.err
	}

	return dossier.records, nil
}

// dossierRecords is the dossier as it is built, with the first error that
// stopped it; once err is set every later append does nothing.
type dossierRecords struct {
	records []personaldata.Record
	err     error
}

// appendRecords adds the rows of one table, each record carrying exactly the
// columns the declaration names for the table, in its order. A row whose
// declared values are all empty says nothing about anybody and is left out;
// a row of a table that declares the customer id always names them.
func appendRecords[T any](
	dossier *dossierRecords, table, parentID string, rows []T, values accessors[T], rowID func(T) string,
) {
	holdings := make([]personaldata.Holding, 0)
	for i := range personalColumns {
		if personalColumns[i].Table == table {
			holdings = append(holdings, personalColumns[i])
		}
	}

	for i := range rows {
		if dossier.err != nil {
			return
		}
		fields := make([]personaldata.Field, 0, len(holdings))
		holds := false
		for _, holding := range holdings {
			read, ok := values[holding.Column]
			if !ok {
				dossier.err = errors.Internal(CodeDisclosureColumnUnread,
					"the payment module declares %s.%s and the disclosure has no way to read it; the "+
						"dossier would be short of a column the declaration promises", table, holding.Column)
				return
			}
			value := read(rows[i])
			holds = holds || value != nil
			fields = append(fields, personaldata.Field{Column: holding.Column, Kind: holding.Kind, Value: value})
		}
		if !holds {
			continue
		}
		id := rowID(rows[i])
		if parentID != "" {
			id = parentID + "/" + id
		}
		dossier.records = append(dossier.records, personaldata.Record{Table: table, ID: id, Fields: fields})
	}
}

// groupBy indexes rows by a key, keeping the order they were read in.
func groupBy[T any](rows []T, key func(T) string) map[string][]T {
	out := make(map[string][]T)
	for i := range rows {
		out[key(rows[i])] = append(out[key(rows[i])], rows[i])
	}

	return out
}

// textValue is a text column's value, or nil when it holds nothing.
func textValue(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}

	return s
}

// mapValue is a jsonb column's value, or nil when it holds nothing.
func mapValue(m map[string]any) any {
	if len(m) == 0 {
		return nil
	}

	return m
}

// rawValue decodes a stored document for the dossier, or nil when it holds
// nothing; a document that does not decode is handed over as its text.
func rawValue(raw json.RawMessage) any {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		if strings.TrimSpace(string(raw)) == "" {
			return nil
		}

		return string(raw)
	}
	if m, ok := value.(map[string]any); ok && len(m) == 0 {
		return nil
	}

	return value
}
