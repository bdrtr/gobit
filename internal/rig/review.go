package rig

import "time"

// The review family's shape.
//
// # Why the rig grew reviews at all
//
// The review module's migration carries timing figures measured "against
// PostgreSQL 16 on 505,000 reviews over 20,001 products", and that database is
// gone. It is the same hole this package was written to close for the catalog:
// a performance sentence resting on rows nobody promised to keep, with
// `docker compose down -v` the whole distance between a measured claim and
// unfalsifiable prose.
//
// This family does NOT reproduce that database and does not pretend to — the
// product count alone differs, and the original mix of statuses was never
// written down. What it gives is a shape somebody can rebuild and measure
// again, which is what the older figures never had.
const (
	// DefaultReviews is how many reviews the rig builds, and it is ZERO.
	//
	// [DefaultSpec] returns the shape this repository's catalog figures were
	// measured on, and that catalog had no reviews at all. A default that added
	// half a million rows would make every one of those sentences describe a
	// database nobody measured. The reviews are an ADDITION a caller asks for
	// by name, which is [Spec.SkewedCategorySize]'s rule.
	DefaultReviews = 0
	// ReviewTable is the table a caller asserts a non-empty review set against.
	ReviewTable = "reviews"
)

// generatedReviewPattern matches the ids this family writes, and nothing else.
//
// A generated id is 'rev_R' plus digits; a real one is 'rev_' plus 26 Crockford
// Base32 characters. The two cannot collide, which is what lets the reset
// delete the rig's rows and leave a developer's own behind.
const generatedReviewPattern = `^rev_R[0-9]{1,12}$`

// The review family's timestamps.
//
// FOUR of them, and it is the catalog family's reasoning rather than a copy of
// its numbers: the listings order by (created_at DESC, id DESC), so a rig with
// a distinct timestamp per row would never exercise the id tie-break that
// decides a page boundary, and every keyset figure would be measured on the
// easy case. Four values over half a million rows makes the tie-break carry
// almost the whole ordering, which is the hard case and the honest one.
//
// They sit AFTER the catalog's, because a review is written about a product
// that already exists.
var (
	reviewCreatedAt = []time.Time{
		time.Date(2026, 9, 4, 6, 15, 3, 114287000, time.UTC),
		time.Date(2026, 9, 4, 9, 41, 52, 660913000, time.UTC),
		time.Date(2026, 9, 5, 7, 2, 18, 205746000, time.UTC),
		time.Date(2026, 9, 5, 18, 33, 41, 872054000, time.UTC),
	}
	// reviewModeratedAt is when the decided rows were decided, and it is later
	// than every value above so no row claims to have been moderated before it
	// was written.
	reviewModeratedAt = time.Date(2026, 9, 6, 11, 27, 5, 431802000, time.UTC)
	// reviewSuggestedAt is when the proposals were made.
	reviewSuggestedAt = time.Date(2026, 9, 6, 12, 8, 44, 719355000, time.UTC)
)

// reviewSteps builds the review family.
//
// # The proportions, and what they make a measurement over this rig MEAN
//
// One row in ten is waiting, one in ten was refused, and the rest are published.
// A moderation queue is small next to the archive behind it — a shop running for
// a year has handled almost everything and is looking at what arrived this week
// — so a rig whose rows were half unmoderated would measure a state no live shop
// is in. The numbers live in the statement below and nowhere else: a Go constant
// repeating them would be a second copy free to drift from the SQL that decides.
//
// # A proposal is WRITTEN only about a waiting review, and it SURVIVES the
// decision
//
// Both halves matter and the second is the one a first draft of this file got
// wrong. `Service.Suggest`'s statement carries `status = 'submitted'` as a
// literal, so nothing can propose about a decided review — but nothing clears
// the columns when an operator decides either, so a shop that has been running
// the job for a while carries proposals on its ARCHIVE as well as on its queue,
// and the archive is the part that grows without bound.
//
// A rig that put proposals only on waiting reviews would describe the first
// month of an installation and nothing after it, and an index measured on it
// would be measured on a set that stops being the real one. So the family
// builds both: half the waiting reviews carry a proposal, and a share of the
// decided ones carry the proposal they were decided against.
//
// # Half of the waiting reviews carry one, and that is the point
//
// A rig where every waiting review had a proposal could not measure the
// operator's actual question — "what has the model not reached yet" — and one
// where none did could not measure the other. The split also means the
// suggestion filter's selectivity is a KNOWN fraction of the queue rather than
// something a reader has to guess.
func reviewSteps(spec Spec) []step {
	count := spec.Reviews
	if count == 0 {
		return nil
	}

	products := spec.SingleVariantProducts
	if products == 0 {
		// With no family B there is nothing to be a review OF. The reviews are
		// still built, all of them about the same absent product, because
		// product_id is another module's identifier and is deliberately not
		// validated (Principle 2.2) — a rig that refused here would be
		// enforcing a foreign key the schema does not have.
		products = 1
	}

	return []step{{
		name: ReviewTable,
		sql: `INSERT INTO reviews (
    id, product_id, rating, title, body, author_name, status,
    moderated_at, moderation_note,
    suggested_status, suggested_at, suggestion_note, suggestion_model,
    created_at, updated_at
)
SELECT
    'rev_R' || n,
    'prod_B' || (((n - 1) % $2::int) + 1),
    ((n % 5) + 1)::smallint,
    CASE WHEN n % 3 = 0 THEN '' ELSE 'Review ' || n END,
    'Generated review ' || n || ' for the measurement rig.',
    'Rig Customer ' || ((n % 997) + 1),
    CASE
        WHEN n % 10 = 0 THEN 'submitted'
        WHEN n % 10 = 1 THEN 'rejected'
        ELSE 'approved'
    END,
    CASE WHEN n % 10 = 0 THEN NULL ELSE $3::timestamptz END,
    CASE WHEN n % 10 = 1 THEN 'refused by the rig' ELSE '' END,
    -- A proposal is carried by half the WAITING rows and by three in ten of the
    -- DECIDED ones: the job reached those before an operator did, and the
    -- decision left the columns where they were.
    CASE
        WHEN NOT ((n % 10 = 0 AND (n / 10) % 2 = 0) OR (n % 10 <> 0 AND n % 10 < 4)) THEN NULL
        WHEN (n / 20) % 2 = 0 THEN 'approved'
        ELSE 'rejected'
    END,
    CASE WHEN (n % 10 = 0 AND (n / 10) % 2 = 0) OR (n % 10 <> 0 AND n % 10 < 4)
         THEN $4::timestamptz ELSE NULL END,
    CASE WHEN (n % 10 = 0 AND (n / 10) % 2 = 0) OR (n % 10 <> 0 AND n % 10 < 4)
         THEN 'proposed by the rig' ELSE '' END,
    CASE WHEN (n % 10 = 0 AND (n / 10) % 2 = 0) OR (n % 10 <> 0 AND n % 10 < 4)
         THEN 'rig-model-1' ELSE '' END,
    ($5::timestamptz[])[(n % 4) + 1],
    ($5::timestamptz[])[(n % 4) + 1]
FROM generate_series(1, $1::int) AS n
ON CONFLICT (id) DO NOTHING`,
		args: []any{count, products, reviewModeratedAt, reviewSuggestedAt, reviewCreatedAt},
	}}
}

// reviewResetStep deletes the rig's reviews and leaves everything else.
func reviewResetStep() step {
	return step{
		name: ReviewTable,
		sql:  `DELETE FROM reviews WHERE id ~ $1`,
		args: []any{generatedReviewPattern},
	}
}
