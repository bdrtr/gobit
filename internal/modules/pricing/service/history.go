package service

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// The ladder's past (ADR 0167).
//
// The repository keeps what the ladder read after every write; this file runs
// the SAME ladder — [selectPrice], unchanged — over those snapshots, one moment
// at a time, and reports the answers as stretches of time. The moments are the
// only ones at which the answer can change: a write to the set, a write to one
// of its lists, and the edges of a list's window, which open and close with no
// write at all.

// ReductionReferenceDays is how far back a reduction's reference price looks:
// the lowest price that applied in the thirty days before the reduction began.
//
// It is the EU's rule for announcing a price reduction (Directive 98/6/EC,
// Article 6a, added by the Omnibus Directive 2019/2161), and it is a constant
// rather than a setting because it is a sentence a shop has to be able to
// prove, not a preference. A shop under another rule reads the timeline itself.
const ReductionReferenceDays = 30

// timelineQuantity is the quantity the timeline is priced at: one unit, the
// price a storefront shows and the one a reduction is announced on.
const timelineQuantity int32 = 1

// CodeTimelineInvalid reports a timeline request that cannot be answered as
// asked: no currency, or a window that ends before it begins.
const CodeTimelineInvalid = "pricing_timeline_invalid"

// TimelineQuery is what a timeline is asked for.
type TimelineQuery struct {
	// CurrencyCode is the currency; required.
	CurrencyCode string
	// From and To bound the window, [From, To). To defaults to now and From to
	// the reference days before To — the window a reduction announced now
	// would look back over.
	From *time.Time
	To   *time.Time
}

// PriceTimeline answers what one set charged in one currency over a window, at
// quantity one and with no rule context — the price a shopper who names
// nothing is charged, and the one a storefront shows — and which price and
// which list each stretch came from.
func (s *Service) PriceTimeline(
	ctx context.Context, priceSetID string, q TimelineQuery,
) (models.PriceTimeline, error) {
	if err := s.ready(); err != nil {
		return models.PriceTimeline{}, err
	}
	if err := requireID(priceSetID, models.PriceSetIDPrefix, "price set id"); err != nil {
		return models.PriceTimeline{}, err
	}

	currency := strings.ToUpper(strings.TrimSpace(q.CurrencyCode))
	if currency == "" {
		return models.PriceTimeline{}, errors.Invalid(CodeTimelineInvalid,
			"a timeline is in one currency, and none was given")
	}

	to := s.clock()
	if q.To != nil {
		to = *q.To
	}
	from := to.AddDate(0, 0, -ReductionReferenceDays)
	if q.From != nil {
		from = *q.From
	}
	if !from.Before(to) {
		return models.PriceTimeline{}, errors.Invalid(CodeTimelineInvalid,
			"the window has to begin before it ends: %s is not before %s",
			from.Format(time.RFC3339), to.Format(time.RFC3339))
	}

	if _, err := s.repo.GetPriceSet(ctx, priceSetID); err != nil {
		return models.PriceTimeline{}, err
	}

	sets, lists, err := s.history(ctx, []string{priceSetID})
	if err != nil {
		return models.PriceTimeline{}, err
	}

	return buildTimeline(priceSetID, currency, sets[priceSetID], lists, from, to), nil
}

