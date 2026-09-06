package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/review/models"
)

// SubmitInput is a review arriving from the storefront.
//
// # What is NOT in it
//
// There is no email address and no order id, and both absences are decisions
// rather than omissions.
//
// An email would be a contact detail collected under the cover of publishing a
// review — an unverified mailing list with no unsubscribe, which is the exact
// property that makes the back-in-stock waitlist fail decision A15 in
// docs/gaps.md. The notification module already refuses to store a recipient
// address for the same reason.
//
// An order id would let the row claim "verified purchase" and the claim would
// be false: ADR 0008 leaves customer identity to the embedding application, so
// an order id proves only that the writer has one, and it is the same
// credential the storefront return request already runs on. What it would buy —
// a narrower spam surface — is bought instead by the thing that actually works,
// which is that nothing is published until a person approves it.
type SubmitInput struct {
	// ProductID is what the review is about.
	//
	// It is the product's ID and not its handle. A storefront that renders a
	// product page has already read the product and holds its id, so nothing
	// has to be resolved here; taking a handle would mean this module either
	// stores two different keys for one product — splitting its reviews in half
	// silently — or reaches into the product module to resolve one, putting a
	// storefront WRITE behind another module being registered.
	ProductID string
	// Rating is the star count, [MinRating] to [MaxRating].
	Rating int16
	// Title is the headline; it may be empty.
	Title string
	// Body is the review text.
	Body string
	// AuthorName is the byline the author typed. See [models.Review] for why it
	// is the only thing stored about them.
	AuthorName string
}

// Submit stores a review and returns it in [models.StatusSubmitted].
//
// # It is published to nobody
//
// The status is set HERE and is not taken from the input: a submitted status
// the caller could choose would be an approval endpoint wearing a submission's
// name. From this moment the review is visible to an operator through the admin
// queue and to nobody else, and it stays that way until somebody moves it.
//
// # What this method deliberately does not do
//
// It does not check that the product exists. That would mean reading another
// module on the write path of an unauthenticated endpoint, and the module
// already has the right answer to a review about nothing: an operator does not
// approve it. It also does not deduplicate — two identical reviews from the
// same person are two rows, because the module has no identity to tell "the
// same person" from "two people who agree".
func (s *Service) Submit(ctx context.Context, in SubmitInput) (models.Review, error) {
	if err := in.validate(); err != nil {
		return models.Review{}, err
	}

	review := models.Review{
		ID:        models.NewReviewID(),
		ProductID: strings.TrimSpace(in.ProductID),
		Rating:    in.Rating,
		// The text is stored TRIMMED. A body of three newlines would otherwise
		// pass the emptiness check the moment it was written and arrive in the
		// moderation queue as a blank card an operator has to open to see is
		// blank.
		Title:      strings.TrimSpace(in.Title),
		Body:       strings.TrimSpace(in.Body),
		AuthorName: strings.TrimSpace(in.AuthorName),
		Status:     models.StatusSubmitted,
	}

	stored, err := s.repo.Create(ctx, review)
	if err != nil {
		return models.Review{}, err
	}

	s.log.DebugContext(ctx, "a review was submitted",
		"review_id", stored.ID, "product_id", stored.ProductID, "status", stored.Status)

	return stored, nil
}

// validate refuses a submission that could not become a readable review.
func (in SubmitInput) validate() error {
	switch {
	case strings.TrimSpace(in.ProductID) == "":
		return errors.Invalid(CodeInvalidInput, "the product id is required")
	case in.Rating < MinRating || in.Rating > MaxRating:
		return errors.Invalid(CodeInvalidInput,
			"the rating has to be between %d and %d: %d", MinRating, MaxRating, in.Rating)
	case trimmedLen(in.AuthorName) == 0:
		return errors.Invalid(CodeInvalidInput,
			"a name to publish the review under is required; "+
				"a shopper who wants no real name types one they choose")
	case trimmedLen(in.AuthorName) > MaxAuthorNameLen:
		return errors.Invalid(CodeInvalidInput,
			"the name can be at most %d characters", MaxAuthorNameLen)
	case trimmedLen(in.Title) > MaxTitleLen:
		return errors.Invalid(CodeInvalidInput,
			"the title can be at most %d characters", MaxTitleLen)
	case trimmedLen(in.Body) == 0:
		return errors.Invalid(CodeInvalidInput, "a review cannot be empty")
	case trimmedLen(in.Body) > MaxBodyLen:
		return errors.Invalid(CodeInvalidInput,
			"the review can be at most %d characters", MaxBodyLen)
	}

	return nil
}

// ListApproved pages the reviews of a product that a shopper may see.
//
// The status is not a parameter and cannot be made one: the repository method
// this calls carries the literal in its SQL. That is the whole design, and it
// is written as a type rather than as a filter so no future caller can widen it
// by forgetting a field.
func (s *Service) ListApproved(
	ctx context.Context, productID string, filter models.Filter,
) (Page, error) {
	if strings.TrimSpace(productID) == "" {
		return Page{}, errors.Invalid(CodeInvalidInput, "the product id is required")
	}

	fetch, limit, err := boundedFilter(filter)
	if err != nil {
		return Page{}, err
	}

	items, count, err := s.repo.ListApproved(ctx, productID, fetch)
	if err != nil {
		return Page{}, err
	}

	return pageOf(items, count, limit, filter.Offset), nil
}

// Summarize returns the count and average a product page shows.
//
// A product with no approved review comes back with a count of zero and an
// average of zero rather than as a NOT FOUND: this module does not know whether
// the product exists, and answering "not found" would tell a client something
// about the catalog that this module is in no position to say.
func (s *Service) Summarize(ctx context.Context, productID string) (models.Summary, error) {
	if strings.TrimSpace(productID) == "" {
		return models.Summary{}, errors.Invalid(CodeInvalidInput, "the product id is required")
	}

	return s.repo.Summarize(ctx, productID)
}
