package searchpg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The error codes.
const (
	codeIndexFailed = "searchpg_index_query_failed"
	codeCanceled    = "searchpg_canceled"
)

// The text search configuration ('simple') is written LITERALLY into the query
// texts below rather than passed as a parameter. The reason is technical: when
// the regconfig argument is a constant, to_tsvector and websearch_to_tsquery are
// IMMUTABLE, so the planner can fold the expression to a constant and use the
// GIN index. The reasoning behind the dictionary choice is in the package
// documentation.

// document is the text of one product, in the pieces that get indexed.
//
// The pieces stand APART because their weights differ (see [upsertSQL]); one
// merged text would make a word appearing in the title indistinguishable from a
// word appearing in the description, and relevance ranking would lose its
// meaning.
type document struct {
	// productID is the index's primary key.
	productID string
	// title carries weight A: the product's title.
	title string
	// keywords carries weight B: the handle, the subtitle, the tags, the variant
	// titles and the SKUs. The short, distinguishing fields live here.
	keywords string
	// body carries weight C: the description. Long text takes the lowest weight;
	// otherwise a product with a long description would come first in every
	// search.
	body string
}

// store is the surface the index table opens to the search flow.
//
// The interface is defined on the CONSUMER side: the HTTP endpoints and the
// event handlers know only the five calls listed here and can be tested without
// a real PostgreSQL. The concrete implementation is [index].
type store interface {
	// Upsert writes or updates the documents and refreshes their stamps.
	Upsert(ctx context.Context, documents []document) error
	// Delete removes the given ids from the index and returns how many rows went.
	Delete(ctx context.Context, productIDs ...string) (int64, error)
	// Search returns the ids matching the query IN RELEVANCE ORDER.
	Search(ctx context.Context, query string, limit, offset int) ([]string, error)
	// Sweep deletes the rows stamped BEFORE the given moment.
	Sweep(ctx context.Context, threshold time.Time) (int64, error)
	// Now returns the database's current time.
	Now(ctx context.Context) (time.Time, error)
}

// upsertSQL writes the documents in a SINGLE statement.
//
// The arrays are expanded into rows with unnest: a page of a hundred documents
// and a single document both take ONE round trip. A separate INSERT per document
// would open as many round trips as the catalog is large during a reindex.
//
// The tsvector is produced IN THE DATABASE and not in Go: how a text turns into
// a document is written in one place, and that it uses the same dictionary as
// the query side (websearch_to_tsquery) is visible from here.
//
// The stamp comes from the database's clock too (now()): with several instances
// writing, the drift between their application clocks could push the sweep
// threshold (see [sweepSQL]) to the wrong side.
const upsertSQL = `
INSERT INTO searchpg_product (product_id, document, indexed_at)
SELECT d.product_id,
       setweight(to_tsvector('simple', d.title), 'A') ||
       setweight(to_tsvector('simple', d.keywords), 'B') ||
       setweight(to_tsvector('simple', d.body), 'C'),
       now()
FROM unnest($1::text[], $2::text[], $3::text[], $4::text[])
     AS d(product_id, title, keywords, body)
ON CONFLICT (product_id) DO UPDATE
SET document   = EXCLUDED.document,
    indexed_at = EXCLUDED.indexed_at`

// deleteSQL removes the given ids from the index.
//
// An id that is not there is NOT an error: a deletion event can arrive for a
// product that was never indexed (one deleted while still a draft), and the
// handler has to be idempotent — the bus may redeliver the same event.
const deleteSQL = `DELETE FROM searchpg_product WHERE product_id = ANY($1::text[])`

