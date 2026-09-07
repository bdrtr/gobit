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
// THREE things in this repository delegate case folding to PostgreSQL:
//
//   - the storefront listing's own filter is `title ILIKE '%' || $q || '%'`
//     (internal/modules/product/repository),
//   - the searchpg plugin's index is `to_tsvector('simple', …)`, and
//   - three identity modules guard their e-mail column with
//     `CHECK (email = lower(email))` (auth, customer and b2b, each in their
//     000001).
//
// None is a string comparison in Go. All three depend on the cluster's CTYPE,
// and a cluster created with `--locale=C` folds ASCII only. On such a cluster a
// shopper typing "çanta" gets ZERO results for a product titled "Çanta" — no
// error, no log line, no metric. The catalog looks empty and the search box
// looks broken for reasons nobody can see.
//
// # The third path was added later, and it is not a search path
//
// The lower() probe arrived on 2026-09-07, out of ADR 0038. That decision moved
// the invoice module's erasure off `lower(buyer_email)` because the fold it was
// asking for belonged to the cluster rather than to gobit — and it stated, as a
// consequence, that "nothing in the tree now depends on lower() folding
// non-ASCII". That was WRONG, and checking it is what added this probe: the
// three e-mail CHECK constraints depend on exactly that.
//
// What they lose is not a search result. Measured on a --locale=C cluster, the
// constraint `CHECK (email <> '' AND email = lower(email))` REFUSES the
// unfolded ASCII address "Ada@Example.com" and ACCEPTS an unfolded address
// whose capitals are outside ASCII (a Turkish dotted capital I, U+0130, and an
// O with diaeresis, U+00D6 — written as code points because this file is
// English and the language ratchet's diacritic lane reads comments), because
// lower() leaves those capitals alone and the value therefore equals its own
// lower(). The guard that is supposed to be the last
// defense behind the Go fold — the one that would catch a direct SQL write or a
// future code path that forgot — silently stops guarding at the ASCII boundary.
// Two rows for one person is what a unique index on that column cannot then
// prevent, and one person with two accounts is what the fold exists to stop.
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
// # Why ALL THREE are tested
//
// They can disagree, and that was measured rather than assumed. Re-measured on
// 2026-09-07 with the encoding held at UTF8, which is what deploy/docker-compose.yml
// pins:
//
//	initdb (encoding=UTF8)            ILIKE   to_tsvector   lower()
//	--locale=C                          ✗          ✗           ✗
//	--locale=C.UTF-8                    ✓          ✓           ✓
//	--locale=C --locale-provider=icu    ✓          ✗           ✓
//
// A cluster created with the ICU provider folds ILIKE correctly while
// `to_tsvector` keeps using the database CTYPE and does NOT. Checking only the
// first would hand such an installation a clean bill of health while its
// product search stayed silently broken.
//
// The knob is CTYPE and not collation, which was measured by separating them:
// a cluster made with `--lc-collate=C --lc-ctype=C.UTF-8` folds all three, so
// an operator reading `datcollate` to decide whether they are affected is
// reading the wrong column. That is also why this file probes BEHAVIOR rather
// than the locale name, and the section above says so for a different reason —
// the two arguments agree, and this is the measurement behind the second.
//
// # The ENCODING is a fourth variable, and it is what makes the conjunction earn its keep
//
// This function used to say that FullText implies Pattern — that "no
// configuration was found where the text-search parser folds and the pattern
// matcher does not", so `return c.FullText` alone would behave identically and
// no test could tell the two apart. It kept the conjunction anyway, on the
// ground that the equivalence was a property of PostgreSQL's implementation
// rather than of what gobit needs. That was the right call, and there is now a
// measured configuration that proves it rather than merely arguing it:
//
//	--locale=C, encoding SQL_ASCII      ILIKE ✗   to_tsvector ✓   lower() ✗
//
// A cluster left to initdb's own choice of encoding under --locale=C gets
// SQL_ASCII, where the text-search parser reports a match while ILIKE does not.
// The shortcut would have returned "fine" for it. gobit's own compose passes
// `--encoding=UTF8` so this is not a cluster this repository produces — but an
// embedder running their own PostgreSQL is not using this repository's compose,
// and that is the installation the probe exists for.

