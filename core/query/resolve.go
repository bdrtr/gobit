package query

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
)

// validateSpec checks the query definition before it enters the core.
func validateSpec(spec GraphSpec) error {
	if spec.Entity == "" {
		return errors.Invalid(codeInvalidSpec, "GraphSpec.Entity cannot be empty")
	}
	if spec.Limit < 0 {
		return errors.Invalid(codeInvalidSpec, "GraphSpec.Limit cannot be negative (given: %d)", spec.Limit)
	}
	if spec.Offset < 0 {
		return errors.Invalid(codeInvalidSpec, "GraphSpec.Offset cannot be negative (given: %d)", spec.Offset)
	}

	total, err := validateExpansions(spec.Expand, 0)
	if err != nil {
		return err
	}
	if total > maxExpansions {
		return errors.Invalid(codeInvalidSpec,
			"the number of expansions exceeded the limit of %d (given: %d)", maxExpansions, total)
	}
	return nil
}

// validateExpansions checks the expansion tree: an empty link name, an output
// key that overwrites the join key, an output key clashing at the same level and
// excessive depth are all rejected. The count returned is the TOTAL number of
// expansions in the tree; the caller applies the width limit.
//
// Validation runs BEFORE any provider is called, over the whole tree; a broken
// spec never causes half the work to be done.
func validateExpansions(exps []Expansion, depth int) (int, error) {
	if len(exps) == 0 {
		return 0, nil
	}
	if depth >= maxExpandDepth {
		return 0, errors.Invalid(codeInvalidSpec,
			"the expansion depth exceeded the limit of %d", maxExpandDepth)
	}

	total := len(exps)
	seen := make(map[string]struct{}, len(exps))
	for _, exp := range exps {
		if exp.Link == "" {
			return 0, errors.Invalid(codeInvalidSpec, "Expansion.Link cannot be empty")
		}
		key := outputKey(exp)
		if key == IDField {
			return 0, errors.Invalid(codeInvalidSpec,
				"an expansion output key cannot be %q; %q is the record's join key and overwriting it loses the record's identity (give the %q expansion another name with As)",
				IDField, IDField, exp.Link)
		}
		if _, dup := seen[key]; dup {
			return 0, errors.Invalid(codeInvalidSpec,
				"the key %q is written by more than one expansion at the same level; separate them with As", key)
		}
		seen[key] = struct{}{}

		nested, err := validateExpansions(exp.Expand, depth+1)
		if err != nil {
			return 0, err
		}
		total += nested
	}
	return total, nil
}

// outputKey returns the key the expansion is written under; Link when As is
// empty.
func outputKey(exp Expansion) string {
	if exp.As != "" {
		return exp.As
	}
	return exp.Link
}

// ctxErr returns a typed error when the context is canceled, and nil otherwise.
//
// It is called BEFORE going to a provider or the link service: starting new work
// with a canceled context is pointless. what names the step that was canceled in
// the message.
func ctxErr(ctx context.Context, what string) error {
	if err := ctx.Err(); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, codeCanceled, "%s was canceled", what)
	}
	return nil
}

// fieldsWithID copies the field list and adds the id field when it is needed.
//
// An empty list (meaning the provider's default fields are wanted) is left
// alone; the id field is assumed to be in the default set. The caller's slice is
// never modified.
func fieldsWithID(fields []string, need bool) []string {
	if len(fields) == 0 {
		return nil
	}
	out := slices.Clone(fields)
	if need && !slices.Contains(out, IDField) {
		out = append(out, IDField)
	}
	return out
}

// collectIDs gathers the ids of every record at this level, PRESERVING ORDER
// and de-duplicating; it also returns the map from an id to the records holding
// it.
//
// The same id can appear in more than one record (the same record listed twice,
// say); the map therefore holds a slice and the expansion result is written to
// all of them.
//
// A SINGLE record whose id cannot be read produces a typed error. That record
// cannot enter the link, so it never receives the expansion key; skipping it
// would mean some records from the same call carrying the key and others not,
// and missing data looking like a correct result. The rule is the one indexByID
// applies to fetched records, and it is the package's "no partial results"
// policy.
func collectIDs(records []Record, entity, linkName string) (ids []string, byID map[string][]Record, err error) {
	ids = make([]string, 0, len(records))
	byID = make(map[string][]Record, len(records))

	for _, rec := range records {
		id, ok := recordID(rec)
		if !ok {
			return nil, nil, errors.Internal(codeMissingID,
				"the %q expansion cannot be made: the id of one of the %q records could not be read (%q %s)",
				linkName, entity, IDField, recordIDProblem(rec)).
				WithDetails(map[string]any{detailLink: linkName, detailEntity: entity, detailField: IDField})
		}
		if _, seen := byID[id]; !seen {
			ids = append(ids, id)
		}
		byID[id] = append(byID[id], rec)
	}
	return ids, byID, nil
}

