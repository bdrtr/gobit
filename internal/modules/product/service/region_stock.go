package service

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// LocationRanker is fulfillment's ranking of warehouses for a destination
// region: the answer the checkout asks before it reserves a line (ADR 0010),
// and the one a storefront read that names a region counts by (ADR 0422).
//
// The surface is declared here and satisfied structurally by fulfillment's
// interop, resolved by name; internal/arch pins the name.
type LocationRanker interface {
	// RankLocations answers the candidates that serve the region, in
	// preference order, as a subset of the strings it was given.
	RankLocations(ctx context.Context, destinationRegionID string, candidateLocationIDs []string) ([]string, error)
}

// CodeNoServiceableLocation is fulfillment's answer when no candidate serves
// the region (ADR 0010). The storefront reads it as "nothing here serves this
// region", not as a failure: a drift would turn that answer back into the
// channel's, which is the gap ADR 0422 closed, so internal/arch binds it to
// fulfillment's constant.
const CodeNoServiceableLocation = "fulfillment_no_serviceable_location"

// CodeRankerUnavailable reports an installation whose fulfillment ranking is
// not bound; a storefront read then counts as if it named no region.
const CodeRankerUnavailable = "product_ranker_unavailable"

// regionServes judges, for each warehouse, whether the checkout would rank it
// for the region (ADR 0422).
//
// The second result is false when the region could not be judged: fulfillment
// is not installed, or its ranking failed. The read then keeps the channel's
// answer and says so in the log, as a binding that cannot be read does
// ([Service.locationsServingChannels]): the badge is a display concern and the
// checkout still refuses what it cannot ship.
//
// An empty candidate list is judged without a call: no warehouse holds
// anything the read could count.
func (s *Service) regionServes(ctx context.Context, region string, ids []string) (map[string]bool, bool) {
	judged := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return judged, true
	}
	if s.ranker == nil {
		return nil, false
	}

	ranked, err := s.ranker.RankLocations(ctx, region, ids)
	switch {
	case errors.CodeOf(err) == CodeNoServiceableLocation:
		// Every candidate is bound to other regions: the checkout ranks none
		// of them, so the read counts none of them.
	case errors.CodeOf(err) == CodeRankerUnavailable:
		s.log.DebugContext(ctx, "fulfillment is not installed; a storefront read names a region it cannot narrow by",
			"region_id", region)

		return nil, false
	case err != nil:
		s.log.ErrorContext(ctx,
			"the warehouses serving the shopper's region could not be read; the stock badge counts "+
				"the channel's warehouses for this response",
			"region_id", region, "error", err)

		return nil, false
	}
	for _, id := range ids {
		judged[id] = slices.Contains(ranked, id)
	}

	return judged, true
}

// regionCandidates are the warehouses a narrowed read may count for a region:
// those the page's breakdowns name, among the channel's when a channel binds
// warehouses (nil counts every warehouse), in a stable order.
func regionCandidates(extras map[string]enrichment, channel map[string]bool) []string {
	seen := map[string]bool{}
	for _, extra := range extras {
		for id := range extra.sellableByLocation {
			if channel == nil || channel[id] {
				seen[id] = true
			}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	return ids
}

// narrowToRegion is the set of warehouses a read naming a region counts: the
// candidates the checkout ranks for it (ADR 0422). It is never nil, so an empty
// set counts nothing; when the region cannot be judged it answers the channel's
// set unchanged and false.
func (s *Service) narrowToRegion(
	ctx context.Context, region string, extras map[string]enrichment, channel map[string]bool,
) (served, judged map[string]bool, narrowed bool) {
	judged, ok := s.regionServes(ctx, region, regionCandidates(extras, channel))
	if !ok {
		return channel, nil, false
	}
	served = make(map[string]bool, len(judged))
	for id, serves := range judged {
		if serves {
			served[id] = true
		}
	}

	return served, judged, true
}

// trimmedRegion is the region a storefront read names, or "" for none.
func trimmedRegion(regionID string) string {
	return strings.TrimSpace(regionID)
}

// restockCounted is the set a restock date is read over (ADR 0399, ADR 0422):
// the badge's, and, for a read narrowed to a region, the forecast's warehouses
// the breakdown did not name, judged by the same rule in one more call.
// Inventory leaves a warehouse with nothing on sale out of the breakdown, so
// the one a receipt is expected at is often not among those the badge judged.
//
// A read that was not narrowed, because it named no region or because the
// ranking could not judge the one it named, dates over the badge's own set
// unchanged: the channel's answer, nil for every warehouse. A narrowed read
// whose second call cannot be judged falls back the same way, to the channel's
// answer, rather than to the warehouses the first call happened to judge: a
// page whose breakdown named none made no first call at all, and counting
// nothing for its date would hide a receipt the channel's answer shows.
func (s *Service) restockCounted(
	ctx context.Context, read storeRead, forecasts map[string]map[string]time.Time,
) map[string]bool {
	if !read.narrowed {
		return read.served
	}

	seen := map[string]bool{}
	for _, byLocation := range forecasts {
		for id := range byLocation {
			if _, done := read.judged[id]; done {
				continue
			}
			if read.channel == nil || read.channel[id] {
				seen[id] = true
			}
		}
	}
	if len(seen) == 0 {
		return read.served
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	judged, ok := s.regionServes(ctx, read.region, ids)
	if !ok {
		return read.channel
	}
	// A narrowed read's set is never nil ([Service.narrowToRegion]), so the
	// clone is a set of its own to add to.
	counted := maps.Clone(read.served)
	for id, serves := range judged {
		if serves {
			counted[id] = true
		}
	}

	return counted
}
