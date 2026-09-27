package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// Gift cards: a code that holds a balance, spent by whoever presents it
// (ADR 0208).
//
// The service issues a card and reads it; the hold, release and refund rows
// are the gift-card provider's (payment/giftcard), as store credit's are.

// CodeGiftCardInvalidInput reports a card input that makes no sense.
const CodeGiftCardInvalidInput = "payment_gift_card_invalid_input"

// IssueGiftCardInput is the input for issuing a card.
type IssueGiftCardInput struct {
	// CurrencyCode is the one currency the card holds; required.
	CurrencyCode string
	// Amount is the balance the card is issued with (minor unit); positive.
	Amount int64
	// Reason is why the card is issued; required, for store credit's reason.
	Reason string
}

// IssuedGiftCard is a card as its issue returns it: the one moment its code is
// known to anybody but the person it is handed to.
type IssuedGiftCard struct {
	Card models.GiftCard
	// Code is the card's code as printed. It is kept nowhere and cannot be read
	// again.
	Code    string
	Balance int64
}

// GiftCardWithBalance is a card and what it holds.
type GiftCardWithBalance struct {
	Card    models.GiftCard
	Balance int64
}

// IssueGiftCard issues a card with a new code and its balance, in one
// transaction, and returns the code once.
func (s *Service) IssueGiftCard(ctx context.Context, in IssueGiftCardInput) (IssuedGiftCard, error) {
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return IssuedGiftCard{}, err
	}
	if err := requireAmount("amount", in.Amount); err != nil {
		return IssuedGiftCard{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return IssuedGiftCard{}, errors.Invalid(CodeGiftCardInvalidInput,
			"the gift card needs a reason: why it was issued will never exist again if it is "+
				"not in the record the moment it is asked")
	}

	out, _, err := s.issueGiftCard(ctx, models.GiftCard{
		CurrencyCode: currency, Reason: reason, Source: models.GiftCardIssued,
	}, in.Amount)
	if err != nil {
		return IssuedGiftCard{}, err
	}

	return out, nil
}

// SoldGiftCardInput is a card sold on an order (ADR 0210).
type SoldGiftCardInput struct {
	// Reference names the sale: the order line and the unit. A second issue of
	// the same reference finds the card the first made.
	Reference string
	// OrderID is the order the card was sold on; it goes into the card's reason.
	OrderID string
	// CurrencyCode and Amount are the card's currency and value.
	CurrencyCode string
	Amount       int64
}

// IssueSoldGiftCard issues the card a sale made, once (ADR 0210).
//
// The flow that calls it runs on an event the bus delivers at least once, so a
// second call for the same sale returns the card the first made, without its
// code: the code was shown once, to the call that made it, and exists nowhere
// else. The second return says whether this call made the card.
func (s *Service) IssueSoldGiftCard(ctx context.Context, in SoldGiftCardInput) (IssuedGiftCard, bool, error) {
	reference := strings.TrimSpace(in.Reference)
	orderID := strings.TrimSpace(in.OrderID)
	if reference == "" || orderID == "" {
		return IssuedGiftCard{}, false, errors.Invalid(CodeGiftCardInvalidInput,
			"a sold gift card needs the sale it came from and its order")
	}
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return IssuedGiftCard{}, false, err
	}
	if err := requireAmount("amount", in.Amount); err != nil {
		return IssuedGiftCard{}, false, err
	}

	return s.issueGiftCard(ctx, models.GiftCard{
		CurrencyCode: currency, Reason: "sold on order " + orderID,
		Source: models.GiftCardSold, SourceReference: reference,
	}, in.Amount)
}

