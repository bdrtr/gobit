package service

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// maxLocationRegions is the largest number of regions that can be bound to a
// single warehouse. The bound prevents a single request from writing unbounded
// rows.
const maxLocationRegions = 100

// SetShippingLocationInput is the shipping policy of a warehouse.
//
// The input is WHOLESALE: every field given is an ABSOLUTE value, and a field
// left out DOES NOT mean "do not change it". If the region list is given empty,
// all of the warehouse's region links are DELETED and the warehouse comes to
// serve every region — there is no way to say "leave the regions as they are".
// The alternative was a surface that told an empty slice apart from a nil slice;
// that distinction is lost while passing through JSON and what the client
// believed it sent would diverge from what the server did.
type SetShippingLocationInput struct {
	// LocationID is the stock location the policy will be written for.
	//
	// It IS NOT verified that the warehouse EXISTS: this module does not know
	// the stock module and there is nobody it could ask whether a location
	// identifier is valid. A policy written for a warehouse that does not exist
	// is HARMLESS — the selection only eliminates and orders the candidates the
	// stock module PRODUCED; it cannot add an element to the set.
	LocationID string
	// Priority is the preference order; THE SMALLER ONE WINS. Zero is the
	// default and coincides with the same rank as a warehouse that has no policy
	// at all; to lift a warehouse above the defaults, a NEGATIVE value is given.
	Priority int64
	// RegionIDs are the shipping regions the warehouse serves. If given EMPTY,
	// the warehouse serves ALL regions (see [Service.RankLocations]).
	//
	// THEIR ORDER IS MEANINGLESS: the links form a set and the read path always
	// returns them sorted by identifier. A repeated identifier is not an error;
	// it is dropped.
	RegionIDs []string
}

// SetShippingLocation writes, or overwrites, a warehouse's shipping policy.
//
// The priority and the region links are written in THE SAME transaction. Had
// they been written separately, an interleaving selection would see the
// warehouse with its new priority but its old regions; worse, a warehouse seen
// while the links were deleted but not yet written would be open to ALL regions,
// and an edit made to narrow the scope would, for a moment, widen it.
func (s *Service) SetShippingLocation(
	ctx context.Context,
	in SetShippingLocationInput,
) (models.ShippingLocation, error) {
	locationID := strings.TrimSpace(in.LocationID)
	if err := requireText("the location identifier", locationID); err != nil {
		return models.ShippingLocation{}, err
	}

	regions, err := normalizeRegionIDs(in.RegionIDs)
	if err != nil {
		return models.ShippingLocation{}, err
	}

	var out models.ShippingLocation
	txErr := s.store.WithTx(ctx, func(ctx context.Context) error {
		if _, upErr := s.store.UpsertShippingLocation(ctx, locationID, in.Priority); upErr != nil {
			return upErr
		}
		if repErr := s.store.ReplaceShippingLocationRegions(ctx, locationID, regions); repErr != nil {
			return repErr
		}
		saved, getErr := s.store.GetShippingLocation(ctx, locationID)
		if getErr != nil {
			return getErr
		}
		out = saved
		return nil
	})
	if txErr != nil {
		return models.ShippingLocation{}, txErr
	}

	s.log.InfoContext(ctx, "the warehouse shipping policy was written",
		"location_id", locationID, "priority", in.Priority, "regions", len(regions))
	return out, nil
}

// GetShippingLocation returns the warehouse's policy with its regions.
//
// NotFound is returned for a warehouse that has no policy, and that DOES NOT
// MEAN "there is no such warehouse": this module does not know whether
// warehouses exist, it only reports that it has no record of its own. A
// warehouse without a policy is valid in the selection and gets the default
// behavior.
func (s *Service) GetShippingLocation(
	ctx context.Context,
	locationID string,
) (models.ShippingLocation, error) {
	trimmed := strings.TrimSpace(locationID)
	if err := requireText("the location identifier", trimmed); err != nil {
		return models.ShippingLocation{}, err
	}
	return s.store.GetShippingLocation(ctx, trimmed)
}

