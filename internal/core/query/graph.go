package query

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
)

// The error codes; a caller can branch on them through errors.CodeOf.
const (
	codeInvalidSpec      = "query_invalid_spec"
	codeNoContainer      = "query_container_missing"
	codeNoLinkService    = "query_link_service_missing"
	codeProviderNotFound = "query_provider_not_found"
	codeProviderInvalid  = "query_provider_invalid"
	codeProviderMismatch = "query_provider_entity_mismatch"
	codeProviderFailed   = "query_provider_failed"
	codeLinkDefFailed    = "query_link_definition_failed"
	codeLinkMismatch     = "query_link_entity_mismatch"
	codeLinkFailed       = "query_link_failed"
	codeCardinality      = "query_unknown_cardinality"
	codeMissingID        = "query_record_id_missing"
	codeCanceled         = "query_canceled"
)

// The keys used in the error details (errors.Error.Details). They are kept
// stable so a caller can rely on the details.
const (
	detailEntity = "entity"
	detailLink   = "link"
	detailName   = "looked_up_name"
	detailField  = "field"
)

// run is the state of a single [Query.Graph] call.
//
// Definition and provider resolutions are cached for the duration of the call:
// even when the same link or the same entity appears at several levels, the
// container and the link service are asked once. The cache lasts FOR THE CALL
// and is not shared between two Graph calls, so a link defined later is seen on
// the next call.
type run struct {
	res       *resolver
	defs      map[string]link.LinkDefinition
	providers map[string]Provider
}

// Graph fetches the root records according to the spec and applies the
// expansions.
//
// With no root records it returns an empty (non-nil) slice and a nil error. An
// error at any level brings the whole call down; no partial result is returned.
//
// The expansion tree is resolved BEFORE any data is fetched (see plan), and the
// records in the tree returned belong to the caller (see ownRecords).
func (r *resolver) Graph(ctx context.Context, spec GraphSpec) ([]Record, error) {
	if err := ctxErr(ctx, "the query"); err != nil {
		return nil, err
	}
	if err := validateSpec(spec); err != nil {
		return nil, err
	}
	if r.c == nil {
		return nil, errors.Internal(codeNoContainer,
			"query was built without a container; the %q provider cannot be resolved", spec.Entity+ProviderSuffix)
	}

	rn := &run{
		res:       r,
		defs:      make(map[string]link.LinkDefinition),
		providers: make(map[string]Provider),
	}

	provider, err := rn.provider(spec.Entity)
	if err != nil {
		return nil, err
	}

	// The tree is resolved BEFORE the data, for two reasons:
	//   1. A broken spec (an unknown link, an unregistered target provider)
	//      fails without paying for the root query.
	//   2. A deterministic spec error is NOT HIDDEN BEHIND a transient error
	//      from a provider. Otherwise a "the database is unreachable" error
	//      would mask a misspelled link name, which is the thing that actually
	//      needs fixing.
	nodes, err := rn.plan(ctx, spec.Entity, spec.Expand)
	if err != nil {
		return nil, err
	}

	roots, err := provider.List(ctx, ListOptions{
		Fields:  fieldsWithID(spec.Fields, len(spec.Expand) > 0),
		Filters: spec.Filters,
		Limit:   spec.Limit,
		Offset:  spec.Offset,
	})
	if err != nil {
		return nil, wrapCallErr(err, codeProviderFailed,
			"the List call of the %q provider failed", spec.Entity)
	}
	if len(roots) == 0 {
		return []Record{}, nil
	}

	roots = ownRecords(roots)
	if err := rn.expand(ctx, spec.Entity, roots, nodes); err != nil {
		return nil, err
	}
	return roots, nil
}

// planNode is a single resolved expansion: the target entity, the direction
// traveled and the shape of the result are all decided WITHOUT LOOKING AT THE
// DATA.
type planNode struct {
	exp      Expansion
	def      link.LinkDefinition
	target   string
	reverse  bool
	many     bool
	children []planNode
}