// indexByID indexes the fetched records by id.
//
// A record whose id cannot be read cannot be attached to its parent; dropping it
// silently would make missing data look like a correct result, so a typed error
// is returned.
func indexByID(records []Record, entity string) (map[string]Record, error) {
	byID := make(map[string]Record, len(records))
	for _, rec := range records {
		id, ok := recordID(rec)
		if !ok {
			return nil, errors.Internal(codeMissingID,
				"the id of a record returned by the %q provider could not be read (%q %s); the join cannot be made",
				entity, IDField, recordIDProblem(rec)).
				WithDetails(map[string]any{detailEntity: entity, detailField: IDField})
		}
		byID[id] = rec
	}
	return byID, nil
}

// recordID returns the record's id. The second result is false when the field
// is absent, is not a string, or is empty.
func recordID(rec Record) (string, bool) {
	raw, ok := rec[IDField]
	if !ok {
		return "", false
	}
	id, ok := raw.(string)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// recordIDProblem explains WHY the id could not be read, in a form fit for the
// message.
//
// Telling "the field is absent" from "the field is there but has the wrong
// type" is critical for diagnosis: in a provider fed by pgx.RowToMap a uuid
// column arrives as [16]byte rather than a string, and a message that only said
// "the field is absent" would blame the wrong side.
func recordIDProblem(rec Record) string {
	raw, ok := rec[IDField]
	if !ok {
		return "field is absent"
	}
	if s, isString := raw.(string); isString && s == "" {
		return "field is an empty string"
	}
	return fmt.Sprintf("field is of type %T, a string was expected", raw)
}

// ownRecords produces copies of the provider's records that BELONG TO THE CALL.
//
// Query writes the expansion result into the record; when a provider shares the
// map it returns with its own state, that write corrupts it: the module's own
// reads carry a foreign key, a stale field leaks into later calls, and two
// concurrent Graph calls writing to the same map produce a data race. Copying at
// the boundary rather than writing "copy your records" into the provider
// contract gives structural protection INDEPENDENT of what the provider does.
//
// The copy is shallow: the field values stay shared, but Query writes only
// top-level keys and never touches the inside of a value.
func ownRecords(records []Record) []Record {
	out := make([]Record, len(records))
	for i, rec := range records {
		out[i] = maps.Clone(rec)
	}
	return out
}

// uniqueValues de-duplicates every related id coming out of the link
// resolution.
//
// The order is deterministic: the root ids are sorted and each one's related ids
// are appended in the order they arrived. A deterministic order helps both
// caching on the provider side and assertions in tests.
func uniqueValues(related map[string][]string) []string {
	out := make([]string, 0, len(related))
	seen := make(map[string]struct{}, len(related))

	for _, key := range slices.Sorted(maps.Keys(related)) {
		for _, id := range related[key] {
			if id == "" {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// shape turns the related ids into the result value the cardinality calls for.
//
// With many true it always returns []Record (an EMPTY slice, not nil, when
// nothing matches); with false it returns the first matching [Record], or nil.
// On an end that writes a single record, the first of several links is taken —
// so a link whose cardinality was declared wrongly does not silently produce a
// slice and change the SHAPE of the result.
func shape(ids []string, byID map[string]Record, many bool) any {
	if !many {
		for _, id := range ids {
			if rec, ok := byID[id]; ok {
				return rec
			}
		}
		return nil
	}

	out := make([]Record, 0, len(ids))
	for _, id := range ids {
		if rec, ok := byID[id]; ok {
			out = append(out, rec)
		}
	}
	return out
}

// targetSide resolves which end of the link is traveled from and to.
//
// When the root entity is on the link's From end the direction is forward
// (reverse=false); on the To end it is reverse (reverse=true). When both ends
// are the same entity (a self link) the forward direction is chosen. When the
// link does not touch the root entity at all it returns errors.KindInvalid.
func targetSide(def link.LinkDefinition, entity string) (target string, reverse bool, err error) {
	// The ends are matched by ENTITY name, not by module name: one module can
	// offer several entities (see link.LinkSide.Entity).
	from, to := def.From.EntityName(), def.To.EntityName()
	switch {
	case from == entity:
		return to, false, nil
	case to == entity:
		return from, true, nil
	default:
		return "", false, errors.Invalid(codeLinkMismatch,
			"the %q link does not connect to the %q entity; the link's ends are %q and %q",
			def.Name, entity, from, to).
			WithDetails(map[string]any{detailLink: def.Name, detailEntity: entity})
	}
}

// writesMany reports whether the expansion writes a slice or a single record.
//
// Cardinality is DIRECTIONAL: [link.OneToMany] means "one From record, many To
// records". Resolved in the reverse direction (from the To end to the From end)
// the same link is singular, so a single record is written. [link.ManyToMany]
// writes a slice in both directions and [link.OneToOne] a single record in
// both.
func writesMany(def link.LinkDefinition, reverse bool) (bool, error) {
	switch def.Cardinality {
	case link.OneToOne:
		return false, nil
	case link.OneToMany:
		return !reverse, nil
	case link.ManyToMany:
		return true, nil
	default:
		return false, errors.Invalid(codeCardinality,
			"the cardinality of the %q link is not recognized: %s", def.Name, def.Cardinality).
			WithDetails(map[string]any{detailLink: def.Name})
	}
}
