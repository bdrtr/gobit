package service

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The order journal (ADR 0188): the order module's facts as balanced debit
// and credit lines, derived when read from records the module already keeps.

const (
	// MaxJournalWindow is the widest window one read of the journal covers,
	// the payment journal's quarter (ADR 0186).
	MaxJournalWindow = 93 * 24 * time.Hour
	// MaxJournalEntries is the most entries one read returns; a window holding
	// more is refused rather than cut.
	MaxJournalEntries = 10000
)

// JournalQuery is the window a journal read covers: [From, To), and one
// currency when CurrencyCode is set.
type JournalQuery struct {
	From, To     time.Time
	CurrencyCode string
}

// Journal is the module's books over a window.
type Journal struct {
	From         time.Time
	To           time.Time
	CurrencyCode string
	// Entries are in time order, and every one of them balances.
	Entries []models.JournalEntry
	// Balances are the entries' sums per currency and account.
	Balances []models.JournalBalance
}

// Journal derives the module's books over a window.
func (s *Service) Journal(ctx context.Context, q JournalQuery) (Journal, error) {
	if q.From.IsZero() || q.To.IsZero() {
		return Journal{}, errors.Invalid(CodeInvalidInput, "the journal needs both ends of its window")
	}
	// The bounds are cut to the microsecond the database stores before any
	// read. The driver cuts them for the queries, and the documents are read
	// back and filtered again here: two filters at two precisions would let a
	// document stamped at a bound's microsecond fall between two adjacent
	// windows (ADR 0419).
	from, to := q.From.UTC().Truncate(time.Microsecond), q.To.UTC().Truncate(time.Microsecond)
	if !from.Before(to) {
		return Journal{}, errors.Invalid(CodeInvalidInput,
			"the journal's window has to end after it begins: %s is not before %s",
			from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	if to.Sub(from) > MaxJournalWindow {
		return Journal{}, errors.Invalid(CodeInvalidInput,
			"the journal reads at most %d days at a time", int(MaxJournalWindow/(24*time.Hour)))
	}
	currency := ""
	if q.CurrencyCode != "" {
		normalized, err := normalizeCurrency(q.CurrencyCode)
		if err != nil {
			return Journal{}, err
		}
		currency = normalized
	}

	facts, err := s.store.JournalFacts(ctx, from, to, currency, MaxJournalEntries)
	if err != nil {
		return Journal{}, err
	}
	refunded, err := s.refundFacts(ctx, from, to, currency)
	if err != nil {
		return Journal{}, err
	}
	facts = append(facts, refunded...)
	documented, err := s.documentFacts(ctx, from, to, currency)
	if err != nil {
		return Journal{}, err
	}
	facts = append(facts, documented...)
	if len(facts) > MaxJournalEntries {
		return Journal{}, errors.Invalid(CodeInvalidInput,
			"the window holds more than %d facts; ask for a narrower one", MaxJournalEntries)
	}

	entries, err := journalEntries(facts)
	if err != nil {
		return Journal{}, err
	}

	return Journal{
		From: from, To: to, CurrencyCode: currency,
		Entries: entries, Balances: trialBalance(entries),
	}, nil
}

// journalEntries names the accounts of every fact and puts the entries in time
// order, the id and then the kind breaking a tie: an order placed and canceled
// in the same instant keeps one order of the two.
func journalEntries(facts []models.JournalFact) ([]models.JournalEntry, error) {
	entries := make([]models.JournalEntry, 0, len(facts))
	for i := range facts {
		entry, err := journalEntry(&facts[i])
		if err != nil {
			return nil, err
		}
		if len(entry.Lines) == 0 {
			// A free order moves nothing onto the books.
			continue
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b models.JournalEntry) int {
		return cmp.Or(a.OccurredAt.Compare(b.OccurredAt), cmp.Compare(a.ID, b.ID),
			cmp.Compare(kindOrder(a.Kind), kindOrder(b.Kind)))
	})

	return entries, nil
}

// kindOrder puts a placement before the cancellation of the same order, and a
// document's correction before its voiding.
func kindOrder(kind models.JournalKind) int {
	switch kind {
	case models.JournalOrderPlaced:
		return 0
	case models.JournalOrderCanceled:
		return 1
	case models.JournalTaxCorrected:
		return 3
	case models.JournalTaxCorrectionVoided:
		return 4
	default:
		return 2
	}
}

// journalEntry is the chart of accounts, in one place.
//
//	order placed      Dr receivable (total), sales_discounts (discount)
//	                  Cr sales (subtotal less gift cards), gift_card (gift card
//	                  lines), tax_payable (tax), shipping (shipping)
//	order canceled    the same lines, the other way
//	credit line       Dr credit_allowances   Cr receivable
//	return refunded   Dr sales_returns       Cr receivable
//	claim refunded    Dr claim_allowances    Cr receivable
//	delivery changed  Dr shipping            Cr receivable
//	delivery upgraded Dr receivable          Cr shipping
//	exchange funded   Dr receivable          Cr sales
//	exchange refunded Dr sales               Cr receivable
//	tax corrected     a refund document: Dr tax_payable   Cr the act's account
//	                  a sale document:   Dr the act's account   Cr tax_payable
//	correction voided the same lines, the other way
//
// A tax correction is an amending document that names an act (ADR 0419): its
// tax_total leaves the account the act was booked to whole, at the document's
// issued_at, and goes back at its voided_at. The act's account is the one
// [givenBackTo] or [chargedTo] names for the act's kind. An exchange that
// names its return writes no entry beyond its funding's or its refund's: its
// refund document of what came back and its sale of what was sent each move
// their tax against sales, so sales nets the goods kept and sent without
// their tax, and tax_payable moves by the sent goods' tax less the returned
// ones' (ADR 0432).
//
// An order balances because its table holds it to
// total = subtotal - discount_total + tax_total + shipping_total; the entry is
// checked anyway, since a row that broke the identity would otherwise be books
// that do not balance. A zero amount writes no line.
func journalEntry(f *models.JournalFact) (models.JournalEntry, error) {
	entry := models.JournalEntry{
		ID: f.ID, Kind: f.Kind, OrderID: f.OrderID,
		OccurredAt: f.OccurredAt.UTC(), CurrencyCode: f.CurrencyCode,
	}

	switch f.Kind {
	case models.JournalOrderPlaced, models.JournalOrderCanceled:
		for _, amount := range []int64{
			f.Subtotal, f.DiscountTotal, f.TaxTotal, f.ShippingTotal, f.Total, f.GiftCardSubtotal,
		} {
			if amount < 0 {
				return models.JournalEntry{}, errors.Internal(CodeInvalidInput,
					"the journal read order %s with a negative amount", f.ID)
			}
		}
		if f.Total+f.DiscountTotal != f.Subtotal+f.TaxTotal+f.ShippingTotal {
			return models.JournalEntry{}, errors.Internal(CodeInvalidInput,
				"the journal read order %s whose total does not add up", f.ID)
		}
		if f.GiftCardSubtotal > f.Subtotal {
			return models.JournalEntry{}, errors.Internal(CodeInvalidInput,
				"the journal read order %s whose gift card lines exceed its subtotal", f.ID)
		}
		// A sold gift card's price is a debt to its holder rather than a sale
		// (ADR 0211); the rest of the subtotal is sales.
		lines := []models.JournalLine{
			{Account: models.AccountReceivable, Debit: f.Total},
			{Account: models.AccountSalesDiscounts, Debit: f.DiscountTotal},
			{Account: models.AccountSales, Credit: f.Subtotal - f.GiftCardSubtotal},
			{Account: models.AccountGiftCard, Credit: f.GiftCardSubtotal},
			{Account: models.AccountTaxPayable, Credit: f.TaxTotal},
			{Account: models.AccountShipping, Credit: f.ShippingTotal},
		}
		for _, line := range lines {
			if line.Debit == 0 && line.Credit == 0 {
				continue
			}
			if f.Kind == models.JournalOrderCanceled {
				line.Debit, line.Credit = line.Credit, line.Debit
			}
			entry.Lines = append(entry.Lines, line)
		}
	case models.JournalCreditLine, models.JournalReturnRefunded, models.JournalClaimRefunded,
		models.JournalDeliveryChanged, models.JournalExchangeRefunded:
		if f.Amount <= 0 {
			return models.JournalEntry{}, errors.Internal(CodeInvalidInput,
				"the journal read %s %s of %d; it moves a positive amount", f.Kind, f.ID, f.Amount)
		}
		entry.Lines = []models.JournalLine{
			{Account: givenBackTo[f.Kind], Debit: f.Amount},
			{Account: models.AccountReceivable, Credit: f.Amount},
		}
	case models.JournalDeliveryUpgraded, models.JournalExchangeFunded:
		// A dearer delivery or an exchange's difference adds to what the
		// order owes and to what it charged; the payment module's capture of
		// it credits receivable (ADR 0200, 0203).
		if f.Amount <= 0 {
			return models.JournalEntry{}, errors.Internal(CodeInvalidInput,
				"the journal read %s %s of %d; it moves a positive amount", f.Kind, f.ID, f.Amount)
		}
		entry.Lines = []models.JournalLine{
			{Account: models.AccountReceivable, Debit: f.Amount},
			{Account: chargedTo[f.Kind], Credit: f.Amount},
		}
	case models.JournalTaxCorrected, models.JournalTaxCorrectionVoided:
		lines, err := taxCorrectionLines(f)
		if err != nil {
			return models.JournalEntry{}, err
		}
		entry.Lines = lines
	default:
		return models.JournalEntry{}, errors.Internal(CodeInvalidInput,
			"the journal read a fact of an unknown kind %q (%s)", f.Kind, f.ID)
	}

	return entry, nil
}

// taxCorrectionLines are a document's correction (ADR 0419): a refund document
// takes its tax off tax_payable and off the account its act gave back from, a
// sale document puts it on tax_payable and takes it out of the account its act
// charged to, and a voiding reverses either. A document that moved no tax
// writes no line, and is held to the same kinds as one that did: the journal
// refuses what it cannot place whatever it would have written.
func taxCorrectionLines(f *models.JournalFact) ([]models.JournalLine, error) {
	if f.Amount < 0 {
		return nil, errors.Internal(CodeInvalidInput,
			"the journal read document %s with a negative tax of %d", f.ID, f.Amount)
	}

	var lines []models.JournalLine
	switch f.DocumentKind {
	case documentRefund:
		account, ok := givenBackTo[f.ActKind]
		if !ok {
			return nil, errors.Internal(CodeInvalidInput,
				"the journal read refund document %s naming a %s act, which gives nothing back", f.ID, f.ActKind)
		}
		lines = []models.JournalLine{
			{Account: models.AccountTaxPayable, Debit: f.Amount},
			{Account: account, Credit: f.Amount},
		}
	case documentSale:
		account, ok := chargedTo[f.ActKind]
		if !ok {
			return nil, errors.Internal(CodeInvalidInput,
				"the journal read sale document %s naming a %s act, which charges nothing", f.ID, f.ActKind)
		}
		lines = []models.JournalLine{
			{Account: account, Debit: f.Amount},
			{Account: models.AccountTaxPayable, Credit: f.Amount},
		}
	default:
		return nil, errors.Internal(CodeInvalidInput,
			"the journal read document %s of an unknown kind %q", f.ID, f.DocumentKind)
	}
	if f.Amount == 0 {
		return nil, nil
	}
	if f.Kind == models.JournalTaxCorrectionVoided {
		for i := range lines {
			lines[i].Debit, lines[i].Credit = lines[i].Credit, lines[i].Debit
		}
	}

	return lines, nil
}

// trialBalance sums the entries per currency and account, in a stable order.
func trialBalance(entries []models.JournalEntry) []models.JournalBalance {
	type key struct {
		currency string
		account  models.JournalAccount
	}
	sums := map[key]*models.JournalBalance{}
	for i := range entries {
		for j := range entries[i].Lines {
			line := &entries[i].Lines[j]
			k := key{entries[i].CurrencyCode, line.Account}
			sum, ok := sums[k]
			if !ok {
				sum = &models.JournalBalance{CurrencyCode: k.currency, Account: k.account}
				sums[k] = sum
			}
			sum.Debit += line.Debit
			sum.Credit += line.Credit
		}
	}

	out := make([]models.JournalBalance, 0, len(sums))
	for _, sum := range sums {
		out = append(out, *sum)
	}
	slices.SortFunc(out, func(a, b models.JournalBalance) int {
		return cmp.Or(cmp.Compare(a.CurrencyCode, b.CurrencyCode), cmp.Compare(a.Account, b.Account))
	})

	return out
}

// givenBackTo is the account each kind of amount given back is debited to.
var givenBackTo = map[models.JournalKind]models.JournalAccount{
	models.JournalCreditLine:     models.AccountCreditAllowances,
	models.JournalReturnRefunded: models.AccountSalesReturns,
	models.JournalClaimRefunded:  models.AccountClaimAllowances,
	// A cheaper delivery gives back shipping the order charged, not a
	// concession (ADR 0199).
	models.JournalDeliveryChanged: models.AccountShipping,
	// A refund of an exchange's difference reverses the sale its funding
	// booked (ADR 0203).
	models.JournalExchangeRefunded: models.AccountSales,
	// What an exchange takes back is a document's act only: its money is in
	// the difference ADR 0203 books to sales, and its refund document takes
	// the returned goods' tax out of sales (ADR 0432).
	models.JournalExchangeReturned: models.AccountSales,
}

// chargedTo is the account each kind of amount added to what the order owes
// is credited to.
var chargedTo = map[models.JournalKind]models.JournalAccount{
	models.JournalDeliveryUpgraded: models.AccountShipping,
	// An exchange's positive difference is goods sold for more than the goods
	// they replace (ADR 0203).
	models.JournalExchangeFunded: models.AccountSales,
	// What an exchange sends is a document's act only, and its sale puts the
	// sent goods' tax on tax_payable out of sales (ADR 0432).
	models.JournalExchangeSent: models.AccountSales,
}

// CausedRefunds is the surface of the payment module ("payment.interop") the
// journal reads (ADR 0189): the refunds inside [from, to) that name their
// cause, as a JSON array of {id, reference, amount, currency_code,
// collection_id, refunded_at}.
//
// CausedRefundsOfJSON answers the same array for the refunds that name one of
// the given causes, whenever they were made (ADR 0406).
//
// CausedRefundsByIDJSON answers it for the refunds with the given ids, which is
// how the journal finds the order a documented refund belongs to (ADR 0419).
type CausedRefunds interface {
	CausedRefundsJSON(ctx context.Context, from, to time.Time, currencyCode string) (json.RawMessage, error)
	CausedRefundsOfJSON(ctx context.Context, references []string) (json.RawMessage, error)
	CausedRefundsByIDJSON(ctx context.Context, ids []string) (json.RawMessage, error)
}

// causedRefund is one element of [CausedRefunds.CausedRefundsJSON]'s answer.
// The field names are the payment module's and are written here on purpose:
// this module cannot import that one, and the e2e closure test is what holds
// the two spellings together (ADR 0189).
type causedRefund struct {
	ID           string    `json:"id"`
	Reference    string    `json:"reference"`
	Amount       int64     `json:"amount"`
	CurrencyCode string    `json:"currency_code"`
	RefundedAt   time.Time `json:"refunded_at"`
}

// causeExchange is the kind of cause an exchange is (ADR 0189, 0203), and
// causeClaim the kind a claim is.
const (
	causeExchange = "exchange"
	causeClaim    = "claim"
)

// refundFacts reads the refunds in the window that name one of this module's
// returns or claims, as facts on the order they belong to (ADR 0189).
//
// A refund whose reference is an exchange's, or nothing of this module's, is
// not a fact here: an exchange's difference is outside these books (ADR 0188).
// A refund in another currency than its order's is an error rather than an
// entry, because the reference would then name the wrong order.
func (s *Service) refundFacts(
	ctx context.Context, from, to time.Time, currency string,
) ([]models.JournalFact, error) {
	if s.refunds == nil {
		return nil, nil
	}
	raw, err := s.refunds.CausedRefundsJSON(ctx, from, to, currency)
	if err != nil {
		return nil, err
	}
	var refunds []causedRefund
	if err := json.Unmarshal(raw, &refunds); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInvalidInput,
			"the payment module's refunds could not be read")
	}
	if len(refunds) == 0 {
		return nil, nil
	}

	references := make([]string, 0, len(refunds))
	for i := range refunds {
		references = append(references, refunds[i].Reference)
	}
	causes, err := s.store.JournalCauses(ctx, references)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]models.JournalCause, len(causes))
	for i := range causes {
		byID[causes[i].ID] = causes[i]
	}

	facts := make([]models.JournalFact, 0, len(refunds))
	for i := range refunds {
		refund := &refunds[i]
		cause, ok := byID[refund.Reference]
		if !ok {
			continue
		}
		if cause.CurrencyCode != refund.CurrencyCode {
			return nil, errors.Internal(CodeInvalidInput,
				"refund %s names %s %s in %s, and the order is in %s",
				refund.ID, cause.Kind, cause.ID, refund.CurrencyCode, cause.CurrencyCode)
		}
		kind := models.JournalReturnRefunded
		switch cause.Kind {
		case causeClaim:
			kind = models.JournalClaimRefunded
		case causeExchange:
			kind = models.JournalExchangeRefunded
		}
		facts = append(facts, models.JournalFact{
			ID: refund.ID, Kind: kind, OrderID: cause.OrderID,
			OccurredAt: refund.RefundedAt, CurrencyCode: refund.CurrencyCode, Amount: refund.Amount,
		})
	}

	return facts, nil
}

