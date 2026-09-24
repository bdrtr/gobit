package models

import "time"

// The pricing module's memory of what its ladder read (ADR 0167).
//
// A price a shopper is charged is COMPUTED: the ladder picks among a set's live
// prices, and a list's status and window decide which of them compete at a
// moment. The answer changes without a write — a sale window opens at its
// starts_at whether anybody touches the catalog or not — so what is kept is the
// ladder's input after every write, and the price at a past moment is the
// ladder run over the snapshots that stood then.

// PriceSetSnapshot is a set's live prices, each with its rules, as they stood
// after one write.
type PriceSetSnapshot struct {
	// PriceSetID is the set the snapshot is of.
	PriceSetID string
	// RecordedAt is when the write that produced it happened, on the
	// application's clock — the one the write stamped its own rows with.
	RecordedAt time.Time
	// Prices are the set's live prices after the write. Empty is a set with no
	// price, which is also what a deleted set leaves.
	Prices []Price
}

// PriceListSnapshot is a list's header as it stood after one write.
type PriceListSnapshot struct {
	// RecordedAt is when the write happened, on the application's clock.
	RecordedAt time.Time
	// Info is what the ladder reads of the list: its type, status and window.
	Info PriceListInfo
	// Deleted says the write removed the list. A deleted list offers no price,
	// exactly as the candidate query sees it.
	Deleted bool
}

// AppliedPrice is one stretch of time during which ONE answer of the ladder
// held, for one currency, at quantity one and with no rule context — the price
// a shopper who names nothing is charged, which is the price a storefront shows.
type AppliedPrice struct {
	// From is when the stretch began.
	From time.Time
	// To is when it ended, exclusive. The last stretch ends where the window
	// asked for ends, whether or not the answer changed there.
	To *time.Time
	// Priced reports whether any price applied. A stretch with none is a set
	// that offered nothing in that currency, and it is not a price of zero.
	Priced bool
	// Amount is the price that applied, in minor units.
	Amount int64
	// PriceID is the price row that won the ladder.
	PriceID string
	// PriceListID is the list the winning price belongs to; nil is a base price.
	PriceListID *string
	// PriceListType is that list's type; empty for a base price.
	PriceListType PriceListType
}

// PriceTimeline is the ladder's answer over a stretch of time for one set in
// one currency.
type PriceTimeline struct {
	// PriceSetID and CurrencyCode say which prices.
	PriceSetID   string
	CurrencyCode string
	// From and To are the window that was asked about, [From, To).
	From time.Time
	To   time.Time
	// RecordedSince is the first moment the history holds for the set. Nothing
	// before it is known — ADR 0047 deleted it — so a stretch asked for earlier
	// starts here instead, and Covers says whether the question was answered in
	// full.
	RecordedSince *time.Time
	// Stretches are the ladder's answers in time order, contiguous.
	Stretches []AppliedPrice
}

// Covers reports whether the timeline holds the whole of [from, to).
func (t PriceTimeline) Covers(from time.Time) bool {
	return t.RecordedSince != nil && !t.RecordedSince.After(from)
}

// Lowest returns the lowest price that applied during [from, to) and the
// stretch it applied in; false when no price applied at all.
//
// It reads only what the timeline holds. A caller that needs the WHOLE window
// asks [PriceTimeline.Covers] first, because the lowest price of a window whose
// beginning is unknown is only the lowest price of the part that is known.
func (t PriceTimeline) Lowest(from, to time.Time) (AppliedPrice, bool) {
	var (
		lowest AppliedPrice
		found  bool
	)
	for _, stretch := range t.Stretches {
		if !stretch.Priced || !stretch.From.Before(to) {
			continue
		}
		if stretch.To != nil && !stretch.To.After(from) {
			continue
		}
		if !found || stretch.Amount < lowest.Amount {
			lowest, found = stretch, true
		}
	}

	return lowest, found
}

// Reduction is a price a storefront shows as reduced, and what it was before.
//
// A reduction is a run of stretches whose price came from a SALE list; an
// override list is a different price, not a reduction, and a base price is the
// thing reduced from. The reference is the lowest price that applied in the
// days before the run began — the number a shop announcing a reduction has to
// show (see the service's ReductionReferenceDays).
type Reduction struct {
	// CurrencyCode is the currency the reduction is in.
	CurrencyCode string
	// PriceID is the sale price applied now.
	PriceID string
	// ReducedSince is when the current run of sale prices began; nil when it
	// began before the history did, which is a start nobody can state.
	ReducedSince *time.Time
	// LowestPrior is the lowest price that applied in the reference days before
	// ReducedSince; nil when the history does not reach back that far, or when
	// nothing was priced then. A shop cannot announce a reference it cannot
	// prove, so an unknown one is ABSENT rather than approximated.
	LowestPrior *int64
}

// StorePrice is a price as the storefront shows it: the price, the kind of list
// it comes from, and — for the sale price charged now — its reduction.
//
// Both storefront surfaces are built from it, the product listing through the
// Query provider and the price set endpoint, so a price reads the same on both.
type StorePrice struct {
	// Price is the price, without its rules: a storefront price has none.
	Price Price
	// ListType is the type of the list the price comes from; empty for a base
	// price.
	ListType PriceListType
	// Reduction is set on the sale price a shopper is charged now.
	Reduction *Reduction
}