// ListShippingLocations returns the written policies in priority order; the
// second value is the count of ALL rows.
//
// The list contains only the warehouses THAT HAVE A POLICY. The full list of the
// warehouses in the setup lives in the stock module and is not visible from
// here; combining the two lists is the admin surface's job.
func (s *Service) ListShippingLocations(
	ctx context.Context,
	page Page,
) ([]models.ShippingLocation, int64, error) {
	normalized, err := page.normalize()
	if err != nil {
		return nil, 0, err
	}
	return s.store.ListShippingLocations(ctx, models.LocationFilter{
		Limit:  normalized.Limit,
		Offset: normalized.Offset,
	})
}

// DeleteShippingLocation deletes the warehouse's policy; NotFound if absent.
//
// Deleting DOES NOT CLOSE the warehouse, it RETURNS IT TO THE DEFAULT: a
// warehouse with no record is taken to be at priority zero and to serve every
// region. Removing a warehouse from candidacy is not within the shipping
// module's authority — the candidate list is produced by a stock fact.
func (s *Service) DeleteShippingLocation(ctx context.Context, locationID string) error {
	trimmed := strings.TrimSpace(locationID)
	if err := requireText("the location identifier", trimmed); err != nil {
		return err
	}
	if err := s.store.DeleteShippingLocation(ctx, trimmed); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "the warehouse shipping policy was deleted", "location_id", trimmed)
	return nil
}