// issueGiftCard writes a card with a new code and its issue row in one
// transaction, and reports whether it wrote them.
func (s *Service) issueGiftCard(
	ctx context.Context, card models.GiftCard, amount int64,
) (IssuedGiftCard, bool, error) {
	code := models.NewGiftCardCode()
	normalized, _ := models.NormalizeGiftCardCode(code)
	card.ID, card.CodeTail = models.NewGiftCardID(), models.GiftCardCodeTail(normalized)

	var out IssuedGiftCard
	var created bool
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		written, inserted, err := s.store.InsertGiftCard(ctx, card, models.GiftCardCodeDigest(normalized))
		if err != nil {
			return err
		}
		if !inserted {
			existing, err := s.store.GiftCardBySourceReference(ctx, card.SourceReference)
			if err != nil {
				return err
			}
			balance, err := s.store.GiftCardBalance(ctx, existing.ID)
			if err != nil {
				return err
			}
			out = IssuedGiftCard{Card: existing, Balance: balance}

			return nil
		}
		if _, err := s.store.AppendGiftCardEntry(ctx, models.GiftCardEntry{
			ID:         models.NewGiftCardEntryID(),
			GiftCardID: written.ID,
			Amount:     amount,
			Kind:       models.GiftCardIssue,
		}); err != nil {
			return err
		}
		out, created = IssuedGiftCard{Card: written, Code: code, Balance: amount}, true

		return nil
	})
	if err != nil {
		return IssuedGiftCard{}, false, err
	}

	if created {
		s.log.InfoContext(ctx, "gift card issued", "gift_card", out.Card.ID, "source", out.Card.Source,
			"tail", out.Card.CodeTail, "amount", amount, "currency", out.Card.CurrencyCode)
	}

	return out, created, nil
}

// ReplaceGiftCardCode gives a card a new code and returns it once (ADR 0210).
//
// It is how a code that never reached its holder is recovered: the old code
// stops opening the card, the balance and the history stay with it, and the new
// code is handed to the operator who asked.
func (s *Service) ReplaceGiftCardCode(ctx context.Context, id string) (IssuedGiftCard, error) {
	if strings.TrimSpace(id) == "" {
		return IssuedGiftCard{}, errors.Invalid(CodeGiftCardInvalidInput, "the gift card id is required")
	}
	code := models.NewGiftCardCode()
	normalized, _ := models.NormalizeGiftCardCode(code)

	card, err := s.store.ReplaceGiftCardCode(ctx, id, models.GiftCardCodeDigest(normalized),
		models.GiftCardCodeTail(normalized))
	if err != nil {
		return IssuedGiftCard{}, err
	}
	balance, err := s.store.GiftCardBalance(ctx, card.ID)
	if err != nil {
		return IssuedGiftCard{}, err
	}
	s.log.InfoContext(ctx, "gift card code replaced", "gift_card", card.ID, "tail", card.CodeTail)

	return IssuedGiftCard{Card: card, Code: code, Balance: balance}, nil
}

// GetGiftCard returns a card and its balance.
func (s *Service) GetGiftCard(ctx context.Context, id string) (GiftCardWithBalance, error) {
	if strings.TrimSpace(id) == "" {
		return GiftCardWithBalance{}, errors.Invalid(CodeGiftCardInvalidInput, "the gift card id is required")
	}
	card, err := s.store.GiftCard(ctx, id)
	if err != nil {
		return GiftCardWithBalance{}, err
	}
	balance, err := s.store.GiftCardBalance(ctx, card.ID)
	if err != nil {
		return GiftCardWithBalance{}, err
	}

	return GiftCardWithBalance{Card: card, Balance: balance}, nil
}

// ListGiftCards pages the cards with their balances, newest first.
func (s *Service) ListGiftCards(ctx context.Context, page Page) ([]GiftCardWithBalance, int64, error) {
	page, err := page.normalize()
	if err != nil {
		return nil, 0, err
	}
	cards, total, err := s.store.ListGiftCards(ctx, page.Limit, page.Offset)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]string, 0, len(cards))
	for i := range cards {
		ids = append(ids, cards[i].ID)
	}
	balances, err := s.store.GiftCardBalances(ctx, ids)
	if err != nil {
		return nil, 0, err
	}

	out := make([]GiftCardWithBalance, 0, len(cards))
	for i := range cards {
		out = append(out, GiftCardWithBalance{Card: cards[i], Balance: balances[cards[i].ID]})
	}

	return out, total, nil
}

// ListGiftCardEntries pages a card's history, newest first.
func (s *Service) ListGiftCardEntries(
	ctx context.Context, id string, page Page,
) ([]models.GiftCardEntry, int64, error) {
	if strings.TrimSpace(id) == "" {
		return nil, 0, errors.Invalid(CodeGiftCardInvalidInput, "the gift card id is required")
	}
	page, err := page.normalize()
	if err != nil {
		return nil, 0, err
	}
	if _, err := s.store.GiftCard(ctx, id); err != nil {
		return nil, 0, err
	}

	return s.store.ListGiftCardEntries(ctx, id, page.Limit, page.Offset)
}