// searchSQL returns the ids matching the query in relevance order.
//
// websearch_to_tsquery was chosen over to_tsquery DELIBERATELY: it never turns a
// user's text into a syntax error. An `a & &` reaching to_tsquery brings the
// query down and would produce 500s depending on what somebody typed into the
// search box; websearch also brings quoting ("an exact phrase"), OR and -
// (exclude) along with it.
//
// # The ranking uses ts_rank, NOT ts_rank_cd
//
// A GIN index cannot satisfy the ORDER BY, so the ranking expression runs for
// EVERY MATCHING DOCUMENT: to return one page it is computed on all 52 thousand
// of 52 thousand matches. The expression's per-row cost is therefore the
// endpoint's cost. Measured (postgres:16.14-alpine, an index of 52k documents,
// ~92 lexemes per document, everything cached, parallel workers off, LIMIT 20):
//
//	matches    ts_rank_cd   ts_rank   the match alone
//	     10       0.23 ms   0.10 ms           0.06 ms
//	    110       1.60 ms   0.25 ms           0.18 ms
//	  1,002      13.70 ms   1.40 ms           1.10 ms
//	 10,400     148.00 ms  23.00 ms          21.70 ms
//	 52,000     663.00 ms  30.60 ms          23.80 ms
//
// The table is for the LITERAL query. Because pgx uses prepared statements, the
// plan can switch to the GENERIC one after the sixth execution; there $1 is
// unknown, so the call in the WHERE is not folded to a constant, moves into the
// Recheck Cond and runs again per row. The GIN index is used there too. The
// RANKING expression is unaffected because it is a scalar subquery (below): on
// 52 thousand matches the generic plan is 25.4 ms and the literal one 24.3 ms.
//
// The difference between them is ts_rank_cd's ~12 µs per document; ts_rank
// spends ~0.07 µs on the same rows (a single-term query). The planner CANNOT
// tell the two apart; pg_proc.procost is 1 for both, so "this ranking is
// expensive" never enters the plan at all.
//
// Both numbers depend on the QUERY SHAPE, and which depends on what was
// measured. ts_rank_cd's cost depends neither on document size nor on term
// frequency (52 thousand rows over a single-lexeme tsvector column take 623 ms;
// over real 92-lexeme documents, 667 ms). ts_rank's does: its cost grows with
// the product of the POSITION lists of the query's terms in the document.
// Measured over 20 thousand synthetic rows with the query `a & c`:
//
//	occurrences in the document    ts_rank   ts_rank_cd
//	1 per term                     0.21 µs      29.5 µs
//	10 per term                    0.91 µs      30.8 µs
//	at the tsvector position cap    315 µs      93.6 µs
//
// So at the cap — a document carrying one lexeme more than 256 times — the
// relation INVERTS and ts_rank becomes 3.4 times more expensive. The real
// documents this index produces (~92 lexemes) are nowhere near that cap, but
// leaving the number unwritten would make "ts_rank is always cheap" an unmeasured
// promise: a machine-generated description (SEO text repeating one word hundreds
// of times) can approach the cap.
//
// Why it matters on THIS endpoint: it is the storefront's hot path, its identity
// is the publishable key every browser carries, and the default quota is 600
// requests per minute. A single word occurring across the WHOLE catalog (a brand
// name, "cotton") was burning 6.6 cores per second at that quota; on deep paging
// (LIMIT 100 OFFSET 50000) one request took 1909 ms. The same request is 37.6 ms
// with today's expression; the pair measured on the generic plan is 744 ms ->
// 47 ms.
//
// WHAT IS LOST is proximity being able to BEAT WEIGHT — not proximity itself.
// ts_rank is sensitive to proximity too, and that was measured: as the gap
// between two A-weighted words grows from 0 to 6, the score falls 0.9910 →
// 0.9850 → 0.9736 → 0.9524 → 0.9149 → 0.8530 → 0.7615. The difference is that in
// ts_rank proximity is a SATURATING secondary factor, while in ts_rank_cd cover
// density can override the weight model (A title, B keywords, C description; see
// [document]). On the query "blue shirt", a product carrying both words side by side
// in the keyword field (B) came BEFORE a product carrying both IN ITS TITLE (A)
// (cd 0.4 > 0.2; rank 0.396 < 0.915). That ordering is the only reason the index
// is split into weights at all, so weight winning is the EXPECTED behavior.
//
// # EXCLUSIONS are dropped from the ranking query
//
// ts_rank gives EVERY document 0 for a query containing a negation (!); measured:
// on 'alpha' & !'zeta' a document carrying 'alpha' scores 0, and so does one
// carrying 'alpha' and 'beta' together. Had the ranking used the raw query, a
// shopper typing "shirt -blue" would get their results ordered NOT by relevance
// but by product_id, that is, by indexing order — while the first paragraph of
// this godoc counts - support as a REASON for choosing websearch. It would be
// silent: the result set right, its order meaningless.
//
// querytree returns the indexable (positive) part of a tsquery — 'alpha' &
// !'zeta' → 'alpha' — and the ranking uses that part. The filtering stays in the
// WHERE as it was, so exclusion behavior does not change; what changes is only
// what the score looks at.
//
// A LIMIT: for a query made only of exclusions ("-blue"), querytree returns 'T',
// there is no positive signal to rank by, and the order falls back to product_id.
// That is not hidden: an exclusion carries NO relevance, and a deterministic
// order is better than inventing a score that pretends otherwise.
//
// # A known limit: the match scan still grows with the catalog
//
// The remaining cost is the match itself: a word matching the entire catalog
// still reads 52 thousand rows (~24 ms on the literal plan, ~53 ms on the generic
// one) and that number grows linearly with the catalog — in a 500 thousand
// product catalog the same word rises to half a second. Getting below it means
// the index satisfying the ORDER BY as well (RUM), and adding a mandatory
// EXTENSION is ADR 0015's dated decision; it cannot be taken here as a one-line
// speed-up.
//
// The ranking expression is a SCALAR SUBQUERY and that is a SPEED decision. In a
// literal query a repeated expression is folded by the planner into a single
// constant, but because pgx uses prepared statements the plan can switch to the
// GENERIC one after the sixth execution, and there $1 is unknown so NO folding
// happens: the planner reparses the query text per row. The subquery turns it
// into an InitPlan, computed ONCE per query. Measured (52 thousand matches,
// generic plan): 46.7 ms with the repeated expression, 25.4 ms with the subquery
// — the same place as the literal plan's 24.3 ms.
//
// The call in the WHERE was left as it is ON PURPOSE: the GIN index being chosen
// depends on it, and moving it into a subquery risks disabling the index. That
// the index is still used on the generic plan was verified with EXPLAIN (Bitmap
// Index Scan on searchpg_product_document_idx).
//
// The second sort key is product_id: if the order between equally relevant
// records is not deterministic, paging can show one product on two pages or on
// none.
const searchSQL = `
SELECT product_id
FROM searchpg_product
WHERE document @@ websearch_to_tsquery('simple', $1)
ORDER BY ts_rank(document, (SELECT querytree(websearch_to_tsquery('simple', $1))::tsquery)) DESC,
         product_id
LIMIT $2 OFFSET $3`