// RankLocations lines the candidates up in PREFERENCE ORDER: the fulfillment
// leaves from the first one.
//
// # The decision stops here because it is a SHIPPING decision
//
// Which warehouse to ship from is a shipping decision; its rules look at the
// shipping region and at the operator's preference order. "Which locations have
// enough stock", on the other hand, is a STOCK FACT and comes from the stock
// module's surface. The division of labor is deliberate: gathering the two
// halves into one module would make the stock query depend on the shipping
// policy, or the shipping policy depend on the stock schema.
//
// That is why the cart flow does not build the order ITSELF: it takes the
// candidates from stock and asks here for the order.
//
// # Why an ORDER is returned rather than a single location
//
// The caller may try to reserve at the first warehouse and fail: the candidates
// are read without a lock and the chosen warehouse can run out in the window in
// between. Had a single location been returned, the caller would have nowhere to
// fall back to, or this surface would be called AGAIN on every stock-out and the
// same records would be read over and over for the same order. The order is
// computed once; the cost of the second and third attempt is zero.
//
// # Policy: ELIMINATE, RANK, BREAK THE TIE
//
// In order:
//
//  1. ELIMINATION — if at least one region is bound to a warehouse and
//     destinationRegionID is NOT among them, the candidate drops. A warehouse
//     with no region bound to it serves ALL regions and is not eliminated.
//  2. RANKING — the remaining ones are lined up by
//     [models.LocationPolicy.Priority] from smallest to largest. A warehouse with
//     no policy record is at priority zero, that is, at THE SAME rank as a
//     warehouse whose priority is explicitly written as zero.
//  3. TIE BREAKING — at equal priority the smaller identifier goes first.
//
// If there is no policy record at all, the result of the three steps is the third
// step alone: the head of the order is exactly the selection that was made
// BEFORE this policy was added.
//
// # The returned slice is a SUBSET of the input
//
// The elements are EXACTLY the same strings as the elements of
// candidateLocationIDs; not a normalized copy and not a twin read from the policy
// row. The matching is done with the leading and trailing whitespace stripped,
// but the returned value is still the string the caller gave: the caller looks
// the result up in its own candidate ledger and, if it cannot find it, drops the
// flow as an internal error.
//
// The same candidate never appears twice in the slice.
//
// # What it DOES NOT GUARANTEE
//
// Three things are outside this surface and the reader must not expect them from
// it:
//
//   - The stock DISTRIBUTION does not enter the decision. "Put the warehouse
//     with the most stock first" cannot be expressed. The data DOES EXIST in the
//     stock module — it already computes the sellable count per location while
//     producing the candidate list — but it is not on the cross-module PRIMITIVE
//     surface, and adding it there would touch the stock module's "no location
//     breakdown leaks to the storefront" boundary. The second and heavier reason
//     is determinism: the policy is the operator's SETTING and its changing is an
//     expected consequence, whereas stock is a fast-changing fact and the same
//     defense does not work there.
//   - COST does not enter the decision. There is no tariff model between a
//     warehouse and a carrier; had one been written, the data it rested on would
//     be made up.
//   - No decision is made at the ORDER LEVEL. The order is asked for PER LINE and
//     this surface does not see the whole cart; "take all the lines out of a
//     single warehouse" or "reduce the number of shipments" cannot be expressed
//     here.
//
// In this system "proximity" is NOT geographic distance but shipping region
// coverage: warehouses have no coordinates and none were made up.
//
// # The order is deterministic, but with respect to what
//
// With the same candidates AND the same policy records, a second call returns
// the same order; the result is independent of the ARRIVAL ORDER of the
// candidates. If the operator changes the policy between two calls the order
// changes too — that is the expected consequence of a setting and the
// determinism claim DOES NOT COVER it.
//
// The candidate slice is not sorted, it is COPIED: sorting it in place would be
// corrupting the caller's slice, and a decision surface cannot modify the data
// it is handed.
//
// # An empty result is a Conflict
//
// There are two separate emptinesses and both return errors.Conflict:
//
//   - If the candidate list arrives EMPTY, [CodeNoShippingLocation]. What is
//     missing is the state of the world, not the shape of the request;
//     errors.Invalid would have been wrong.
//   - If ALL the candidates ARE ELIMINATED, [CodeNoServiceableLocation]. The code
//     is separate because the work the operator has to do is separate too: in the
//     first there is no stock, in the second the region coverage of the
//     warehouses is set up wrong.
//
// The kind being Conflict determines two things at once and both are measurable:
// the error's HTTP counterpart (the caller preserves the kind while wrapping the
// error, 409) and its retryability (the engine's default predicate DOES NOT RETRY
// KindConflict, it retries KindInternal). An eliminated candidate set does not
// change by trying again; had Internal been chosen, a configuration error the
// operator has to fix by hand would be mistaken for a transient fault and
// repeated the day compensation retries were switched on.
//
// The code REACHES THE CALLER: while wrapping a step failure the cart flow
// INHERITS the underlying error's code, which means the code the storefront
// client sees in the body is the one from here. This is not an assumption but the
// cart flow's written contract, and it has a measured reason — had the code been
// overwritten, "stock could not be reserved" would be reported with full shelves.
//
// If destinationRegionID is empty, errors.Invalid is returned. This is a DEFENSE:
// today's only caller is the cart flow and a plan with an empty region cannot
// even be built there. The reason the surface defends itself is that skipping the
// elimination while the coverage cannot be evaluated would mean silently putting
// a warehouse that DOES NOT SERVE that region first. An EMPTY identifier in the
// candidate list is rejected for the same reason.
func (s *Service) RankLocations(
	ctx context.Context,
	destinationRegionID string,
	candidateLocationIDs []string,
) ([]string, error) {
	if len(candidateLocationIDs) == 0 {
		return nil, errors.Conflict(CodeNoShippingLocation,
			"there is no location the fulfillment can leave from")
	}

	regionID := strings.TrimSpace(destinationRegionID)
	if err := requireText("the destination region identifier", regionID); err != nil {
		return nil, err
	}

	candidates := make([]locationCandidate, 0, len(candidateLocationIDs))
	keys := make([]string, 0, len(candidateLocationIDs))
	for i, candidate := range candidateLocationIDs {
		key := strings.TrimSpace(candidate)
		if key == "" {
			return nil, errors.Invalid(CodeInvalidInput,
				"a candidate location identifier cannot be empty (candidate %d)", i+1)
		}
		if err := checkTextLen("the candidate location identifier", key); err != nil {
			return nil, err
		}
		candidates = append(candidates, locationCandidate{original: candidate, key: key})
		keys = append(keys, key)
	}

	rows, err := s.store.LocationPolicies(ctx, keys)
	if err != nil {
		return nil, err
	}

	policies := make(map[string]models.LocationPolicy, len(rows))
	for _, row := range rows {
		policies[row.LocationID] = row
	}

	ranked := rankLocations(regionID, candidates, policies)
	if len(ranked) == 0 {
		return nil, errors.Conflict(CodeNoServiceableLocation,
			"no warehouse serves the region %s; the eliminated candidates: %s",
			regionID, eliminatedSummary(candidates, policies))
	}
	return ranked, nil
}

