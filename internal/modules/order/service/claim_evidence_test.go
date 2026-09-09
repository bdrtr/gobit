package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// claimForEvidence opens an order and a claim on it.
func claimForEvidence(ctx context.Context, t *testing.T, e env) models.Claim {
	t.Helper()

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	claim, err := e.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID: order.ID,
		Type:    models.ClaimReplace,
		Reason:  "arrived damaged",
	})
	require.NoError(t, err)

	return claim
}

// TestAClaimCanShowWhatWentWrong is the record a claim could not carry.
//
// `order_claims` held a reason and a note, so "the box was crushed" was a
// SENTENCE and never a picture. An operator deciding whether to replace goods
// was reading a description of evidence rather than the evidence.
func TestAClaimCanShowWhatWentWrong(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim := claimForEvidence(ctx, t, e)

	evidence, err := e.svc.AttachClaimEvidence(ctx, claim.ID, service.AttachClaimEvidenceInput{
		UploadID: "upl_crushed_box", Caption: "  the corner  ",
	})
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(evidence.ID, models.ClaimEvidenceIDPrefix))
	assert.Equal(t, claim.ID, evidence.OrderClaimID)
	assert.Equal(t, "upl_crushed_box", evidence.UploadID)
	assert.Equal(t, "the corner", evidence.Caption, "the caption is trimmed")
}

// TestAClaimCarriesSeveralPiecesOfEvidence is why it is a table.
//
// A damaged parcel is rarely one photograph, and a column could hold exactly
// one — which would make the second picture a reason to overwrite the first.
func TestAClaimCarriesSeveralPiecesOfEvidence(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim := claimForEvidence(ctx, t, e)

	for _, upload := range []string{"upl_one", "upl_two", "upl_three"} {
		_, err := e.svc.AttachClaimEvidence(ctx, claim.ID, service.AttachClaimEvidenceInput{
			UploadID: upload,
		})
		require.NoError(t, err)
	}

	evidence, err := e.svc.ListClaimEvidence(ctx, claim.ID)
	require.NoError(t, err)
	require.Len(t, evidence, 3)
	assert.Equal(t, "upl_one", evidence[0].UploadID, "oldest first")
	assert.Equal(t, "upl_three", evidence[2].UploadID)
}

// TestTheSameFileIsEvidenceOnce keeps a double click from becoming a second
// piece of evidence.
func TestTheSameFileIsEvidenceOnce(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim := claimForEvidence(ctx, t, e)

	_, err := e.svc.AttachClaimEvidence(ctx, claim.ID, service.AttachClaimEvidenceInput{
		UploadID: "upl_same",
	})
	require.NoError(t, err)

	_, err = e.svc.AttachClaimEvidence(ctx, claim.ID, service.AttachClaimEvidenceInput{
		UploadID: "upl_same",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
}

// TestEvidenceOnAnUnknownClaimIsNotFound turns a foreign key's constraint name
// into an answer an operator can act on.
func TestEvidenceOnAnUnknownClaimIsNotFound(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	_, err := e.svc.AttachClaimEvidence(ctx, "claim_does_not_exist",
		service.AttachClaimEvidenceInput{UploadID: "upl_one"})

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

// TestEvidenceWithoutAFileIsRefused keeps out the one value that claims a file
// while naming none.
func TestEvidenceWithoutAFileIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim := claimForEvidence(ctx, t, e)

	_, err := e.svc.AttachClaimEvidence(ctx, claim.ID,
		service.AttachClaimEvidenceInput{UploadID: "  "})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestDetachingEvidenceLeavesTheOthers proves the delete is one binding, not a
// sweep.
func TestDetachingEvidenceLeavesTheOthers(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim := claimForEvidence(ctx, t, e)

	first, err := e.svc.AttachClaimEvidence(ctx, claim.ID,
		service.AttachClaimEvidenceInput{UploadID: "upl_one"})
	require.NoError(t, err)
	_, err = e.svc.AttachClaimEvidence(ctx, claim.ID,
		service.AttachClaimEvidenceInput{UploadID: "upl_two"})
	require.NoError(t, err)

	require.NoError(t, e.svc.DetachClaimEvidence(ctx, first.ID))

	evidence, err := e.svc.ListClaimEvidence(ctx, claim.ID)
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	assert.Equal(t, "upl_two", evidence[0].UploadID)
}
