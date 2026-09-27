package service_test

import (
	"context"
	"slices"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// The gift card half of fakeStore (ADR 0208). The balance is the sum of the
// entries, as in the real table.

func (f *fakeStore) InsertGiftCard(
	_ context.Context, card models.GiftCard, digest string, validityDays int32,
) (models.GiftCard, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.giftValidity = append(f.giftValidity, validityDays)
	if f.giftCards == nil {
		f.giftCards, f.giftDigests = map[string]models.GiftCard{}, map[string]string{}
	}
	for _, existing := range f.giftDigests {
		if existing == digest {
			return models.GiftCard{}, false, errors.Conflict("payment_query_failed", "the digest is taken")
		}
	}
	for id := range f.giftCards {
		if card.SourceReference != "" && f.giftCards[id].SourceReference == card.SourceReference {
			return models.GiftCard{}, false, nil
		}
	}
	// The table stamps each card with its own transaction's moment; the fake
	// stamps the order they arrive in.
	card.CreatedAt = time.Unix(int64(len(f.giftCards)), 0).UTC()
	if card.ExpiresAt == nil && validityDays > 0 {
		expires := card.CreatedAt.AddDate(0, 0, int(validityDays))
		card.ExpiresAt = &expires
	}
	f.giftCards[card.ID], f.giftDigests[card.ID] = card, digest

	return card, true, nil
}

func (f *fakeStore) GiftCardBySourceReference(_ context.Context, reference string) (models.GiftCard, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for id := range f.giftCards {
		if f.giftCards[id].SourceReference == reference {
			return f.giftCards[id], nil
		}
	}

	return models.GiftCard{}, errors.NotFound("payment_gift_card_not_found", "no gift card was sold as %s", reference)
}

func (f *fakeStore) SoldGiftCardReferences(_ context.Context, references []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var found []string
	for id := range f.giftCards {
		if slices.Contains(references, f.giftCards[id].SourceReference) {
			found = append(found, f.giftCards[id].SourceReference)
		}
	}

	return found, nil
}

func (f *fakeStore) ExpiredOpenGiftCards(_ context.Context, limit int32) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var ids []string
	for id := range f.giftCards {
		card := f.giftCards[id]
		if card.DisabledAt == nil && card.ExpiredAt(time.Now()) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	if len(ids) > int(limit) {
		ids = ids[:limit]
	}

	return ids, nil
}

func (f *fakeStore) LockGiftCardBalance(ctx context.Context, cardID, _ string) error {
	if ctx.Value(txMarkerKey{}) == nil {
		return errors.Internal("payment_query_failed", "LockGiftCardBalance outside a transaction")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.giftCards[cardID]; !ok {
		return errors.NotFound("payment_gift_card_not_found", "no such gift card: %s", cardID)
	}
	f.giftLocks = append(f.giftLocks, cardID)

	return nil
}

func (f *fakeStore) GiftCardOpenHolds(_ context.Context, id string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.giftHolds[id], nil
}

func (f *fakeStore) DisableGiftCard(_ context.Context, id, reason string) (models.GiftCard, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	card, ok := f.giftCards[id]
	if !ok || card.DisabledAt != nil {
		return models.GiftCard{}, errors.NotFound("payment_gift_card_not_found", "no open gift card %s", id)
	}
	closed := time.Unix(2_000, 0).UTC()
	card.DisabledAt, card.DisableReason = &closed, reason
	f.giftCards[id] = card

	return card, nil
}

func (f *fakeStore) ReplaceGiftCardCode(_ context.Context, id, digest, tail string) (models.GiftCard, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	card, ok := f.giftCards[id]
	if !ok {
		return models.GiftCard{}, errors.NotFound("payment_gift_card_not_found", "no such gift card: %s", id)
	}
	changed := time.Unix(1_000, 0).UTC()
	card.CodeTail, card.CodeChangedAt = tail, &changed
	f.giftCards[id], f.giftDigests[id] = card, digest

	return card, nil
}

func (f *fakeStore) AppendGiftCardEntry(_ context.Context, entry models.GiftCardEntry) (models.GiftCardEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failGiftEntry != nil {
		return models.GiftCardEntry{}, f.failGiftEntry
	}
	if f.giftEntries == nil {
		f.giftEntries = map[string][]models.GiftCardEntry{}
	}
	f.giftEntries[entry.GiftCardID] = append(f.giftEntries[entry.GiftCardID], entry)

	return entry, nil
}

func (f *fakeStore) GiftCard(_ context.Context, id string) (models.GiftCard, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	card, ok := f.giftCards[id]
	if !ok {
		return models.GiftCard{}, errors.NotFound("payment_gift_card_not_found", "no such gift card: %s", id)
	}

	return card, nil
}

func (f *fakeStore) GiftCardBalance(_ context.Context, cardID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.giftBalanceLocked(cardID), nil
}

func (f *fakeStore) GiftCardBalances(_ context.Context, cardIDs []string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := map[string]int64{}
	for _, id := range cardIDs {
		if len(f.giftEntries[id]) > 0 {
			out[id] = f.giftBalanceLocked(id)
		}
	}

	return out, nil
}

func (f *fakeStore) ListGiftCards(_ context.Context, limit, offset int64) ([]models.GiftCard, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cards := make([]models.GiftCard, 0, len(f.giftCards))
	for id := range f.giftCards {
		cards = append(cards, f.giftCards[id])
	}
	slices.SortFunc(cards, func(a, b models.GiftCard) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return compareDesc(a.ID, b.ID)
	})

	return page(cards, limit, offset), int64(len(cards)), nil
}

func (f *fakeStore) ListGiftCardEntries(
	_ context.Context, cardID string, limit, offset int64,
) ([]models.GiftCardEntry, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	entries := slices.Clone(f.giftEntries[cardID])
	slices.Reverse(entries)

	return page(entries, limit, offset), int64(len(entries)), nil
}

// giftBalanceLocked sums a card's entries; the caller holds the lock.
func (f *fakeStore) giftBalanceLocked(cardID string) int64 {
	var balance int64
	for _, entry := range f.giftEntries[cardID] {
		balance += entry.Amount
	}

	return balance
}

// compareDesc orders two ids newest first.
func compareDesc(a, b string) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	default:
		return 0
	}
}

// page cuts a slice to a page.
func page[T any](all []T, limit, offset int64) []T {
	if offset >= int64(len(all)) {
		return nil
	}
	end := min(offset+limit, int64(len(all)))

	return all[offset:end]
}