// plan resolves the expansion tree BEFORE any data is fetched.
//
// For each level the link definition is read, the direction (targetSide) and
// the shape of the result (writesMany) are decided, and the target provider is
// verified to be registered in the container. A wrong link name, a link that
// does not connect to the root entity, an unrecognized cardinality and a
// forgotten provider registration are therefore reported INDEPENDENTLY OF THE
// DATA: the same error comes back deterministically even when the level above
// fetched no record at all. Otherwise a broken query definition would pass
// tests with empty fixtures and blow up with the first real data.
//
// Because definitions and providers are cached for the call, this pre-pass adds
// no round trip during the expansion.
func (rn *run) plan(ctx context.Context, entity string, exps []Expansion) ([]planNode, error) {
	if len(exps) == 0 {
		return nil, nil
	}

	nodes := make([]planNode, 0, len(exps))
	for _, exp := range exps {
		if err := ctxErr(ctx, "the "+exp.Link+" expansion"); err != nil {
			return nil, err
		}

		def, err := rn.definition(ctx, exp.Link)
		if err != nil {
			return nil, err
		}
		target, reverse, err := targetSide(def, entity)
		if err != nil {
			return nil, err
		}
		many, err := writesMany(def, reverse)
		if err != nil {
			return nil, err
		}
		if _, err := rn.provider(target); err != nil {
			return nil, err
		}
		children, err := rn.plan(ctx, target, exp.Expand)
		if err != nil {
			return nil, err
		}

		nodes = append(nodes, planNode{
			exp: exp, def: def, target: target, reverse: reverse, many: many, children: children,
		})
	}
	return nodes, nil
}

// expand applies the plan nodes to EVERY record in the parents slice.
//
// For each expansion, in order: the ids of every record at this level are
// collected in one pass, the links are resolved in one round and ONE
// FetchByIDs goes to the target module. There is NO provider call per record,
// and the same rule holds at nested levels.
//
// The expanded records are written into the parent BY REFERENCE (a Record is a
// map), so a nested expansion also updates the copy at the level above and no
// merge step is needed. The maps written are not the provider's own but the
// call-owned copies taken by ownRecords.
func (rn *run) expand(ctx context.Context, entity string, parents []Record, nodes []planNode) error {
	// The nodes carry the link definition, so they are walked by address rather
	// than copied.
	for i := range nodes {
		node := &nodes[i]

		if err := ctxErr(ctx, "the "+node.exp.Link+" expansion"); err != nil {
			return err
		}

		ids, byParent, err := collectIDs(parents, entity, node.exp.Link)
		if err != nil {
			return err
		}

		related, err := rn.resolveLinks(ctx, node.def, ids, node.reverse)
		if err != nil {
			return err
		}

		children, err := rn.fetchRelated(ctx, node.target, related, node.exp)
		if err != nil {
			return err
		}
		byID, err := indexByID(children, node.target)
		if err != nil {
			return err
		}

		key := outputKey(node.exp)
		for id, records := range byParent {
			value := shape(related[id], byID, node.many)
			for _, rec := range records {
				rec[key] = value
			}
		}

		rn.res.log.Debug("expansion resolved",
			"link", node.exp.Link, "root_entity", entity, "target_entity", node.target,
			"reverse", node.reverse, "root_records", len(parents), "fetched_records", len(children),
			"key", key)

		if len(node.children) > 0 && len(children) > 0 {
			if err := rn.expand(ctx, node.target, children, node.children); err != nil {
				return err
			}
		}
	}
	return nil
}

// fetchRelated fetches all the related ids from the target provider in ONE
// call. With no related id the provider is not called at all.
//
// The fetched records are copied with ownRecords before entering the result
// tree; the rationale is written there.
func (rn *run) fetchRelated(ctx context.Context, target string, related map[string][]string, exp Expansion) ([]Record, error) {
	ids := uniqueValues(related)
	if len(ids) == 0 {
		return nil, nil
	}

	provider, err := rn.provider(target)
	if err != nil {
		return nil, err
	}

	records, err := provider.FetchByIDs(ctx, ids, fieldsWithID(exp.Fields, true))
	if err != nil {
		return nil, wrapCallErr(err, codeProviderFailed,
			"the FetchByIDs call of the %q provider failed (link: %q, %d ids)",
			target, exp.Link, len(ids))
	}
	return ownRecords(records), nil
}

