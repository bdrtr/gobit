//go:build integration

// The tests here run against a real PostgreSQL; to run them:
// make test-integration
//
// What they cover is the half a fake cannot: that the REFUSALS come from the
// schema. The service asks the database to keep one file from being evidence
// twice and to refuse evidence on a claim that is not there, and a fake that
// answered those on its own would be testing its own invention (D50).
package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// openClaim opens an order and a claim on it.
func openClaim(ctx context.Context, t *testing.T, svc *service.Service) models.Claim {
	t.Helper()

	order, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	claim, err := svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID: order.ID,
		Type:    models.ClaimReplace,
		Reason:  "arrived damaged",
	})
	require.NoError(t, err)

	return claim
}

// TestEvidenceIsWrittenAndReadBackOnTheRealSchema is the round trip that proves
// the migration and the query agree about the columns.
func TestEvidenceIsWrittenAndReadBackOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	claim := openClaim(ctx, t, svc)

	for _, upload := range []string{"upl_a", "upl_b", "upl_c"} {
		_, err := svc.AttachClaimEvidence(ctx, claim.ID,
			service.AttachClaimEvidenceInput{UploadID: upload, Caption: "shot of " + upload})
		require.NoError(t, err)
	}

	evidence, err := svc.ListClaimEvidence(ctx, claim.ID)
	require.NoError(t, err)
	require.Len(t, evidence, 3)

	assert.Equal(t, "upl_a", evidence[0].UploadID, "oldest first")
	assert.Equal(t, "upl_c", evidence[2].UploadID)
	assert.Equal(t, "shot of upl_a", evidence[0].Caption)
	assert.Equal(t, claim.ID, evidence[0].OrderClaimID)
	assert.False(t, evidence[0].CreatedAt.IsZero(), "the moment comes from the database")
}

// TestTheSchemaKeepsOneFileFromBeingEvidenceTwice is the constraint, not the
// service.
//
// Nothing in Go looks for the duplicate: the insert is sent and the UNIQUE
// index refuses it. Were the index dropped, this is the only test that would
// notice — the service's own suite would keep passing on a fake that checks.
func TestTheSchemaKeepsOneFileFromBeingEvidenceTwice(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	claim := openClaim(ctx, t, svc)

	_, err := svc.AttachClaimEvidence(ctx, claim.ID,
		service.AttachClaimEvidenceInput{UploadID: "upl_same"})
	require.NoError(t, err)

	_, err = svc.AttachClaimEvidence(ctx, claim.ID,
		service.AttachClaimEvidenceInput{UploadID: "upl_same"})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "got %v", err)
}

// TestTheSameFileIsEvidenceOnTwoClaims is the other side of that index.
//
// The uniqueness is of the PAIR. One parcel photograph can be the evidence of
// the damage claim and of the missing-item claim beside it, and an index on the
// upload alone would have refused the second one.
func TestTheSameFileIsEvidenceOnTwoClaims(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	first := openClaim(ctx, t, svc)
	second := openClaim(ctx, t, svc)

	_, err := svc.AttachClaimEvidence(ctx, first.ID,
		service.AttachClaimEvidenceInput{UploadID: "upl_shared"})
	require.NoError(t, err)

	_, err = svc.AttachClaimEvidence(ctx, second.ID,
		service.AttachClaimEvidenceInput{UploadID: "upl_shared"})
	require.NoError(t, err)
}

// TestEvidenceOnAClaimThatIsNotThereIsRefusedByTheSchemaToo is the belt beside
// the braces, and it goes AROUND the service to find it.
//
// The service looks the claim up first, for a better message, so calling it
// would only prove that the lookup is there. The write is sent to the store
// directly: what refuses it is the foreign key, and that is what makes the
// answer true when a claim is deleted between the lookup and the insert.
//
// The constraint's name is the part that can rot. It does not end in
// "_order_id_fkey" like every other child of an order, so the suffix that
// classifies those does not match it and the answer would be a 500 — which is
// how this table was built the first time.
func TestEvidenceOnAClaimThatIsNotThereIsRefusedByTheSchemaToo(t *testing.T) {
	ctx := context.Background()
	store := repository.New(testPool.Pool())

	_, err := store.CreateClaimEvidence(ctx, models.ClaimEvidence{
		ID:           models.NewClaimEvidenceID(),
		OrderClaimID: "claim_never_existed",
		UploadID:     "upl_a",
	})

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "got %v", err)
}

// TestEvidenceThatNamesNoFileIsRefusedBySchemaToo is the same shape for the
// CHECK: the service refuses the empty id, and the column refuses it as well.
func TestEvidenceThatNamesNoFileIsRefusedBySchemaToo(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	claim := openClaim(ctx, t, svc)

	store := repository.New(testPool.Pool())

	_, err := store.CreateClaimEvidence(ctx, models.ClaimEvidence{
		ID:           models.NewClaimEvidenceID(),
		OrderClaimID: claim.ID,
		UploadID:     "",
	})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "got %v", err)
}

// TestDetachingEvidenceRemovesOneBinding proves the delete is by id.
func TestDetachingEvidenceRemovesOneBinding(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	claim := openClaim(ctx, t, svc)

	first, err := svc.AttachClaimEvidence(ctx, claim.ID,
		service.AttachClaimEvidenceInput{UploadID: "upl_a"})
	require.NoError(t, err)
	_, err = svc.AttachClaimEvidence(ctx, claim.ID,
		service.AttachClaimEvidenceInput{UploadID: "upl_b"})
	require.NoError(t, err)

	require.NoError(t, svc.DetachClaimEvidence(ctx, first.ID))

	evidence, err := svc.ListClaimEvidence(ctx, claim.ID)
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	assert.Equal(t, "upl_b", evidence[0].UploadID)

	// The same file may be bound again: the binding was removed, and nothing
	// remembers that it once existed.
	_, err = svc.AttachClaimEvidence(ctx, claim.ID,
		service.AttachClaimEvidenceInput{UploadID: "upl_a"})
	require.NoError(t, err)
}

// TestDetachingEvidenceThatIsNotThereIsNotFound keeps a delete of nothing from
// reading as a success.
func TestDetachingEvidenceThatIsNotThereIsNotFound(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	err := svc.DetachClaimEvidence(ctx, "clev_never_existed")

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "got %v", err)
}
