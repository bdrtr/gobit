package searchpg

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// This file is the plugin's ONLY face toward the CATALOG (ADR 0001, ADR 0006).
//
// The plugin cannot import any module and therefore cannot name product's
// types. The access is made of three parts and all three stand here:
//
//  1. The NARROW interface it needs is defined in this package
//     ([StoreProductReader]).
//  2. The concrete surface is resolved from the container BY NAME
//     ("product.interop").
//  3. What travels is JSON, and the schema is written out EXPLICITLY below.
//
// The product's storefront representation is NOT redefined here. The search
// endpoint passes the records through as raw JSON (see [catalog.products]) and
// only the fields to be indexed are parsed ([productView]). Keeping a second
// copy of the representation would mean a field added to product disappearing
// silently from search.

// The error codes.
const (
	codeCatalogMissing  = "searchpg_catalog_unavailable"
	codeCatalogRead     = "searchpg_catalog_read_failed"
	codeCatalogResponse = "searchpg_catalog_response_invalid"
)

// StoreProductReader is the NARROW surface the plugin asks of the catalog.
//
// It is defined on the CONSUMER side and product's "product.interop"
// registration satisfies it STRUCTURALLY; there is no compile-time bond between
// the two sides and there cannot be (Principle 2.4). That the signature speaks
// in primitives and stdlib types is therefore mandatory: had a type of
// product's been named, that type would be a DIFFERENT type defined here and
// the concrete surface would not satisfy this interface.
//
// The request and response schemas are documented on [catalogRequest] and
// [catalogResponse].
type StoreProductReader interface {
	StoreProductsByIDsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
}

// catalogRequest is the JSON schema of the "product.interop" request.
//
//	{"ids": ["prod_..."], "sales_channel_ids": ["sc_..."]}
//
// # A nil sales_channel_ids MEANS something
//
// The field is defined by the catalog and is NOT reinterpreted here: null (a nil
// slice) means "the request carries no channel id" and no filter is applied,
// while an empty array means "there is an identity and it has no channel" and
// the filter IS applied. There is a difference between the two states that
// omitempty would erase, which is why the field is ALWAYS written.
//
// Two callers pass two different values and both are right:
//
//   - The search endpoint passes the channels that come from the request's
//     IDENTITY (see [channels]).
//   - The event handler passes nil: the index is INDEPENDENT of the channel,
//     because the filtering happens at read time. Keeping a separate index per
//     channel would mean writing the same product once per channel and
//     rebuilding the index whenever a channel assignment changed.
type catalogRequest struct {
	IDs             []string `json:"ids"`
	SalesChannelIDs []string `json:"sales_channel_ids"`
}

// catalogResponse is the JSON schema of the "product.interop" response.
//
//	{"products": [ <storefront product record>, ... ]}
//
// The records are left RAW: the search endpoint writes them out as they are,
// while the indexing parses only the fields it needs. Defining the record's full
// shape in this package would produce a second copy of the storefront
// representation.
type catalogResponse struct {
	Products []json.RawMessage `json:"products"`
}

// catalog is LAZY access to the "product.interop" surface.
//
// The laziness is mandatory: the plugin's Setup runs BEFORE the modules and at
// that moment there is no such registration in the container. The resolution is
// deferred to first use — that is, to the first search request or the first
// catalog event.
type catalog struct {
	// c is the container the registration is looked up in; it may be nil
	// (embedded use, tests).
	c *container.Container

	// mu makes sure the reader is resolved once.
	//
	// sync.Once was DELIBERATELY not used: Once also makes the first call's
	// RESULT permanent, and a single resolution that failed while product was not
	// yet registered would leave search dead for the life of the process. The
	// lock keeps only a SUCCESSFUL result; a failure is retried on the next
	// request.
	mu     sync.Mutex
	reader StoreProductReader
}

// newCatalog builds lazy catalog access over the given container.
func newCatalog(c *container.Container) *catalog { return &catalog{c: c} }

// resolve resolves the catalog surface from the container and keeps the result.
func (k *catalog) resolve() (StoreProductReader, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.reader != nil {
		return k.reader, nil
	}
	if k.c == nil {
		return nil, coreerrors.Unavailable(codeCatalogMissing,
			"there is no container; the %q surface cannot be resolved", catalogInteropName)
	}

	reader, err := container.Resolve[StoreProductReader](k.c, catalogInteropName)
	if err != nil {
		// The CLASS is preserved: a missing registration gives NotFound and a type
		// mismatch gives Internal, and the two are different faults — one is
		// "product is not installed", the other "the surface's signature has
		// changed".
		return nil, coreerrors.Wrap(err, coreerrors.KindOf(err), codeCatalogMissing,
			"the catalog read surface %q could not be resolved; is the product module installed?", catalogInteropName)
	}

	k.reader = reader

	return reader, nil
}

// products returns the STOREFRONT records of the given ids as raw JSON.
//
// The order of the records is the request's id order (the relevance order); an
// id that is not found, is not published, or does not appear in the request's
// channels is skipped SILENTLY. The rule belongs to the catalog and is not
// repeated here.
//
// For an empty id list the catalog is NOT CALLED AT ALL: the result is already
// empty, and taking an empty turn would be a needless round trip every time
// search finds nothing.
func (k *catalog) products(ctx context.Context, ids, channels []string) ([]json.RawMessage, error) {
	if len(ids) == 0 {
		return []json.RawMessage{}, nil
	}

	reader, err := k.resolve()
	if err != nil {
		return nil, err
	}

	request, err := json.Marshal(catalogRequest{IDs: ids, SalesChannelIDs: channels})
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeCatalogRead,
			"the catalog request could not be encoded (%d ids)", len(ids))
	}

	raw, err := reader.StoreProductsByIDsJSON(ctx, request)
	if err != nil {
		// The class is preserved: the catalog returns Invalid for a request that
		// exceeds its limit, and turning that into Internal would present a fault
		// the caller can fix as a server failure.
		return nil, coreerrors.Wrap(err, coreerrors.KindOf(err), codeCatalogRead,
			"the catalog records could not be read (%d ids)", len(ids))
	}

	var response catalogResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeCatalogResponse,
			"the catalog response could not be decoded; the schema of the %q surface may have changed", catalogInteropName)
	}
	if response.Products == nil {
		return []json.RawMessage{}, nil
	}

	return response.Products, nil
}
