// Package service holds the review module's rules.
//
// # The one guarantee
//
// A review is INVISIBLE on the storefront until an operator approves it. Every
// storefront read in this package goes through a repository method whose SQL
// carries status = 'approved' as a literal, and there is no method a caller can
// reach that takes the status as an argument.
//
// That is the property decision A15 in docs/gaps.md turns on: the storefront's
// only principal is the publishable key, so this module cannot know who wrote a
// review, and what makes the write acceptable is that a human stands between it
// and its effect — the same argument the order module's storefront return
// request already rests on.
//
// # What it does not know
//
// It does not know what a product is. The subject of a review is an identifier
// belonging to another module, stored and never validated (Principle 2.2), the
// same way an order line stores the variant it sold.
package service

import (
	"context"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/bdrtr/gobit/core/errors"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/review/models"
)

// Error codes.
const (
	// CodeInvalidInput reports that the request could not be accepted.
	CodeInvalidInput = "review_invalid_input"
	// CodeNotFound reports that the review does not exist.
	CodeNotFound = "review_not_found"
	// CodeTransition reports a status move the review may not make.
	CodeTransition = "review_invalid_transition"
)

// ReviewListing names this listing inside a cursor.
//
// A cursor carries the name of the listing it belongs to so that one handed to
// a different listing is REFUSED rather than silently selecting the wrong rows.
// The admin queue and the storefront listing share the NAME on purpose: they
// walk the same table in the same order, so a cursor from one really does name
// a position in the other, and refusing it would be refusing a position that is
// genuinely valid. What the storefront cursor does NOT carry is permission —
// the status literal is in the SQL, so a cursor taken from the admin queue
// still lands inside the approved-only listing.
const ReviewListing = "reviews"

// DefaultLimit and MaxLimit bound the listing page size.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// The bounds on what an author may write.
//
// They are here rather than as column widths, because a text column with a
// length is a migration away from every change of mind, while these are a
// product decision that belongs next to the message explaining it.
const (
	// MinRating and MaxRating are the star bounds. They match the CHECK the
	// column carries; the database is the one that cannot be bypassed and this
	// is the one that can say WHY in a sentence a client reads.
	MinRating = 1
	MaxRating = 5
	// MaxAuthorNameLen is the longest byline, in runes.
	MaxAuthorNameLen = 80
	// MaxTitleLen is the longest headline, in runes.
	MaxTitleLen = 120
	// MaxBodyLen is the longest review text, in runes.
	//
	// It is counted in RUNES and not in bytes, so a Turkish review is not
	// shorter than an English one by the length of its diacritics.
	MaxBodyLen = 4000
	// MaxNoteLen is the longest moderation note, in runes. It is an operator's
	// sentence rather than a document, so it is bounded well below the body.
	MaxNoteLen = 1000
)

// Repo is the storage surface the service needs.
//
// It is declared HERE, on the consumer's side, so the service depends on the
// methods it calls rather than on a package. The storefront's two reads are
// SEPARATE methods from the admin listing rather than the same one with a
// filter: that is what makes "the storefront sees only approved reviews" a
// property of the type rather than of every call site.
type Repo interface {
	Create(ctx context.Context, in models.Review) (models.Review, error)
	Get(ctx context.Context, id string) (models.Review, error)
	Moderate(
		ctx context.Context, id string, from, to models.Status, note string,
	) (models.Review, error)
	List(ctx context.Context, filter models.Filter) ([]models.Review, int64, error)
	ListApproved(
		ctx context.Context, productID string, filter models.Filter,
	) ([]models.Review, int64, error)
	Summarize(ctx context.Context, productID string) (models.Summary, error)
}

// Options are the service's settings.
type Options struct {
	// Logger falls back to slog.Default when nil.
	Logger *slog.Logger
}

// Service is the review module's rules.
type Service struct {
	repo Repo
	log  *slog.Logger
}

// New builds the service.
func New(repo Repo, opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return &Service{repo: repo, log: opts.Logger}
}

// Page is one page of a review listing.
type Page struct {
	// Items are the reviews on this page.
	Items []models.Review
	// Count is the total number of reviews matching the filter.
	Count int64
	// Limit and Offset are the bounds that were applied.
	Limit  int64
	Offset int64
	// NextCursor is the opaque position the NEXT page starts below; empty means
	// this page is the last one.
	NextCursor string
}

// boundedFilter refuses a page request that makes no sense and applies the
// default size.
//
// It returns the filter with one row MORE than was asked for: that is how "is
// there a next page" is answered without a second query, and it is what lets
// the cursor be absent on the last page. The caller trims with [pageOf].
func boundedFilter(filter models.Filter) (fetch models.Filter, limit int64, err error) {
	limit = filter.Limit
	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit < 0:
		return models.Filter{}, 0, errors.Invalid(CodeInvalidInput,
			"the limit cannot be negative: %d", limit)
	case limit > MaxLimit:
		return models.Filter{}, 0, errors.Invalid(CodeInvalidInput,
			"the limit can be at most %d: %d", MaxLimit, limit)
	}
	if filter.Offset < 0 {
		return models.Filter{}, 0, errors.Invalid(CodeInvalidInput,
			"the offset cannot be negative: %d", filter.Offset)
	}

	fetch = filter
	fetch.Limit = limit + 1

	return fetch, limit, nil
}

// pageOf trims the extra row off and mints the cursor when there was one.
func pageOf(items []models.Review, count, limit, offset int64) Page {
	out := Page{Items: items, Count: count, Limit: limit, Offset: offset}
	if int64(len(items)) > limit {
		out.Items = items[:limit]
		last := out.Items[len(out.Items)-1]
		out.NextCursor = corepage.Encode(ReviewListing,
			corepage.Cursor{Time: last.CreatedAt, ID: last.ID})
	}

	return out
}

// trimmedLen returns the rune count of the trimmed value.
func trimmedLen(value string) int { return utf8.RuneCountInString(strings.TrimSpace(value)) }
