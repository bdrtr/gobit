// Package query is the read layer that reduces a cross-module read to a single
// call (plan Section 5.3, Phase 2).
//
// The flow is always the same: the root entity's records are fetched, the
// related ids are found through the links, they are fetched from the related
// modules IN BATCH, and the result is joined into one record tree.
//
// # Fetching data without knowing the modules
//
// Under Principle 2.4 the core may not import the modules. Query therefore does
// not know at compile time which module it is asking: every module registers
// its own [Provider] implementation in the container under
// "<module name>.query" and Query resolves it BY NAME (see ADR 0004). The
// consuming side (this package) declares the interface, the providing module
// only satisfies the signature and imports nothing (see ADR 0001).
//
// When a provider registration was forgotten, [Query.Graph] returns
// errors.KindNotFound and NAMES THE NAME it looked up; diagnosability is
// critical at this layer.
//
// # The no-N+1 rule
//
// A provider is called ONLY ONCE per expansion. The ids of every record at that
// level are collected first, the links are resolved with one
// [link.LinkService.ListMany] call, and one [Provider.FetchByIDs] goes to the
// target module. The same holds at nested levels: the cost grows with the number
// of EXPANSIONS, not with the number of records. A hundred root records and one
// root record produce the same number of calls per expansion.
//
// # The join key
//
// Query joins records on the [IDField] ("id") field: that field of the root
// record is the id that goes into the link table, and the same field of a
// fetched record is used to map back. Providers must offer their primary key
// under this name.
//
// Being the key, it is protected: an expansion's output key CANNOT be [IDField]
// (errors.KindInvalid), because the result would overwrite the record's
// identity. A record whose id cannot be read — the field absent, not a string,
// or empty — is not skipped in silence either; errors.KindInternal is returned
// and the message says WHY it could not be read (the type that arrived).
//
// [link.LinkSide.Field] is DELIBERATELY not used. That field ("product_id" and
// the like) is metadata declaring the id's meaning in the module that owns it;
// a provider record need not carry such a field, and asking a provider for it
// in a request that selects fields would return errors.KindInvalid for a field
// it does not know. The join is therefore bound to a single, predictable key.
//
// # Direction
//
// An expansion does NOT ASSUME the root entity sits on the link's From end. The
// direction is resolved by looking at the link definition's ends: with the root
// entity on the From end it goes forward (From -> To), on the To end it goes
// backward (To -> From). When neither end is the root entity it returns
// errors.KindInvalid.
//
// Both directions are resolved with the BATCH methods of the
// [link.LinkService] contract: ListMany forward, ListManyByTo backward. There
// is no link query per record.
//
// # The shape of the result
//
// How an expansion is written into the result is decided together by the link's
// cardinality and the DIRECTION traveled: an end that writes a single record
// gets a [Record] (nil when nothing matches), an end that writes many gets a
// []Record (an empty slice when nothing matches). [link.OneToMany] is a
// DIRECTIONAL cardinality — it means "one From, many To" — so it writes a slice
// forward and a single record backward.
//
// # Record ownership
//
// The records coming from a provider are COPIED; every [Record] in the tree
// [Query.Graph] returns belongs to the call. Because Query writes the expansion
// result into the record, a provider that does not copy would otherwise have
// its own state corrupted (a foreign key leaking in, a stale field, a data race
// between concurrent calls). It works whether or not the provider shares its
// records; the copy is shallow, and Query never touches the field VALUES.
//
// # Limits
//
// A spec may be at most [maxExpandDepth] levels deep and carry at most
// [maxExpansions] expansions in total; both are enforced with
// errors.KindInvalid and without going to a provider at all. The expansion
// tree's link names, direction, cardinality and target provider registrations
// are also resolved BEFORE ANY DATA IS FETCHED: a broken query definition gives
// the same error even when the level above fetched no record.
//
// # The error policy
//
// There are NO partial results. When any provider or link call at any level
// fails, [Query.Graph] returns an error; it does not return a record tree
// filled with missing data. Finding no root record is NOT an error; an empty
// (non-nil) slice is returned.
//
// The underlying error's class is preserved. An untyped cancellation
// (context.Canceled / context.DeadlineExceeded) is mapped to
// errors.KindUnavailable, so it is not confused with a server error and
// produces a 503 rather than a 500 at the API boundary.
package query

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/link"
)

// IDField is the name of the field holding the identity in a provider's
// records.
//
// Query matches records against links through this field. In a request that
// selects fields (Fields non-empty) and also has expansions, Query adds this
// field to the list sent to the provider ITSELF; so an expanded record carries
// it even when the caller did not ask for it.
const IDField = "id"

