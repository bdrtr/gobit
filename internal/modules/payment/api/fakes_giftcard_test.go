package api_test

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// The gift card half of fakePayments (ADR 0208).

func (f *fakePayments) IssueGiftCard(_ context.Context, in service.IssueGiftCardInput) (service.IssuedGiftCard, error) {
	f.lastGiftInput = in
	if f.err != nil {
		return service.IssuedGiftCard{}, f.err
	}

	return f.issuedCard, nil
}

func (f *fakePayments) GetGiftCard(_ context.Context, id string) (service.GiftCardWithBalance, error) {
	f.lastGiftID = id
	if f.err != nil {
		return service.GiftCardWithBalance{}, f.err
	}

	return f.giftCards[0], nil
}

func (f *fakePayments) ListGiftCards(_ context.Context, _ service.Page) ([]service.GiftCardWithBalance, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}

	return f.giftCards, int64(len(f.giftCards)), nil
}

func (f *fakePayments) ListGiftCardEntries(
	_ context.Context, id string, _ service.Page,
) ([]models.GiftCardEntry, int64, error) {
	f.lastGiftID = id
	if f.err != nil {
		return nil, 0, f.err
	}

	return f.giftEntries, int64(len(f.giftEntries)), nil
}

func (f *fakePayments) ReplaceGiftCardCode(_ context.Context, id string) (service.IssuedGiftCard, error) {
	f.lastGiftID = id
	if f.err != nil {
		return service.IssuedGiftCard{}, f.err
	}

	return f.issuedCard, nil
}

func (f *fakePayments) DisableGiftCard(_ context.Context, id, reason string) (service.GiftCardWithBalance, error) {
	f.lastGiftID, f.lastDisableReason = id, reason
	if f.err != nil {
		return service.GiftCardWithBalance{}, f.err
	}

	return f.giftCards[0], nil
}
