package service_test

import (
	"context"
	"sync"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// fakeRepo is an in-memory stand-in for the repository.
//
// It imitates the two behaviors the rules depend on: a moderation refuses a
// row whose current status is not the one the caller believed, and the
// storefront's read is a DIFFERENT method that filters on approval. The second
// is the important one — a fake whose ListApproved took a status parameter
// would let a test pass that the real repository would fail, and the whole
// module is about that filter.
//
// It does NOT imitate the database's CHECK constraints. A rating of 9 or a
// status the column does not know is refused by PostgreSQL and proven against a
// real one in the module's integration test; a fake that refused them here
// would be proving its own code.
type fakeRepo struct {
	mu sync.Mutex

	reviews map[string]models.Review
	// order keeps the insertion sequence, so a listing comes back in a
	// deterministic order without the fake having to imitate the keyset walk.
	order []string

	// createErr and moderateErr script a failure.
	createErr   error
	moderateErr error

	// listFilter records what the admin listing was last asked for.
	listFilter models.Filter
	// approvedFor records the product the storefront listing was last asked
	// for.
	approvedFor string
}

// newFakeRepo builds an empty fake.
func newFakeRepo() *fakeRepo {
	return &fakeRepo{reviews: map[string]models.Review{}}
}

// Create stores the review as it was handed over.
func (f *fakeRepo) Create(_ context.Context, in models.Review) (models.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.createErr != nil {
		return models.Review{}, f.createErr
	}

	f.reviews[in.ID] = in
	f.order = append(f.order, in.ID)

	return in, nil
}

// Get returns the review whatever its status.
func (f *fakeRepo) Get(_ context.Context, id string) (models.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	review, found := f.reviews[id]
	if !found {
		return models.Review{}, errors.NotFound("review_not_found", "no such review: %s", id)
	}

	return review, nil
}

// Moderate refuses a row that is not in the status the caller believed, the
// same way the real conditional UPDATE does.
func (f *fakeRepo) Moderate(
	_ context.Context, id string, from, to models.Status, note string,
) (models.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.moderateErr != nil {
		return models.Review{}, f.moderateErr
	}

	review, found := f.reviews[id]
	if !found {
		return models.Review{}, errors.NotFound("review_not_found", "no such review: %s", id)
	}
	if review.Status != from {
		return models.Review{}, errors.Conflict("review_conflict",
			"review %s is no longer in status %q", id, from)
	}

	review.Status = to
	review.ModerationNote = note
	review.ModeratedAt = fixedNow
	f.reviews[id] = review

	return review, nil
}

// List returns every review matching the filter, in insertion order.
func (f *fakeRepo) List(
	_ context.Context, filter models.Filter,
) ([]models.Review, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.listFilter = filter

	matched := f.matching(func(review models.Review) bool {
		if filter.Status != nil && review.Status.String() != *filter.Status {
			return false
		}
		if filter.ProductID != nil && review.ProductID != *filter.ProductID {
			return false
		}

		return true
	})

	return limited(matched, filter.Limit), int64(len(matched)), nil
}

// ListApproved returns only the approved reviews of one product.
//
// There is deliberately no status parameter: the real query carries the literal
// and this fake carries it too, so a test cannot accidentally exercise a path
// production does not have.
func (f *fakeRepo) ListApproved(
	_ context.Context, productID string, filter models.Filter,
) ([]models.Review, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.approvedFor = productID

	matched := f.matching(func(review models.Review) bool {
		return review.ProductID == productID && review.Status == models.StatusApproved
	})

	return limited(matched, filter.Limit), int64(len(matched)), nil
}

// Summarize counts and averages the approved reviews of one product.
func (f *fakeRepo) Summarize(_ context.Context, productID string) (models.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	matched := f.matching(func(review models.Review) bool {
		return review.ProductID == productID && review.Status == models.StatusApproved
	})

	out := models.Summary{ProductID: productID, Count: int64(len(matched))}
	if out.Count == 0 {
		return out, nil
	}

	var sum int64
	for i := range matched {
		sum += int64(matched[i].Rating)
	}

	// Rounded half away from zero, the same rule PostgreSQL's round() applies,
	// so a fake-backed expectation and a database-backed one agree.
	out.AverageHundredths = (sum*100*2/out.Count + 1) / 2

	return out, nil
}

// matching collects the reviews the predicate accepts, in INSERTION order. The
// caller holds the lock.
//
// The order matters: the real listing walks a keyset index and a fake returning
// map order would make a paging assertion pass or fail by chance.
func (f *fakeRepo) matching(keep func(models.Review) bool) []models.Review {
	var out []models.Review
	for _, id := range f.order {
		review := f.reviews[id]
		if keep(review) {
			out = append(out, review)
		}
	}

	return out
}

// limited applies the fetch bound the service asked for.
func limited(items []models.Review, limit int64) []models.Review {
	if limit > 0 && int64(len(items)) > limit {
		return items[:limit]
	}

	return items
}

// seed stores a review directly, bypassing the rules, so a test can start from
// a state the storefront could not produce (an approved review, say).
func (f *fakeRepo) seed(review models.Review) models.Review {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.reviews[review.ID] = review
	f.order = append(f.order, review.ID)

	return review
}

// newService builds a service over an empty fake.
func newService() (*service.Service, *fakeRepo) {
	repo := newFakeRepo()

	return service.New(repo, service.Options{}), repo
}
