// Package service holds the business rules of the product module.
//
// The contract of the layer: inputs are validated, ids are produced here, data
// access goes through [repository.Store] and what is returned to the outside is
// ALWAYS a core/errors typed error. Choosing the HTTP status code is not the API
// layer's job but that of the error class this layer returns.
//
// # Other modules' data
//
// Price (pricing) and stock (inventory) are NOT in this module and those modules
// are NOT IMPORTED (Principle 2.4, ADR 0001). The surfaces that are needed are
// defined in this package as narrow interfaces ([Linker], [Grapher]) and their
// concrete implementations are resolved from the container BY NAME. The store
// listing gathers price and stock over the links with the Query layer (ADR
// 0004).
package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// Linker is the NARROW surface product needs from the core's link service.
//
// Instead of the full LinkService only the three methods that are used are asked
// for: as the contract grows, the tests and the fakes of this module are not
// affected.
type Linker interface {
	// Create links fromID with toID; for the same pair it is a no-op.
	Create(ctx context.Context, name, fromID, toID string) error
	// Delete removes the link; if there is no link it is a no-op.
	Delete(ctx context.Context, name, fromID, toID string) error
	// List returns the toIDs linked to fromID.
	List(ctx context.Context, name, fromID string) ([]string, error)
	// ListManyByTo resolves the REVERSE direction in one query: for each toID
	// it returns the fromIDs bound to it.
	//
	// The storefront badge asks it exactly one question — which warehouses does
	// this sales channel ship from — and the binding is declared with the
	// warehouse on the From end by the module that owns warehouses.
	ListManyByTo(ctx context.Context, name string, toIDs []string) (map[string][]string, error)
}

// Grapher is the cross-module read surface (the core's Query layer).
//
// The store listing gathers the price and stock records of the variants with it;
// the pricing and inventory modules are neither imported nor known by name — the
// only thing that is known is the link names.
type Grapher interface {
	// Graph pulls the root records according to the spec and applies the expansions.
	Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// UploadReader is the NARROW surface product needs from the file module.
//
// The file module CANNOT be imported (Principle 2.4, and depguard enforces it),
// so the surface is declared here and satisfied STRUCTURALLY by whatever the
// container holds under the file module's interop name — the pattern of ADR
// 0001/0006, the same one the order module uses for the b2b spending rule.
//
// The record travels as JSON because its SHAPE belongs to the file module: it
// declares the fields, and naming a type here would either duplicate that shape
// or force an import. This module does not read the body at all; it only needs
// to know whether the record is there (see [Service.verifyImageUploads]). The
// callers that want the file behind an image decode it themselves.
type UploadReader interface {
	// UploadJSON returns the upload record as JSON, errors.NotFound if the id
	// belongs to no upload.
	//
	// A NIL body WITH A NIL ERROR is the third answer and it means "I cannot
	// answer": the file module is not installed in this setup. It is separate
	// from NotFound on purpose — "there is no such upload" is a fact about the
	// id, "there is no file module" is a fact about the installation, and
	// treating the second as the first would reject ids that are perfectly
	// good.
	UploadJSON(ctx context.Context, uploadID string) (json.RawMessage, error)
}

// EventPublisher is the NARROW surface the service needs from the event bus.
//
// core/eventbus is CORE and importing it is free (Principle 2.4); the narrowing
// here is there to reduce the dependency: the catalog only PUBLISHES, it does
// not subscribe and it does not close the bus. Binding to the whole of
// [eventbus.EventBus] would give the impression that the module has the
// authority to subscribe and to shut down.
//
// The [eventbus.Event] type is used as it is: the shape of the event is the
// core's contract and redefining it here would lead the two types to drift
// apart.
type EventPublisher interface {
	// Publish publishes the event and does NOT WAIT for the handlers.
	Publish(ctx context.Context, e eventbus.Event) error
}

// Options holds the dependencies of the service setup.
type Options struct {
	// Repo is required.
	Repo repository.Store
	// Links is needed for the endpoints that use the link definitions; if nil is
	// given, the link endpoints return a typed "not ready" error.
	Links Linker
	// Query is there for the price/stock expansion of the store listing; if nil
	// is given, the listing works without prices and without stock.
	Query Grapher
	// Uploads is the file module's read-back; if nil is given, an image's
	// upload id is recorded WITHOUT being verified (see
	// [Service.verifyImageUploads]).
	Uploads UploadReader
	// Events is the bus the catalog events are published on; if nil is given, the
	// events are silently skipped (rationale: [Service.publishProductEvent]).
	Events EventPublisher
	// Logger, if nil is given, means the logs are discarded.
	Logger *slog.Logger
}

// Service is the public service of the product module.
//
// It is registered in the container under the name "product.service". All of its
// methods are goroutine-safe (they hold no state; the state is in the database).
type Service struct {
	repo    repository.Store
	links   Linker
	graph   Grapher
	uploads UploadReader
	events  EventPublisher
	log     *slog.Logger
}

// New builds the service with the given dependencies.
//
// If no repo is given it returns an error: a catalog service without a
// repository would blow up on every call, and that has to be seen at setup time.
func New(opts Options) (*Service, error) {
	if opts.Repo == nil {
		return nil, errors.Invalid(codeNotReady,
			"the product service cannot be built without a repository")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		repo:    opts.Repo,
		links:   opts.Links,
		graph:   opts.Query,
		uploads: opts.Uploads,
		events:  opts.Events,
		log:     log,
	}, nil
}

// ListResult is the result of a paginated list.
type ListResult[T any] struct {
	Items []T
	// Count is the total number of records, INDEPENDENT of limit/offset; it is the
	// source of the "count" field in the API envelope.
	//
	// It is A POINTER and nil means "NOT COUNTED" — it does NOT mean "zero
	// records". The count is optional (see [ListProductsOptions.SkipCount]) and
	// when it is skipped a plain int field would carry 0; 0 is a lie here, because
	// it cannot be told apart from the sentence "no matching records". Moving the
	// distinction into the type FORCES the caller to ask the question it has to ask
	// before reading the number: using a nil like a plain number does not compile.
	Count  *int
	Offset int
	Limit  int
	// NextCursor is the opaque position the NEXT page starts below; empty means
	// this page is the last one.
	//
	// It is empty rather than "the key of the last row" when there is nothing
	// more, and the difference is the point: a cursor that always came back
	// would make a client walk one extra request into an empty page before it
	// could tell it was done.
	NextCursor string
}
