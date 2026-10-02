//go:build integration

package invoice_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// TestThePanelMovesAnInvoiceOnTheRealSchema is ADR 0344 against a real
// PostgreSQL: the page reads the document with its rows as they were
// stored, and a move from a status the document is no longer in is refused
// and writes nothing, while one from the status it is in is written.
func TestThePanelMovesAnInvoiceOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	surface := service.NewAdminSurface(svc)
	issued, err := svc.Issue(ctx, issueFor("ZZM"))
	require.NoError(t, err)

	raw, err := surface.InvoiceJSON(ctx, issued.ID)
	require.NoError(t, err)
	var document struct {
		Status string `json:"status"`
		Total  int64  `json:"total"`
		Lines  []struct {
			Description string `json:"description"`
			Total       int64  `json:"total"`
		} `json:"lines"`
		Moves []string `json:"moves"`
	}
	require.NoError(t, json.Unmarshal(raw, &document))
	assert.Equal(t, "issued", document.Status)
	assert.Equal(t, issued.Total, document.Total)
	require.Len(t, document.Lines, len(issued.Lines))
	assert.Equal(t, issued.Lines[0].Description, document.Lines[0].Description)
	assert.Equal(t, issued.Lines[0].Total, document.Lines[0].Total)
	assert.Equal(t, []string{"sent", "canceled"}, document.Moves)

	require.NoError(t, surface.MoveInvoice(ctx, issued.ID, "issued", "sent", ""))
	err = surface.MoveInvoice(ctx, issued.ID, "issued", "canceled", "a duplicate")
	require.Error(t, err)
	assert.Equal(t, service.CodeStatusMoved, errors.CodeOf(err), "%v", err)
	stored, err := svc.GetInvoice(ctx, issued.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusSent, stored.Status, "the refused move wrote nothing")
	assert.Empty(t, stored.StatusReason)

	require.NoError(t, surface.MoveInvoice(ctx, issued.ID, "sent", "canceled", "a duplicate"))
	stored, err = svc.GetInvoice(ctx, issued.ID)
	require.NoError(t, err)
	assert.Equal(t, "canceled|a duplicate", string(stored.Status)+"|"+stored.StatusReason)
	_, err = surface.InvoiceJSON(ctx, "inv_missing")
	assert.True(t, errors.IsNotFound(err), "%v", err)
}
