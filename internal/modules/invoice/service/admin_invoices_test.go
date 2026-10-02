package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// TestThePanelListsTheInvoices is ADR 0343: the surface lists a page of the
// documents in the status asked for, or in every status, with how many there
// are, each with what the Invoices screen prints, and refuses a status there
// is not.
func TestThePanelListsTheInvoices(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	surface := service.NewAdminSurface(newService(repo))
	issued := time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC)
	rejected := issuedInvoice("inv_1", models.StatusRejected)
	rejected.Buyer.Name, rejected.StatusReason, rejected.IssuedAt = "Ada Lovelace", "wrong tax number", issued
	repo.listResult, repo.listCount = []models.Invoice{rejected}, 7

	raw, total, err := surface.InvoicesJSON(context.Background(), "rejected", 25, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(7), total)
	assert.JSONEq(t, `[{"id":"inv_1","number":"GBT2026000000001","kind":"sale","status":"rejected",
		"status_reason":"wrong tax number","buyer_name":"Ada Lovelace","currency_code":"TRY","total":2400,
		"issued_at":"2026-10-02T09:30:00Z"}]`, string(raw))
	require.NotNil(t, repo.listFilter.Status)
	assert.Equal(t, "rejected", *repo.listFilter.Status)
	assert.Equal(t, int64(26), repo.listFilter.Limit, "one more than the page, to know there is another")
	assert.Equal(t, int64(50), repo.listFilter.Offset)
	assert.Nil(t, repo.listFilter.Kind, "every kind")

	repo.listResult, repo.listCount = nil, 0
	raw, _, err = surface.InvoicesJSON(context.Background(), "", 25, 0)
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, string(raw))
	assert.Nil(t, repo.listFilter.Status, "no status is every status")

	_, _, err = surface.InvoicesJSON(context.Background(), "paid", 25, 0)
	assert.True(t, errors.IsInvalid(err), "a status there is not: %v", err)
}