// caseFoldingProbe asks the database to fold two non-ASCII letters, once
// through the pattern matcher the storefront filter uses and once through the
// text-search parser the search plugin uses.
//
// The letters are Turkish because that is where this was found, but nothing
// here is Turkish-specific: any cluster that folds these folds the accented
// letters of every other language the same way.
const caseFoldingProbe = `SELECT
	('Ç' ILIKE 'ç') AS pattern,
	(to_tsvector('simple', 'ÇANTA') @@ websearch_to_tsquery('simple', 'çanta')) AS fulltext,
	(lower('Ç') = 'ç') AS case_lower`

// CaseFolding reports how the database handles case outside ASCII.
type CaseFolding struct {
	// Pattern is true when ILIKE folds non-ASCII letters. The storefront's
	// `?q=` filter depends on it.
	Pattern bool
	// FullText is true when the text-search parser folds them. The searchpg
	// plugin's index depends on it.
	FullText bool
	// Lower is true when lower() folds them. The e-mail CHECK constraints in
	// auth, customer and b2b depend on it, and unlike the two above, what fails
	// when it is false is a GUARD rather than a query: the constraint keeps
	// accepting rows, it just stops refusing the wrong ones.
	Lower bool
}

// OK reports whether every path that delegates folding to the database folds
// non-ASCII case.
//
// The conjunction states the REQUIREMENT — all three must fold — and not the
// cheapest way to detect today's failures. Across the three UTF8 clusters in
// the table above, Lower and Pattern agree on every row, so dropping either
// would be undetectable by any test built from those three; the SQL_ASCII
// counterexample in the same header is what shows why a conjunction of
// separately measured facts is not the same thing as any one of them.
func (c CaseFolding) OK() bool { return c.Pattern && c.FullText && c.Lower }

// checkCaseFolding runs the probe and logs a warning when either half fails.
//
// It NEVER fails startup, and that is deliberate: a catalog written entirely in
// ASCII works perfectly on a C-locale cluster, and refusing to open would break
// installations that have nothing wrong with them. What is not acceptable is
// staying silent — so the log line says exactly which query stops working and
// exactly how to fix the cluster.
func checkCaseFolding(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	var folding CaseFolding
	if err := pool.QueryRow(ctx, caseFoldingProbe).
		Scan(&folding.Pattern, &folding.FullText, &folding.Lower); err != nil {
		// A probe that cannot run is not a reason to refuse service; the pool
		// has already answered a Ping.
		log.WarnContext(ctx, "the database case-folding check could not run", "error", err)

		return
	}
	if folding.OK() {
		return
	}

	// Each flag is reported separately so the operator is told WHICH of the
	// three stops working. They fail for different reasons and cost different
	// things, and a single "case folding is broken" would leave somebody to
	// guess which of their problems this line explains.
	log.WarnContext(ctx,
		"this database folds ASCII case only; search will silently miss non-ASCII text",
		slog.Bool("pattern_matching", folding.Pattern),
		slog.Bool("full_text", folding.FullText),
		slog.Bool("case_lower", folding.Lower),
		slog.String("effect", `a shopper searching "çanta" finds nothing for a product titled "Çanta"`),
		slog.String("effect_identity", "the e-mail CHECK constraints in auth, customer and b2b "+
			"(email = lower(email)) stop refusing unfolded non-ASCII addresses, so they no longer "+
			"guard against one person holding two accounts"),
		slog.String("fix", "recreate the cluster with --locale=C.UTF-8 (initdb); an existing "+
			"data directory keeps the locale it was created with, so this needs a dump and restore"),
	)
}