// sweepSQL deletes the rows stamped before the threshold.
const sweepSQL = `DELETE FROM searchpg_product WHERE indexed_at < $1`

// nowSQL reads the database's current time.
const nowSQL = `SELECT now()`

// index is the PostgreSQL implementation of the search table.
//
// It is safe for concurrent use; its only state is the pool.
type index struct {
	pool *pgxpool.Pool
}

// That the concrete store satisfies the contract is fixed at compile time.
var _ store = (*index)(nil)

// newIndex produces an index store running over the given pool.
func newIndex(pool *pgxpool.Pool) *index { return &index{pool: pool} }

// Upsert writes or updates the documents.
//
// An empty list is not an error and opens no query. If one id is given twice,
// PostgreSQL refuses with "ON CONFLICT DO UPDATE command cannot affect row a
// second time", which is why the caller has to have deduplicated the ids (see
// [searchModule.documents]).
func (i *index) Upsert(ctx context.Context, documents []document) error {
	if len(documents) == 0 {
		return nil
	}

	ids := make([]string, len(documents))
	titles := make([]string, len(documents))
	keywords := make([]string, len(documents))
	bodies := make([]string, len(documents))
	for n, b := range documents {
		ids[n] = b.productID
		titles[n] = b.title
		keywords[n] = b.keywords
		bodies[n] = b.body
	}

	if _, err := i.pool.Exec(ctx, upsertSQL, ids, titles, keywords, bodies); err != nil {
		return wrapDB(err, "%d documents could not be written to the search index", len(documents))
	}

	return nil
}

// Delete removes the given ids from the index.
func (i *index) Delete(ctx context.Context, productIDs ...string) (int64, error) {
	if len(productIDs) == 0 {
		return 0, nil
	}

	tag, err := i.pool.Exec(ctx, deleteSQL, productIDs)
	if err != nil {
		return 0, wrapDB(err, "%d records could not be deleted from the search index", len(productIDs))
	}

	return tag.RowsAffected(), nil
}

// Search returns the product ids matching the query in relevance order.
//
// It returns NOTHING but ids: the record to display is read from the catalog
// itself. Keeping a title or a price in the index would be keeping a second copy
// of the catalog and waiting for the two representations to part company.
func (i *index) Search(ctx context.Context, query string, limit, offset int) ([]string, error) {
	rows, err := i.pool.Query(ctx, searchSQL, query, limit, offset)
	if err != nil {
		return nil, wrapDB(err, "the search query could not be run")
	}

	// CollectRows closes the rows and folds rows.Err() into the result; with no
	// rows it returns an EMPTY slice.
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, wrapDB(err, "the search results could not be read")
	}

	return ids, nil
}

// Sweep deletes the rows stamped before the given moment and returns how many.
//
// It must be called only after a COMPLETE reindex round; the reasoning is
// documented on [searchModule.reindex].
func (i *index) Sweep(ctx context.Context, threshold time.Time) (int64, error) {
	tag, err := i.pool.Exec(ctx, sweepSQL, threshold)
	if err != nil {
		return 0, wrapDB(err, "the stale index rows could not be deleted")
	}

	return tag.RowsAffected(), nil
}

// Now returns the database's current time.
//
// The application clock is NOT used: the sweep threshold has to come from the
// same clock as the write stamps. A drift of a few seconds between the two
// clocks would make rows written DURING a round count as stale against an
// application time taken at the round's start — that is, it would delete fresh
// index rows.
func (i *index) Now(ctx context.Context) (time.Time, error) {
	var now time.Time
	if err := i.pool.QueryRow(ctx, nowSQL).Scan(&now); err != nil {
		return time.Time{}, wrapDB(err, "the database clock could not be read")
	}

	return now, nil
}

// wrapDB turns a driver error into a typed one.
//
// A canceled context falls to KindUnavailable: a request whose client went away
// or whose budget ran out is not a server failure, and reporting it as a 500
// would drown the real failures in noise (the same mapping as core/link's
// wrapDB).
func wrapDB(err error, format string, a ...any) error {
	switch {
	case err == nil:
		return nil
	case coreerrors.Is(err, context.Canceled), coreerrors.Is(err, context.DeadlineExceeded):
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeCanceled,
			format+" (the context was canceled)", a...)
	default:
		return coreerrors.Wrap(err, coreerrors.KindInternal, codeIndexFailed, format, a...)
	}
}
