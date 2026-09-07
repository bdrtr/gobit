package searchpg

import (
	"context"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// The error codes.
const (
	codeReindexFailed = "searchpg_reindex_failed"
	codeCatalogIDs    = "searchpg_catalog_ids_invalid"
)

// reindexPageSize is how many products one round of the reindex handles.
//
// Paging is MANDATORY: pulling the whole catalog into memory at once would
// produce a request that grows with the catalog and kills the process one day.
//
// The value does NOT exceed the catalog's bulk-read limit (product
// service.MaxLimit); if it did, a page's ids would not fit into a single
// "product.interop" call and the round would fail on its first page.
const reindexPageSize = 100

// reindexResult is the summary of one reindex round.
type reindexResult struct {
	// Indexed is how many documents this round wrote (added or updated).
	Indexed int `json:"indexed"`
	// Removed is how many stale rows the sweep deleted.
	Removed int64 `json:"removed"`
	// Pages is how many catalog pages were read; a cost indicator for operations.
	Pages int `json:"pages"`
}

// reindex indexes the whole published catalog from scratch.
//
// An empty index is of no use at all, and the events carry only the changes
// AFTER STARTUP: when the plugin is fitted to an existing installation, when the
// bus is unreachable for a while, or when an event is missed, this is the only
// way to bring the index back to the truth (see events.go, the error policy).
//
// # Where the ids come from
//
// The product ids are read PAGE BY PAGE from the core's Query layer; no SQL is
// sent to the catalog table directly. Two reasons: a module's table is its own
// internal business and a plugin binding to it would break silently when the
// schema changed; and Query is the core's defined way of reading without
// importing a module (ADR 0004).
//
// The filter is "status = published": there is no point indexing a product the
// storefront does not show, and every page not read is a catalog round not paid
// for.
//
// # Why WRITE first and SWEEP after
//
// The round begins with a threshold taken from the database clock; every write
// refreshes the stamp; when the round FINISHES the rows older than the threshold
// are deleted. Products that are no longer published, or were deleted, thereby
// fall out of the index — without any event arriving.
//
// Had the order been reversed and the deletion come first (TRUNCATE + fill),
// search would answer EMPTY for the whole round. The sweep runs only after a
// COMPLETELY finished round: sweeping after a round that stopped halfway would
// delete the valid rows on the pages that were never read.
//
// # The accepted limits
//
//   - The paging is OFFSET based. If a product is deleted during the round, the
//     next page shifts by one record, that record is not read in this round, and
//     the sweep may drop it from the index. The next round, or the product's next
//     write, repairs it.
//   - The round is bound to the caller's context: if the client goes away the
//     work stops halfway and the sweep does NOT run, so the index is not
//     corrupted — it is only left un-updated.
func (m *searchModule) reindex(ctx context.Context) (reindexResult, error) {
	if err := m.ready(); err != nil {
		return reindexResult{}, err
	}
	if m.graph == nil {
		return reindexResult{}, coreerrors.Unavailable(codeNotRegistered,
			"the %s module did not resolve the query layer; a reindex cannot be run", ModuleName)
	}

	threshold, err := m.index.Now(ctx)
	if err != nil {
		return reindexResult{}, err
	}

	var result reindexResult
	for offset := 0; ; offset += reindexPageSize {
		ids, err := m.productIDs(ctx, offset)
		if err != nil {
			return reindexResult{}, err
		}
		if len(ids) == 0 {
			break
		}
		result.Pages++

		documents, err := m.documents(ctx, ids)
		if err != nil {
			return reindexResult{}, err
		}
		if err := m.index.Upsert(ctx, documents); err != nil {
			return reindexResult{}, err
		}
		result.Indexed += len(documents)

		// A short page is the last page; there is no need for another round that
		// reads an empty one.
		if len(ids) < reindexPageSize {
			break
		}
	}

	removed, err := m.index.Sweep(ctx, threshold)
	if err != nil {
		return reindexResult{}, err
	}
	result.Removed = removed

	m.log.InfoContext(ctx, "the search index was rebuilt",
		"indexed", result.Indexed,
		"removed", result.Removed,
		"pages", result.Pages)

	return result, nil
}

// productIDs reads one page of the published products' ids.
//
// ONLY the "id" field is asked of Query: the rest of a record is not used here,
// because the text to be indexed comes from the storefront representation (see
// [searchModule.documents]). Asking for every field would be a needless transport cost
// per catalog page.
func (m *searchModule) productIDs(ctx context.Context, offset int) ([]string, error) {
	records, err := m.graph.Graph(ctx, query.GraphSpec{
		Entity:  catalogEntity,
		Fields:  []string{query.IDField},
		Filters: map[string]any{catalogStatusFilter: catalogStatusPublished},
		Limit:   reindexPageSize,
		Offset:  offset,
	})
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindOf(err), codeReindexFailed,
			"the catalog ids could not be read (offset %d)", offset)
	}

	ids := make([]string, 0, len(records))
	for _, record := range records {
		raw, ok := record[query.IDField]
		if !ok {
			return nil, coreerrors.Internal(codeCatalogIDs,
				"the catalog record has no %q field (offset %d)", query.IDField, offset)
		}
		id, ok := raw.(string)
		if !ok || id == "" {
			return nil, coreerrors.Internal(codeCatalogIDs,
				"the %q field of the catalog record is empty or not a string (%T arrived)",
				query.IDField, raw)
		}
		ids = append(ids, id)
	}

	return ids, nil
}
