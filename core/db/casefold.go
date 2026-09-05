package db

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// This file answers one question at startup: can this database fold the case of
// letters that are not ASCII?
//
// # Why it is worth a query
//
// Both of gobit's search paths are case-insensitive, and both delegate that to
// PostgreSQL:
//
//   - the storefront listing's own filter is `title ILIKE '%' || $q || '%'`
//     (internal/modules/product/repository), and
//   - the searchpg plugin's index is `to_tsvector('simple', …)`.
//
// Neither is a string comparison in Go. Both depend on the cluster's CTYPE, and
// a cluster created with `--locale=C` folds ASCII only. On such a cluster a
// shopper typing "çanta" gets ZERO results for a product titled "Çanta" — no
// error, no log line, no metric. The catalog looks empty and the search box
// looks broken for reasons nobody can see.
//
// The failure is invisible to a test suite as well, because test fixtures are
// usually ASCII. That is exactly the class of defect this repository answers
// with a loud startup signal rather than a comment.
//
// # Why the BEHAVIOR is tested and not the locale name
//
// Reading `datctype` and comparing it against a list of known-good names would
// be a proxy for the thing that matters, and proxies drift: an installation may
// run a locale nobody here anticipated, and the question is not what it is
// called but what it does.
//
// # Why BOTH halves are tested
//
// They can disagree, and that was measured rather than assumed. A cluster
// created with the ICU provider folds ILIKE correctly while `to_tsvector`
// keeps using the database CTYPE and does NOT:
//
//	--locale=C                      ILIKE ✗   to_tsvector ✗
//	--locale=C.UTF-8                ILIKE ✓   to_tsvector ✓
//	--locale=C --locale-provider=icu ILIKE ✓  to_tsvector ✗
//
// Checking only the first would hand an ICU installation a clean bill of health
// while its product search stayed silently broken.

// caseFoldingProbe asks the database to fold two non-ASCII letters, once
// through the pattern matcher the storefront filter uses and once through the
// text-search parser the search plugin uses.
//
// The letters are Turkish because that is where this was found, but nothing
// here is Turkish-specific: any cluster that folds these folds the accented
// letters of every other language the same way.
const caseFoldingProbe = `SELECT
	('Ç' ILIKE 'ç') AS pattern,
	(to_tsvector('simple', 'ÇANTA') @@ websearch_to_tsquery('simple', 'çanta')) AS fulltext`

// CaseFolding reports how the database handles case outside ASCII.
type CaseFolding struct {
	// Pattern is true when ILIKE folds non-ASCII letters. The storefront's
	// `?q=` filter depends on it.
	Pattern bool
	// FullText is true when the text-search parser folds them. The searchpg
	// plugin's index depends on it.
	FullText bool
}

// OK reports whether both search paths fold non-ASCII case.
//
// The conjunction states the REQUIREMENT — both paths must fold — and not the
// cheapest way to detect today's failures. Measured across the three clusters
// this repository can produce, FullText implies Pattern: no configuration was
// found where the text-search parser folds and the pattern matcher does not, so
// `return c.FullText` alone would behave identically and no test can tell the
// two apart. That is a property of PostgreSQL 16's implementation, not of what
// gobit needs, and it is written down here rather than compiled into a
// shortcut that would go quietly wrong if the two ever separated.
func (c CaseFolding) OK() bool { return c.Pattern && c.FullText }

// checkCaseFolding runs the probe and logs a warning when either half fails.
//
// It NEVER fails startup, and that is deliberate: a catalog written entirely in
// ASCII works perfectly on a C-locale cluster, and refusing to open would break
// installations that have nothing wrong with them. What is not acceptable is
// staying silent — so the log line says exactly which query stops working and
// exactly how to fix the cluster.
func checkCaseFolding(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	var folding CaseFolding
	if err := pool.QueryRow(ctx, caseFoldingProbe).Scan(&folding.Pattern, &folding.FullText); err != nil {
		// A probe that cannot run is not a reason to refuse service; the pool
		// has already answered a Ping.
		log.WarnContext(ctx, "the database case-folding check could not run", "error", err)

		return
	}
	if folding.OK() {
		return
	}

	log.WarnContext(ctx,
		"this database folds ASCII case only; search will silently miss non-ASCII text",
		slog.Bool("pattern_matching", folding.Pattern),
		slog.Bool("full_text", folding.FullText),
		slog.String("effect", `a shopper searching "çanta" finds nothing for a product titled "Çanta"`),
		slog.String("fix", "recreate the cluster with --locale=C.UTF-8 (initdb); an existing "+
			"data directory keeps the locale it was created with, so this needs a dump and restore"),
	)
}
