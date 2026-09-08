package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/review/models"
)

// reviewWithAProposal is a review carrying every field a proposal has, so a
// leak has something recognizable to leak.
func reviewWithAProposal() models.Review {
	return models.Review{
		ID:         "rev_1",
		ProductID:  "prod_1",
		Rating:     4,
		Title:      "good",
		Body:       "it arrived quickly",
		AuthorName: "A customer",
		Status:     models.StatusApproved,
		Suggestion: &models.Suggestion{
			Status: models.StatusRejected,
			At:     time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC),
			Note:   "the text reads like an advertisement",
			Model:  "a-model-3",
		},
		CreatedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	}
}

// TestAProposalNeverReachesAShopper is checked on the ENCODED body rather than
// on the struct.
//
// A field absent from storeReviewDTO is the reason this holds today, and a test
// asserting that would be asserting the thing it is trying to protect. What a
// shopper receives is the JSON, so that is what is read: any future field, any
// embedded struct and any inline map would be caught by the same assertion.
//
// The proposal is an operator's material twice over. It is a sentence about a
// stranger's words written for somebody deciding what to publish — which is
// already why the moderation note stays out of a shopper's view — and this one
// was not written by a person at all, so publishing it would put a machine's
// judgement of a customer's writing on the page next to that customer's name.
func TestAProposalNeverReachesAShopper(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(toStoreReviewDTO(reviewWithAProposal()))
	require.NoError(t, err)

	body := strings.ToLower(string(raw))

	for _, forbidden := range []string{
		"suggest", "proposal", "a-model-3", "advertisement",
	} {
		assert.NotContains(t, body, forbidden,
			"the storefront body carries %q; a shopper is being shown a machine's "+
				"judgement of a customer's own writing", forbidden)
	}
}

// TestAnOperatorSeesTheWholeProposal is the other half, and it is what makes
// the test above mean something.
//
// Without it, a converter that dropped the proposal on BOTH surfaces would pass
// the leak test perfectly while the feature did nothing at all.
func TestAnOperatorSeesTheWholeProposal(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(toAdminReviewDTO(reviewWithAProposal()))
	require.NoError(t, err)

	var body struct {
		Suggestion *adminSuggestionDTO `json:"suggestion"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.NotNil(t, body.Suggestion, "the operator's body carries no proposal at all")

	assert.Equal(t, models.StatusRejected.String(), body.Suggestion.Status)
	assert.Equal(t, "a-model-3", body.Suggestion.Model)
	assert.Equal(t, "the text reads like an advertisement", body.Suggestion.Note)
	assert.False(t, body.Suggestion.At.IsZero(), "a proposal with no moment cannot be told from a fresh one")
}

// TestNoProposalIsAnAbsentFieldRatherThanAnEmptyObject pins the choice the DTO
// makes, because the alternative is the one a client cannot read.
//
// An object full of zero values says a model proposed nothing, in a status that
// is the empty string, at the zero instant. "Nobody has been asked" is what the
// row means, and an absent field is how JSON says it — the same choice
// moderated_at already makes on this DTO.
func TestNoProposalIsAnAbsentFieldRatherThanAnEmptyObject(t *testing.T) {
	t.Parallel()

	plain := reviewWithAProposal()
	plain.Suggestion = nil

	raw, err := json.Marshal(toAdminReviewDTO(plain))
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))

	_, present := body["suggestion"]
	assert.False(t, present,
		"a review nobody has proposed anything about carries a suggestion key; "+
			"a client cannot tell that from a proposal that says nothing")
}