// provider resolves an entity's provider from the container by name and caches
// it.
//
// When it is not registered, the error returned NAMES THE NAME LOOKED UP; that
// is ADR 0004's diagnosability requirement.
func (rn *run) provider(entity string) (Provider, error) {
	if p, ok := rn.providers[entity]; ok {
		return p, nil
	}

	name := entity + ProviderSuffix
	p, err := container.Resolve[Provider](rn.res.c, name)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, errors.Wrap(err, errors.KindNotFound, codeProviderNotFound,
				"no query provider was found for the %q entity; the name %q was looked up in the container (does the module register it during Register?)",
				entity, name).
				WithDetails(map[string]any{detailEntity: entity, detailName: name})
		}
		return nil, errors.Wrap(err, errors.KindOf(err), codeProviderInvalid,
			"the query provider of the %q entity could not be resolved (name looked up: %q)", entity, name).
			WithDetails(map[string]any{detailEntity: entity, detailName: name})
	}

	// Had the provider been registered under the wrong name, the join would
	// silently pull data from the wrong module; the registration name and the
	// entity offered must therefore agree.
	if got := p.Entity(); got != entity {
		return nil, errors.Invalid(codeProviderMismatch,
			"the provider registered as %q offers the %q entity, %q was expected", name, got, entity).
			WithDetails(map[string]any{detailName: name, "expected": entity, "got": got})
	}

	rn.providers[entity] = p
	return p, nil
}

// definition resolves the link definition and caches it for the call.
//
// For an undefined link name [link.LinkService.Definition] returns
// errors.NotFound, and that class is preserved on the way up.
func (rn *run) definition(ctx context.Context, name string) (link.LinkDefinition, error) {
	if def, ok := rn.defs[name]; ok {
		return def, nil
	}
	if rn.res.links == nil {
		return link.LinkDefinition{}, errors.Internal(codeNoLinkService,
			"query was built without a link service; the %q link cannot be resolved", name)
	}

	def, err := rn.res.links.Definition(ctx, name)
	if err != nil {
		return link.LinkDefinition{}, wrapCallErr(err, codeLinkDefFailed,
			"the %q link definition could not be read", name).
			WithDetails(map[string]any{detailLink: name})
	}

	rn.defs[name] = def
	return def, nil
}

// resolveLinks resolves the links for EVERY id at this level and returns the
// map from a root id to its related ids.
//
// Both directions are resolved with the contract's BATCH methods: forward with
// [link.LinkService.ListMany], reverse with [link.LinkService.ListManyByTo].
// One call, no N+1.
func (rn *run) resolveLinks(ctx context.Context, def link.LinkDefinition, ids []string, reverse bool) (map[string][]string, error) {
	links := rn.res.links
	if links == nil {
		return nil, errors.Internal(codeNoLinkService,
			"query was built without a link service; the %q link cannot be resolved", def.Name)
	}

	if reverse {
		return rn.resolveReverse(ctx, links, def, ids)
	}

	related, err := links.ListMany(ctx, def.Name, ids)
	if err != nil {
		return nil, wrapLinkErr(err, def.Name, len(ids))
	}
	return related, nil
}

// resolveReverse resolves the link in the reverse direction (To -> From) IN
// BATCH.
//
// [link.LinkService.ListManyByTo] is part of the contract; it is symmetric with
// the forward ListMany and produces no query per record.
func (rn *run) resolveReverse(ctx context.Context, links link.LinkService, def link.LinkDefinition, ids []string) (map[string][]string, error) {
	related, err := links.ListManyByTo(ctx, def.Name, ids)
	if err != nil {
		return nil, wrapLinkErr(err, def.Name, len(ids))
	}
	return related, nil
}

// wrapLinkErr wraps an error from the link service, preserving its class.
func wrapLinkErr(err error, name string, count int) error {
	return wrapCallErr(err, codeLinkFailed,
		"the %q link could not be resolved (%d ids)", name, count).
		WithDetails(map[string]any{detailLink: name})
}

// wrapCallErr wraps an error returned by a provider or the link service into a
// typed error and classifies CANCELLATION separately.
//
// The class of an already-typed error is preserved as it is. A raw
// context.Canceled or context.DeadlineExceeded — which is exactly what pgx
// returns directly — is not typed; errors.KindOf turns it into the safe default
// KindInternal, and the HTTP layer would then answer a request whose budget ran
// out with an opaque (message-suppressed) 500 instead of a 503. Cancellation is
// therefore mapped to KindUnavailable + codeCanceled, the SAME way ctxErr and
// link.wrapDB do it. With err nil it returns nil.
func wrapCallErr(err error, code, format string, a ...any) *errors.Error {
	var typed *errors.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &typed):
		return errors.Wrap(err, typed.Kind, code, format, a...)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.Wrap(err, errors.KindUnavailable, codeCanceled,
			format+" (the context was canceled)", a...)
	default:
		return errors.Wrap(err, errors.KindInternal, code, format, a...)
	}
}