// DocumentedTax is the surface of the invoice module ("invoice.interop") the
// journal reads (ADR 0419): the amending documents that name an act and were
// issued or voided inside [from, to), as a JSON array of {id, kind,
// amendment_key, tax_total, currency_code, issued_at, voided_at}.
type DocumentedTax interface {
	DocumentedTaxJSON(ctx context.Context, from, to time.Time, currencyCode string) (json.RawMessage, error)
}

// documentedTax is one element of [DocumentedTax.DocumentedTaxJSON]'s answer.
// The field names are the invoice module's, written here because this module
// cannot import that one; the e2e books test holds the two spellings together.
type documentedTax struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	AmendmentKey string     `json:"amendment_key"`
	TaxTotal     int64      `json:"tax_total"`
	CurrencyCode string     `json:"currency_code"`
	IssuedAt     time.Time  `json:"issued_at"`
	VoidedAt     *time.Time `json:"voided_at"`
}

// The invoice module's document kinds a correction reads (ADR 0406): a refund
// gives tax back, a sale charges it.
const (
	documentRefund = "refund"
	documentSale   = "sale"
)

// documentedActs are the act kinds a document can name: what the invoicing
// flow documents (ADR 0406), and an exchange that names its return as what it
// takes back and what it sends (ADR 0432). An exchange's funding and its
// refund are on no document. The invoice module refuses a key naming another
// kind, or naming one on the other kind of document, when it is written, and
// internal/arch binds its map to [DocumentedActs] and [DocumentKindOf]
// (ADR 0419).
var documentedActs = []models.JournalKind{
	models.JournalCreditLine, models.JournalDeliveryChanged, models.JournalDeliveryUpgraded,
	models.JournalReturnRefunded, models.JournalClaimRefunded,
	models.JournalExchangeReturned, models.JournalExchangeSent,
}

