package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// panelRow is one delivery as the panel's surface sends it.
type panelRow struct {
	ID         string `json:"id"`
	Template   string `json:"template"`
	Reference  string `json:"reference"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	Resendable bool   `json:"resendable"`
}

func panelRows(t *testing.T, surface *service.AdminSurface, status, reference string) (rows []panelRow, total int64) {
	t.Helper()

	raw, total, err := surface.DeliveriesJSON(context.Background(), status, reference, 25, 0)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &rows))

	return rows, total
}

// TestThePanelListsTheDeliveriesAnOperatorMaySendAgain is ADR 0317: the
// surface lists by status and by order, says which delivery the operator may
// send again — a failed order mail, not another module's message —
// and carries the provider's reason.
func TestThePanelListsTheDeliveriesAnOperatorMaySendAgain(t *testing.T) {
	svc, store, prov, failed := failedConfirmation(t)
	surface := service.NewAdminSurface(svc)
	require.Error(t, svc.Notify(context.Background(), service.NotifyInput{
		Template: "invitation", Channel: "email", Reference: "inv_1", To: "staff@example.com",
	}), "the provider still refuses")

	rows, total := panelRows(t, surface, "failed", "")
	require.Len(t, rows, 2)
	assert.Equal(t, int64(2), total)
	byTemplate := map[string]panelRow{}
	for _, row := range rows {
		byTemplate[row.Template] = row
	}
	confirmation := byTemplate[service.TemplateOrderPlaced]
	assert.Equal(t, failed.ID, confirmation.ID)
	assert.True(t, confirmation.Resendable, "a failed confirmation is sent again here")
	assert.Contains(t, confirmation.Error, "the mail server did not answer")
	assert.False(t, byTemplate["invitation"].Resendable, "another module's message is sent again by that module")

	_, _, err := surface.DeliveriesJSON(context.Background(), "failed", "", 1, 1)
	require.NoError(t, err)
	assert.Equal(t, [2]int64{1, 1}, [2]int64{store.listed.Limit, store.listed.Offset},
		"the panel's page reaches the store")

	sent, _ := panelRows(t, surface, "sent", "")
	assert.Empty(t, sent)
	mine, total := panelRows(t, surface, "", "order_01H")
	require.Len(t, mine, 1, "an order's deliveries, in every status")
	assert.Equal(t, int64(1), total)

	prov.err = nil
	outcome, err := surface.ResendDelivery(context.Background(), failed.ID)
	require.NoError(t, err)
	assert.Equal(t, string(models.DeliverySent), outcome)
	again, _ := panelRows(t, surface, "sent", "")
	require.Len(t, again, 1)
	assert.False(t, again[0].Resendable, "a sent confirmation is not sent again")

	_, _, err = surface.DeliveriesJSON(context.Background(), "lost", "", 25, 0)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestAResendThePanelPressesThatFailsAgainIsAnOutcome: the provider refusing
// again is reported as the status the record was left in, with its reason on
// the record, while a delivery that may not be sent again is refused.
func TestAResendThePanelPressesThatFailsAgainIsAnOutcome(t *testing.T) {
	svc, _, _, failed := failedConfirmation(t)
	surface := service.NewAdminSurface(svc)

	outcome, err := surface.ResendDelivery(context.Background(), failed.ID)
	require.NoError(t, err)
	assert.Equal(t, string(models.DeliveryFailed), outcome)

	_, err = surface.ResendDelivery(context.Background(), "ndel_missing")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "an unknown delivery: %v", err)

	var unset *service.AdminSurface
	_, err = unset.ResendDelivery(context.Background(), failed.ID)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
}
