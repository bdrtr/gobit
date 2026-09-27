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

	code := models.NewGiftCardCode()
	normalized, _ := models.NormalizeGiftCardCode(code)

	var out IssuedGiftCard
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		card, err := s.store.InsertGiftCard(ctx, models.GiftCard{
			ID:           models.NewGiftCardID(),
			CodeTail:     models.GiftCardCodeTail(normalized),
			CurrencyCode: currency,
			Reason:       reason,
		}, models.GiftCardCodeDigest(normalized))
		if err != nil {
			return err
		}
		if _, err := s.store.AppendGiftCardEntry(ctx, models.GiftCardEntry{
			ID:         models.NewGiftCardEntryID(),
			GiftCardID: card.ID,
			Amount:     in.Amount,
			Kind:       models.GiftCardIssue,
		}); err != nil {
			return err
		}
		out = IssuedGiftCard{Card: card, Code: code, Balance: in.Amount}

		return nil
	})
	if err != nil {
		return IssuedGiftCard{}, err
	}

	s.log.InfoContext(ctx, "gift card issued",
		"gift_card", out.Card.ID, "tail", out.Card.CodeTail, "amount", in.Amount, "currency", currency)

	return out, nil
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