// ProviderSuffix is the name suffix modules register their providers under in
// the container. An entity's provider is looked up as
// "<entity><ProviderSuffix>" (ADR 0004).
const ProviderSuffix = ".query"

// maxExpandDepth is the upper bound on nested expansion. The limit stops a
// query definition coming from outside from forcing the core into recursion of
// arbitrary depth; exceeding it returns errors.KindInvalid.
const maxExpandDepth = 10

// maxExpansions is the upper bound on the TOTAL number of expansions in a spec.
//
// The cost grows with the NUMBER of expansions, not with the depth: each
// expansion produces a fixed two round trips (one link resolution plus one
// FetchByIDs). Limiting the depth alone is half a protection; a single request
// staying under the depth limit while carrying dozens of sibling expansions at
// every level could open hundreds of round trips. When the total in the tree
// exceeds this limit, errors.KindInvalid is returned.
const maxExpansions = 50

// Record is a record's field name -> value mapping.
//
// The loose typing is the unavoidable price of the core not knowing the
// modules' models (ADR 0004); type safety is regained at the API boundary.
type Record map[string]any

// ListOptions are the options given to a provider for fetching the root
// records.
type ListOptions struct {
	// Fields are the fields to return. Left empty, the provider's default field
	// set is returned.
	Fields []string
	// Filters are field name -> expected value filters. Their interpretation
	// belongs to the provider; for a filter it does not support the provider
	// must return errors.KindInvalid.
	Filters map[string]any
	// Limit is the maximum number of records to return; 0 means unlimited.
	Limit int
	// Offset is the number of records to skip.
	Offset int
}

// Provider is the read surface a module opens to the Query layer.
//
// The module puts it into the container during Register under
// "<module name>.query" (ADR 0004). The interface is declared in this package;
// the providing module need not import this package, it only satisfies the
// signature.
//
// Query COPIES the records returned and writes the expansion result into the
// copy, so a provider may share the maps it returns with its own state (see
// "Record ownership" in the package comment).
type Provider interface {
	// Entity is the entity name the provider offers (e.g. "product"). It must
	// match the prefix of the registration name; Query verifies it.
	Entity() string

	// List returns the root records. Query calls it ONLY for the root entity.
	List(ctx context.Context, opts ListOptions) ([]Record, error)

	// FetchByIDs returns the records for the given ids.
	// For an id it cannot find it returns NO record; that is not an error.
	// Query calls it IN BATCH with the id set coming out of the links.
	FetchByIDs(ctx context.Context, ids []string, fields []string) ([]Record, error)
}

// Expansion is a single expansion made through one link.
type Expansion struct {
	// Link is the name of the link definition to use (e.g. "product_price").
	Link string
	// As is the key the result is written under on the root record; empty means
	// Link is used.
	As string
	// Fields are the fields wanted from the expanded records; empty gives the
	// provider's default.
	Fields []string
	// Expand holds the nested expansions applied on top of this one.
	// Each level is again resolved IN BATCH within itself.
	Expand []Expansion
}

// GraphSpec is the definition of a single cross-module read.
type GraphSpec struct {
	// Entity is the root entity name; its provider is looked up as
	// "<Entity>.query".
	Entity string
	// Fields are the fields wanted from the root records; empty gives the
	// provider's default.
	Fields []string
	// Filters are the filters applied to the root records.
	Filters map[string]any
	// Limit is the upper bound on the number of root records; 0 means
	// unlimited.
	Limit int
	// Offset is the number of root records to skip.
	Offset int
	// Expand holds the expansions applied on top of the root records.
	Expand []Expansion
}

// Query is the read layer that takes data from the modules and joins it
// through the links.
type Query interface {
	// Graph fetches the root records according to the spec and applies the
	// expansions. With no root record it returns an empty (non-nil) slice and a
	// nil error.
	Graph(ctx context.Context, spec GraphSpec) ([]Record, error)
}

// resolver is [Query]'s only implementation.
type resolver struct {
	links link.LinkService
	c     *container.Container
	log   *slog.Logger
}

var _ Query = (*resolver)(nil)

// New produces a [Query] running on the given link service and container.
//
// links resolves the link definitions and the links themselves; c holds the
// providers under "<entity>.query". With log nil the logs are discarded.
//
// A nil links or c is reported as a typed error on the first [Query.Graph]
// call rather than at construction; the setup path raises no panic.
func New(links link.LinkService, c *container.Container, log *slog.Logger) Query {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &resolver{links: links, c: c, log: log}
}