// DocumentedActs returns the act kinds a document can name; the slice is the
// caller's.
func DocumentedActs() []models.JournalKind {
	return slices.Clone(documentedActs)
}

// DocumentKindOf returns the kind of document whose tax the journal books for
// an act of the kind, "refund" for an act that gave money back and "sale" for
// one that charged it, and false for a kind no document names (ADR 0419).
func DocumentKindOf(act models.JournalKind) (string, bool) {
	if !slices.Contains(documentedActs, act) {
		return "", false
	}
	if _, ok := givenBackTo[act]; ok {
		return documentRefund, true
	}
	if _, ok := chargedTo[act]; ok {
		return documentSale, true
	}
	return "", false
}

// actOrder is the order a document's act belongs to.
type actOrder struct {
	orderID, currency string
}

// documentFacts reads the amending documents issued or voided in the window
// as facts on the order their act belongs to (ADR 0419): one at the document's
// issued_at and, when it fell inside the window too, one at its voided_at.
//
// The act is found from the document's key, "<journal kind>:<act id>" as the
// invoicing flow wrote it: a credit line or a delivery change from this
// module's own tables, a return's or a claim's refund through the payment
// module's refund and then its cause, and an exchange's two halves from the
// exchange the key names (ADR 0432). A key this module cannot read, an act it
// cannot find and a document in another currency than its order's are errors
// rather than entries: books that silently left a correction out would balance
// and be wrong.
func (s *Service) documentFacts(
	ctx context.Context, from, to time.Time, currency string,
) ([]models.JournalFact, error) {
	if s.docs == nil {
		return nil, nil
	}
	raw, err := s.docs.DocumentedTaxJSON(ctx, from, to, currency)
	if err != nil {
		if errors.IsInvalid(err) {
			// The invoice module refuses a window holding more documents than
			// one read takes; the caller asked this module, and is answered in
			// its code.
			return nil, errors.Wrap(err, errors.KindInvalid, CodeInvalidInput,
				"the window holds more documents amending its orders than one read takes; ask for a narrower one")
		}
		return nil, err
	}
	var documents []documentedTax
	if err := json.Unmarshal(raw, &documents); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInvalidInput,
			"the invoice module's documents could not be read")
	}
	if len(documents) == 0 {
		return nil, nil
	}

	acts := make([]models.JournalKind, len(documents))
	ids := make([]string, len(documents))
	var credits, changes, refunds, exchanges []string
	for i := range documents {
		kind, id, ok := strings.Cut(documents[i].AmendmentKey, ":")
		if !ok || id == "" || !slices.Contains(documentedActs, models.JournalKind(kind)) {
			return nil, errors.Internal(CodeInvalidInput,
				"document %s names an act this journal cannot read: %q", documents[i].ID, documents[i].AmendmentKey)
		}
		acts[i], ids[i] = models.JournalKind(kind), id
		switch acts[i] {
		case models.JournalCreditLine:
			credits = append(credits, id)
		case models.JournalDeliveryChanged, models.JournalDeliveryUpgraded:
			changes = append(changes, id)
		case models.JournalExchangeReturned, models.JournalExchangeSent:
			exchanges = append(exchanges, id)
		default:
			refunds = append(refunds, id)
		}
	}

	orders, err := s.actOrders(ctx, credits, changes, refunds)
	if err != nil {
		return nil, err
	}
	if err := s.exchangeActOrders(ctx, exchanges, orders); err != nil {
		return nil, err
	}

	facts := make([]models.JournalFact, 0, len(documents))
	for i := range documents {
		document := &documents[i]
		order, ok := orders[string(acts[i])+":"+ids[i]]
		if !ok {
			return nil, errors.Internal(CodeInvalidInput,
				"document %s names %s %s, which is not one of this module's acts", document.ID, acts[i], ids[i])
		}
		if order.currency != document.CurrencyCode {
			return nil, errors.Internal(CodeInvalidInput,
				"document %s names %s %s in %s, and the order is in %s",
				document.ID, acts[i], ids[i], document.CurrencyCode, order.currency)
		}
		fact := models.JournalFact{
			ID: document.ID, Kind: models.JournalTaxCorrected, OrderID: order.orderID,
			OccurredAt: document.IssuedAt, CurrencyCode: document.CurrencyCode, Amount: document.TaxTotal,
			ActKind: acts[i], DocumentKind: document.Kind,
		}
		if within(document.IssuedAt, from, to) {
			facts = append(facts, fact)
		}
		if document.VoidedAt != nil && within(*document.VoidedAt, from, to) {
			fact.Kind, fact.OccurredAt = models.JournalTaxCorrectionVoided, *document.VoidedAt
			facts = append(facts, fact)
		}
	}

	return facts, nil
}

