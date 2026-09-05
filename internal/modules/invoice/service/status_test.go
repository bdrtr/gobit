package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// TestTheTransitionTableIsWhatItSays pins every edge and every non-edge.
//
// The table is small enough to write out completely, and writing it out is the
// point: each of these is a decision, and a decision that is only implied by
// code is one nobody can disagree with on purpose.
func TestTheTransitionTableIsWhatItSays(t *testing.T) {
	t.Parallel()

	every := []models.Status{
		models.StatusIssued, models.StatusSent, models.StatusAccepted,
		models.StatusRejected, models.StatusCanceled,
	}

	allowed := map[models.Status]map[models.Status]bool{
		models.StatusIssued:   {models.StatusSent: true, models.StatusCanceled: true},
		models.StatusSent:     {models.StatusAccepted: true, models.StatusRejected: true, models.StatusCanceled: true},
		models.StatusAccepted: {models.StatusCanceled: true},
		models.StatusRejected: {},
		models.StatusCanceled: {},
	}

	for _, from := range every {
		for _, to := range every {
			assert.Equal(t, allowed[from][to], from.CanMoveTo(to),
				"the move %s -> %s", from, to)
		}
	}
}

// TestARejectedDocumentIsNotCanceled holds an edge that looks like an oversight.
//
// A rejected document never took effect, so there is nothing to withdraw.
// Letting it be canceled would make the two states say the same thing in the
// record, and an auditor reading "canceled" could no longer tell a withdrawn
// sale from one the receiving side refused.
func TestARejectedDocumentIsNotCanceled(t *testing.T) {
	t.Parallel()

	assert.False(t, models.StatusRejected.CanMoveTo(models.StatusCanceled))
	assert.True(t, models.StatusAccepted.CanMoveTo(models.StatusCanceled),
		"an ACCEPTED sale can still be withdrawn; that is an ordinary event in a shop")
}

// TestAnIllegalMoveIsRefused covers the service's own guard.
func TestAnIllegalMoveIsRefused(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.seed(issuedInvoice("inv_1", models.StatusRejected))

	_, err := newService(repo).MoveStatus(context.Background(), "inv_1", service.MoveInput{
		To: models.StatusAccepted,
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeTransition, errors.CodeOf(err))
}

// TestARejectionAndACancellationNeedAReason is the one thing a person later has
// to account for.
func TestARejectionAndACancellationNeedAReason(t *testing.T) {
	t.Parallel()

	tests := map[models.Status]models.Status{
		models.StatusCanceled: models.StatusIssued,
		models.StatusRejected: models.StatusSent,
	}

	for to, from := range tests {
		t.Run(to.String(), func(t *testing.T) {
			t.Parallel()

			repo := newFakeRepo()
			repo.seed(issuedInvoice("inv_1", from))
			svc := newService(repo)

			_, err := svc.MoveStatus(context.Background(), "inv_1", service.MoveInput{To: to})
			require.Error(t, err, "a %s without a reason has to be refused", to)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

			moved, err := svc.MoveStatus(context.Background(), "inv_1", service.MoveInput{
				To: to, Reason: "the customer withdrew the order",
			})
			require.NoError(t, err)
			assert.Equal(t, to, moved.Status)
			assert.Equal(t, "the customer withdrew the order", moved.StatusReason)
		})
	}
}

// TestSendingRecordsTheProviderAndItsIdentifier is what makes a transmission
// traceable afterwards.
func TestSendingRecordsTheProviderAndItsIdentifier(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.seed(issuedInvoice("inv_1", models.StatusIssued))

	sent, err := newService(repo).MoveStatus(context.Background(), "inv_1", service.MoveInput{
		To:         models.StatusSent,
		ProviderID: "manual",
		ExternalID: "ETTN-1234",
	})
	require.NoError(t, err)

	assert.Equal(t, models.StatusSent, sent.Status)
	assert.Equal(t, "manual", sent.ProviderID)
	assert.Equal(t, "ETTN-1234", sent.ExternalID)
}