// eliminatedSummary writes, for the error message, the regions the candidates
// are bound to.
//
// The summary makes the sneakiest cause of elimination visible: a dead region
// identifier. If the operator deletes a region and reopens it under the same
// name the identifier changes, the policy rows keep carrying the old one, and
// EVERY order in the store is eliminated. With a message that only said "no
// warehouse serves it", the operator could not see that the identifiers had
// diverged.
//
// WHERE the summary is seen is a separate question and must not be overstated:
// only the CODE reaches the storefront client's body. This text stands in the
// server log and in the cart flow's execution record, which means its reader is
// the operator.
//
// # When it is called, every candidate HAS a policy and its links ARE FILLED IN
//
// The function is called only when the order came out empty; the order being
// empty means every candidate was eliminated, and the elimination rule only drops
// a candidate that HAS a record and whose links ARE FILLED IN (both a candidate
// with no record and one with empty links serve every region). That is why there
// is NO branch here for the "no policy" or "no links" case: had they been
// written, they would be two lines that never run and therefore can never be
// exercised.
func eliminatedSummary(candidates []locationCandidate, policies map[string]models.LocationPolicy) string {
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		policy := policies[candidate.key]
		parts = append(parts,
			candidate.key+" → ["+strings.Join(policy.RegionIDs, " ")+"]")
	}
	return strings.Join(parts, ", ")
}

// locationCandidate carries a candidate's FORM as it came from the caller and
// the key used in the matching together.
//
// Keeping the two apart is mandatory: the matching is done with the
// whitespace-stripped key, while the returned value is the string the caller
// gave. Had there been a single field, a caller that wrote " sloc_a " would get
// back the answer "sloc_a", which it could not find in its candidate ledger.
type locationCandidate struct {
	original string
	key      string
}

// rankLocations applies the policy to the candidates and returns the preference
// order.
//
// The function is PURE and does not touch the database: the decision itself can
// thereby be exercised without a real Postgres, with policy sets built one by
// one. The same split is made in shipping option eligibility — the cheap
// elimination in SQL, the rule itself here.
//
// policies carries ONLY the candidates that have a record; a candidate absent
// from the map is at the default (zero priority, serves every region). The
// distinction is made here because "no record" and "has a record but its priority
// is zero" have to give THE SAME result, and the query does not need to know
// that.
//
// The sort is STABLE and has two keys (priority, then key); because the key is
// unique among the candidates, the result is independent of the input's order.
func rankLocations(
	regionID string,
	candidates []locationCandidate,
	policies map[string]models.LocationPolicy,
) []string {
	type rankedCandidate struct {
		original string
		key      string
		priority int64
	}

	remaining := make([]rankedCandidate, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, dup := seen[candidate.key]; dup {
			continue
		}
		seen[candidate.key] = struct{}{}

		policy, configured := policies[candidate.key]
		if configured && !policy.ServesRegion(regionID) {
			continue
		}

		priority := int64(0)
		if configured {
			priority = policy.Priority
		}
		remaining = append(remaining, rankedCandidate{
			original: candidate.original,
			key:      candidate.key,
			priority: priority,
		})
	}

	slices.SortFunc(remaining, func(a, b rankedCandidate) int {
		if a.priority != b.priority {
			return cmp.Compare(a.priority, b.priority)
		}
		return strings.Compare(a.key, b.key)
	})

	out := make([]string, 0, len(remaining))
	for _, candidate := range remaining {
		out = append(out, candidate.original)
	}
	return out
}

// normalizeRegionIDs validates the region identifiers and drops the DUPLICATES.
//
// A repeated identifier is not an error: saying "bind the same region twice"
// gives the same result as binding it once, and meeting the caller with a
// conflict error would drop the request even though there is nothing to fix.
//
// The ORDER of the input IS MEANINGLESS and is not preserved: region links form a
// set, not a list. The read path always returns them sorted BY IDENTIFIER, which
// means two requests writing the same set in different orders produce the same
// record. This function's elimination order is stable only within itself.
func normalizeRegionIDs(regionIDs []string) ([]string, error) {
	if len(regionIDs) > maxLocationRegions {
		return nil, errors.Invalid(CodeInvalidInput,
			"at most %d regions can be bound to a warehouse: %d", maxLocationRegions, len(regionIDs))
	}

	seen := make(map[string]struct{}, len(regionIDs))
	out := make([]string, 0, len(regionIDs))
	for i, regionID := range regionIDs {
		trimmed := strings.TrimSpace(regionID)
		if trimmed == "" {
			return nil, errors.Invalid(CodeInvalidInput,
				"a region identifier cannot be empty (region %d)", i+1)
		}
		if err := checkTextLen("the region identifier", trimmed); err != nil {
			return nil, err
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out, nil
}