// exchangeActOrders finds the order of each exchange an exchange's documents
// name, under both of its kinds (ADR 0432). An id that is no exchange of this
// module is left out, and the document naming it is refused by the caller.
func (s *Service) exchangeActOrders(ctx context.Context, exchanges []string, out map[string]actOrder) error {
	if len(exchanges) == 0 {
		return nil
	}
	causes, err := s.store.JournalCauses(ctx, exchanges)
	if err != nil {
		return err
	}
	for i := range causes {
		if causes[i].Kind != causeExchange {
			continue
		}
		order := actOrder{orderID: causes[i].OrderID, currency: causes[i].CurrencyCode}
		out[string(models.JournalExchangeReturned)+":"+causes[i].ID] = order
		out[string(models.JournalExchangeSent)+":"+causes[i].ID] = order
	}

	return nil
}

// within reports whether the moment is inside [from, to).
func within(at, from, to time.Time) bool {
	return !at.Before(from) && at.Before(to)
}

// actOrders finds the order each documented act belongs to, keyed by
// "<journal kind>:<act id>". A delivery change answers for both of its kinds,
// and a refund for the kind its cause gives it.
func (s *Service) actOrders(
	ctx context.Context, credits, changes, refunds []string,
) (map[string]actOrder, error) {
	out := make(map[string]actOrder, len(credits)+len(changes)+len(refunds))

	rows, err := s.store.JournalActOrders(ctx, credits, changes)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		order := actOrder{orderID: rows[i].OrderID, currency: rows[i].CurrencyCode}
		switch rows[i].Kind {
		case "credit_line":
			out[string(models.JournalCreditLine)+":"+rows[i].ID] = order
		default:
			out[string(models.JournalDeliveryChanged)+":"+rows[i].ID] = order
			out[string(models.JournalDeliveryUpgraded)+":"+rows[i].ID] = order
		}
	}

	if len(refunds) == 0 || s.refunds == nil {
		return out, nil
	}
	raw, err := s.refunds.CausedRefundsByIDJSON(ctx, refunds)
	if err != nil {
		return nil, err
	}
	var found []causedRefund
	if err := json.Unmarshal(raw, &found); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInvalidInput,
			"the payment module's refunds could not be read")
	}
	references := make([]string, 0, len(found))
	for i := range found {
		references = append(references, found[i].Reference)
	}
	causes, err := s.store.JournalCauses(ctx, references)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]models.JournalCause, len(causes))
	for i := range causes {
		byID[causes[i].ID] = causes[i]
	}
	for i := range found {
		cause, ok := byID[found[i].Reference]
		if !ok {
			continue
		}
		kind := models.JournalReturnRefunded
		switch cause.Kind {
		case causeClaim:
			kind = models.JournalClaimRefunded
		case causeExchange:
			continue
		}
		out[string(kind)+":"+found[i].ID] = actOrder{orderID: cause.OrderID, currency: cause.CurrencyCode}
	}

	return out, nil
}