// history loads every snapshot of the given sets and of every list their
// prices ever named.
func (s *Service) history(
	ctx context.Context, priceSetIDs []string,
) (sets map[string][]models.PriceSetSnapshot, lists map[string][]models.PriceListSnapshot, err error) {
	snapshots, err := s.repo.PriceSetHistory(ctx, priceSetIDs)
	if err != nil {
		return nil, nil, err
	}

	bySet := map[string][]models.PriceSetSnapshot{}
	listIDs := map[string]struct{}{}
	for i := range snapshots {
		snapshot := snapshots[i]
		bySet[snapshot.PriceSetID] = append(bySet[snapshot.PriceSetID], snapshot)
		for j := range snapshot.Prices {
			if id := snapshot.Prices[j].PriceListID; id != nil {
				listIDs[*id] = struct{}{}
			}
		}
	}

	ids := make([]string, 0, len(listIDs))
	for id := range listIDs {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	lists, err = s.repo.PriceListHistory(ctx, ids)
	if err != nil {
		return nil, nil, err
	}

	return bySet, lists, nil
}

// buildTimeline runs the ladder over the snapshots at every moment its answer
// can change inside [from, to), and merges the moments that answered the same.
//
// Snapshots are in time order, as the repository returns them. The window
// starts no earlier than the set's first snapshot: before it nothing is known,
// and the timeline says so through RecordedSince rather than by inventing a
// price for a time ADR 0047 deleted.
func buildTimeline(
	priceSetID, currency string,
	sets []models.PriceSetSnapshot,
	lists map[string][]models.PriceListSnapshot,
	from, to time.Time,
) models.PriceTimeline {
	timeline := models.PriceTimeline{
		PriceSetID: priceSetID, CurrencyCode: currency, From: from, To: to,
	}
	if len(sets) == 0 {
		return timeline
	}

	since := sets[0].RecordedAt
	timeline.RecordedSince = &since

	start := from
	if since.After(start) {
		start = since
	}
	if !start.Before(to) {
		return timeline
	}

	moments := changeMoments(sets, lists, start, to)
	for i, at := range moments {
		end := to
		if i+1 < len(moments) {
			end = moments[i+1]
		}

		stretch := appliedAt(sets, lists, currency, at)
		stretch.From = at
		stretchEnd := end
		stretch.To = &stretchEnd

		last := len(timeline.Stretches) - 1
		if last >= 0 && sameAnswer(timeline.Stretches[last], stretch) {
			timeline.Stretches[last].To = &stretchEnd

			continue
		}
		timeline.Stretches = append(timeline.Stretches, stretch)
	}

	return timeline
}

// changeMoments are the instants in [start, to) at which the ladder's answer
// may differ from the instant before: the start itself, every snapshot, and the
// edges of every window a list ever had.
//
// A window's end is INCLUSIVE — [models.PriceListInfo.Usable] still admits the
// list at ends_at — so the change happens at the first instant after it.
func changeMoments(
	sets []models.PriceSetSnapshot,
	lists map[string][]models.PriceListSnapshot,
	start, to time.Time,
) []time.Time {
	moments := []time.Time{start}
	add := func(at time.Time) {
		if at.After(start) && at.Before(to) {
			moments = append(moments, at)
		}
	}

	for i := range sets {
		add(sets[i].RecordedAt)
	}
	for _, snapshots := range lists {
		for i := range snapshots {
			add(snapshots[i].RecordedAt)
			if starts := snapshots[i].Info.StartsAt; starts != nil {
				add(*starts)
			}
			if ends := snapshots[i].Info.EndsAt; ends != nil {
				add(ends.Add(time.Nanosecond))
			}
		}
	}

	slices.SortFunc(moments, func(a, b time.Time) int { return a.Compare(b) })

	return slices.CompactFunc(moments, func(a, b time.Time) bool { return a.Equal(b) })
}

// appliedAt is the ladder's answer at one instant, from the snapshots that
// stood then.
func appliedAt(
	sets []models.PriceSetSnapshot,
	lists map[string][]models.PriceListSnapshot,
	currency string,
	at time.Time,
) models.AppliedPrice {
	set, ok := standingSet(sets, at)
	if !ok {
		return models.AppliedPrice{}
	}

	candidates := make([]models.PriceCandidate, 0, len(set.Prices))
	for i := range set.Prices {
		candidate := models.PriceCandidate{Price: set.Prices[i]}
		if id := set.Prices[i].PriceListID; id != nil {
			if list, ok := standingList(lists[*id], at); ok && !list.Deleted {
				info := list.Info
				candidate.List = &info
			}
		}
		candidates = append(candidates, candidate)
	}

	winner, found := selectPrice(candidates, currency, timelineQuantity, nil, at)
	if !found {
		return models.AppliedPrice{}
	}

	return models.AppliedPrice{
		Priced:        true,
		Amount:        winner.Amount,
		PriceID:       winner.PriceID,
		PriceListID:   winner.PriceListID,
		PriceListType: winner.PriceListType,
	}
}

// standingSet is the last snapshot recorded at or before at.
func standingSet(sets []models.PriceSetSnapshot, at time.Time) (models.PriceSetSnapshot, bool) {
	var (
		standing models.PriceSetSnapshot
		found    bool
	)
	for i := range sets {
		if sets[i].RecordedAt.After(at) {
			break
		}
		standing, found = sets[i], true
	}

	return standing, found
}

// standingList is the last snapshot of a list recorded at or before at.
func standingList(snapshots []models.PriceListSnapshot, at time.Time) (models.PriceListSnapshot, bool) {
	var (
		standing models.PriceListSnapshot
		found    bool
	)
	for i := range snapshots {
		if snapshots[i].RecordedAt.After(at) {
			break
		}
		standing, found = snapshots[i], true
	}

	return standing, found
}

// sameAnswer reports whether two stretches charged the same thing the same way.
func sameAnswer(a, b models.AppliedPrice) bool {
	if a.Priced != b.Priced {
		return false
	}
	if !a.Priced {
		return true
	}

	return a.Amount == b.Amount && a.PriceID == b.PriceID &&
		ptrEqual(a.PriceListID, b.PriceListID) && a.PriceListType == b.PriceListType
}

// ptrEqual compares two optional strings by value.
func ptrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

// reductionFrom reads the reduction a timeline ends in, if it ends in one.
//
// The timeline has to run from the history's start to just past now: the run
// of sale prices is walked back from its last stretch, and the reference window
// ends where the run begins.
func reductionFrom(timeline models.PriceTimeline, referenceDays int) (models.Reduction, bool) {
	stretches := timeline.Stretches
	last := len(stretches) - 1
	if last < 0 || !onSale(stretches[last]) {
		return models.Reduction{}, false
	}

	reduction := models.Reduction{
		CurrencyCode: timeline.CurrencyCode,
		PriceID:      stretches[last].PriceID,
	}

	first := last
	for first > 0 && onSale(stretches[first-1]) {
		first--
	}
	if first == 0 {
		// The run reaches the history's first stretch: it may have begun long
		// before anything was recorded, so neither its start nor its reference
		// can be stated.
		return reduction, true
	}

	since := stretches[first].From
	reduction.ReducedSince = &since

	referenceFrom := since.AddDate(0, 0, -referenceDays)
	if !timeline.Covers(referenceFrom) {
		return reduction, true
	}
	if lowest, found := timeline.Lowest(referenceFrom, since); found {
		amount := lowest.Amount
		reduction.LowestPrior = &amount
	}

	return reduction, true
}

// onSale reports whether a stretch charged a price from a sale list.
func onSale(stretch models.AppliedPrice) bool {
	return stretch.Priced && stretch.PriceListType == models.PriceListSale
}

// reductions computes the reduction of every (set, currency) pair given, from
// one batched read of the history.
//
// The caller names only pairs whose live price is a sale price; a timeline
// whose last answer disagrees with the live one — which a writer that skipped
// its snapshot would cause — announces nothing, because a reference computed
// from a history that is not the catalog's is not a reference.
func (s *Service) reductions(
	ctx context.Context, live map[string]map[string]string, now time.Time,
) (map[string]models.Reduction, error) {
	out := map[string]models.Reduction{}
	if len(live) == 0 {
		return out, nil
	}

	ids := make([]string, 0, len(live))
	for id := range live {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	sets, lists, err := s.history(ctx, ids)
	if err != nil {
		return nil, err
	}

	end := now.Add(time.Nanosecond)
	for _, setID := range ids {
		for currency, livePriceID := range live[setID] {
			timeline := buildTimeline(setID, currency, sets[setID], lists, time.Time{}, end)
			reduction, ok := reductionFrom(timeline, ReductionReferenceDays)
			if !ok || reduction.PriceID != livePriceID {
				continue
			}
			out[livePriceID] = reduction
		}
	}

	return out, nil
}

// storePrices builds what the storefront shows for each set: its listable
// prices, each with its list's type, and the reduction of the sale price a
// shopper is charged now.
//
// It is the ONE path both storefront surfaces take, for the reason
// [Service.ListStorePrices] gives: two surfaces that built their prices apart
// would drift, and a price that read one way in the listing and another on the
// price set endpoint is the drift a shopper would see first.
func (s *Service) storePrices(
	ctx context.Context, candidatesBySet map[string][]models.PriceCandidate, at time.Time,
) (map[string][]models.StorePrice, error) {
	onSale := map[string]map[string]string{}
	listableBySet := map[string][]models.PriceCandidate{}
	for setID, candidates := range candidatesBySet {
		listable := listablePrices(candidates, at)
		listableBySet[setID] = listable
		for _, currency := range currenciesOf(listable) {
			winner, ok := selectPrice(candidates, currency, timelineQuantity, nil, at)
			if !ok || winner.PriceListType != models.PriceListSale {
				continue
			}
			if onSale[setID] == nil {
				onSale[setID] = map[string]string{}
			}
			onSale[setID][currency] = winner.PriceID
		}
	}

	// Only a set a shopper is charged a sale price for now reads its history,
	// and all of them in one round trip (ADR 0004).
	reductions, err := s.reductions(ctx, onSale, at)
	if err != nil {
		return nil, err
	}

	out := make(map[string][]models.StorePrice, len(listableBySet))
	for setID, listable := range listableBySet {
		prices := make([]models.StorePrice, 0, len(listable))
		for i := range listable {
			price := models.StorePrice{Price: listable[i].Price}
			price.Price.Rules = nil
			if listable[i].Price.PriceListID != nil && listable[i].List != nil {
				price.ListType = listable[i].List.Type
			}
			if reduction, ok := reductions[price.Price.ID]; ok {
				reduction := reduction
				price.Reduction = &reduction
			}
			prices = append(prices, price)
		}
		out[setID] = prices
	}

	return out, nil
}
