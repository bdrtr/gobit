package service

import (
	"context"
	"time"
)

// MaxRetentionDays is the longest an open cart may be kept untouched; the
// config holds the same ceiling and internal/arch binds the two (ADR 0301).
const MaxRetentionDays = 3_650

// DeleteAbandonedCarts deletes for good up to limit open carts untouched for
// the shop's retention period before now, the oldest first, with their lines,
// addresses, shipping methods and coupon codes, and returns how many (ADR
// 0301).
//
// A completed cart is never deleted: it is the record an order rests on. With
// no period set nothing is read and nothing is deleted, so an installation
// keeps every cart until its controller names a period (ADR 0029).
func (s *Service) DeleteAbandonedCarts(ctx context.Context, now time.Time, limit int64) (int64, error) {
	if s.retention == 0 || limit <= 0 {
		return 0, nil
	}

	return s.store.DeleteAbandonedCarts(ctx, now.Add(-s.retention), limit)
}

// AbandonedCartsExpire reports whether a retention period is set, for the
// job's line: "nothing was deleted" and "nothing is ever deleted" are
// different answers.
func (s *Service) AbandonedCartsExpire() bool { return s.retention > 0 }
