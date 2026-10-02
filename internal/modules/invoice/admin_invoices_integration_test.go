//go:build integration

package invoice_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// TestThePanelListsTheInvoicesOnTheRealSchema is ADR 0343 against a real
// PostgreSQL: a status lists only the documents in it, a rejected one with
// why, and no status lists every one, with how many there are.
func TestThePanelListsTheInvoicesOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	surface := service.NewAdminSurface(svc)

	kept, err := svc.Issue(ctx, issueFor("ZZL"))
	require.NoError(t, err)
	rejected, err := svc.Issue(ctx, issueFor("ZZL"))
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, rejected.ID, service.MoveInput{To: models.StatusSent})
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, rejected.ID, service.MoveInput{To: models.StatusRejected, Reason: "wrong tax number"})
	require.NoError(t, err)

	type row struct {
		ID           string `json:"id"`
		Number       string `json:"number"`
		Status       string `json:"status"`
		StatusReason string `json:"status_reason"`
		BuyerName    string `json:"buyer_name"`
		Total        int64  `json:"total"`
	}
	list := func(status string) (map[string]row, []string, int64) {
		t.Helper()

		raw, total, err := surface.InvoicesJSON(ctx, status, service.MaxLimit, 0)
		require.NoError(t, err)
		var rows []row
		require.NoError(t, json.Unmarshal(raw, &rows))
		byID, statuses := map[string]row{}, []string{}
		for _, r := range rows {
			byID[r.ID] = r
			statuses = append(statuses, r.Status)
		}
		assert.GreaterOrEqual(t, total, int64(len(rows)))

		return byID, statuses, total
	}

	byID, statuses, _ := list("rejected")
	assert.NotContains(t, byID, kept.ID, "an issued document is not a rejected one")
	for _, status := range statuses {
		assert.Equal(t, "rejected", status)
	}
	assert.Equal(t, row{
		ID: rejected.ID, Number: rejected.Number, Status: "rejected", StatusReason: "wrong tax number",
		BuyerName: rejected.Buyer.Name, Total: rejected.Total,
	}, byID[rejected.ID])

	byID, _, total := list("")
	assert.Contains(t, byID, kept.ID)
	assert.Contains(t, byID, rejected.ID)
	assert.GreaterOrEqual(t, total, int64(2))
}
