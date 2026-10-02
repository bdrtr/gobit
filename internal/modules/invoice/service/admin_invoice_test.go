package service_test

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

// TestThePanelReadsAnInvoice is ADR 0344: the surface returns the document
// with its parties, rows and totals and the statuses it may move to, in the
// order a document moves through them; a document there is not is not
// found.
func TestThePanelReadsAnInvoice(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	svc := newService(repo)
	surface := service.NewAdminSurface(svc)
	in := validIssue()
	in.Buyer = models.Party{Name: "Ada Lovelace", TaxNumber: "12345678901", Email: "ada@example.test",
		Address: "12 Main St", CountryCode: "TR"}
	issued, err := svc.Issue(context.Background(), in)
	require.NoError(t, err)

	raw, err := surface.InvoiceJSON(context.Background(), issued.ID)
	require.NoError(t, err)
	var document struct {
		Number string `json:"number"`
		Status string `json:"status"`
		Seller struct {
			Name      string `json:"name"`
			TaxNumber string `json:"tax_number"`
		} `json:"seller"`
		Buyer struct {
			Name        string `json:"name"`
			TaxNumber   string `json:"tax_number"`
			Email       string `json:"email"`
			Address     string `json:"address"`
			CountryCode string `json:"country_code"`
		} `json:"buyer"`
		Subtotal int64 `json:"subtotal"`
		TaxTotal int64 `json:"tax_total"`
		Total    int64 `json:"total"`
		Lines    []struct {
			Description string `json:"description"`
			Quantity    int64  `json:"quantity"`
			UnitPrice   int64  `json:"unit_price"`
			TaxRateBps  int32  `json:"tax_rate_bps"`
			TaxTotal    int64  `json:"tax_total"`
			Total       int64  `json:"total"`
		} `json:"lines"`
		Moves []string `json:"moves"`
	}
	require.NoError(t, json.Unmarshal(raw, &document))
	assert.Equal(t, issued.Number, document.Number)
	assert.Equal(t, "issued", document.Status)
	assert.Equal(t, "Gobit Shop|1234567890", document.Seller.Name+"|"+document.Seller.TaxNumber)
	assert.Equal(t, "Ada Lovelace|12345678901|ada@example.test|12 Main St|TR", document.Buyer.Name+"|"+
		document.Buyer.TaxNumber+"|"+document.Buyer.Email+"|"+document.Buyer.Address+"|"+document.Buyer.CountryCode)
	assert.Equal(t, []int64{2000, 400, 2400}, []int64{document.Subtotal, document.TaxTotal, document.Total})
	require.Len(t, document.Lines, 1)
	line := document.Lines[0]
	assert.Equal(t, "Red T-Shirt", line.Description)
	assert.Equal(t, []int64{2, 1000, 2000, 400, 2400},
		[]int64{line.Quantity, line.UnitPrice, int64(line.TaxRateBps), line.TaxTotal, line.Total})
	assert.Equal(t, []string{"sent", "canceled"}, document.Moves)

	_, err = surface.InvoiceJSON(context.Background(), "inv_missing")
	assert.True(t, errors.IsNotFound(err), "%v", err)
}

// TestThePanelMovesAnInvoiceFromTheStatusItRead is ADR 0344: the surface
// moves the document from the status the operator read it in, with why, and
// refuses with service.CodeStatusMoved when it moved since, writing nothing;
// a move the document may not make and a cancellation without a reason are
// refused as they are through the API.
func TestThePanelMovesAnInvoiceFromTheStatusItRead(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	svc := newService(repo)
	surface := service.NewAdminSurface(svc)
	issued, err := svc.Issue(context.Background(), validIssue())
	require.NoError(t, err)

	require.NoError(t, surface.MoveInvoice(context.Background(), issued.ID, "issued", "sent", ""))
	err = surface.MoveInvoice(context.Background(), issued.ID, "issued", "canceled", "a duplicate")
	require.Error(t, err)
	assert.Equal(t, service.CodeStatusMoved, errors.CodeOf(err), "read before it was sent: %v", err)
	stored, err := svc.GetInvoice(context.Background(), issued.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusSent, stored.Status, "nothing was written")

	err = surface.MoveInvoice(context.Background(), issued.ID, "sent", "canceled", " ")
	assert.True(t, errors.IsInvalid(err), "a cancellation says why: %v", err)
	err = surface.MoveInvoice(context.Background(), issued.ID, "sent", "issued", "")
	assert.Equal(t, service.CodeTransition, errors.CodeOf(err), "a move it may not make: %v", err)
	require.NoError(t, surface.MoveInvoice(context.Background(), issued.ID, "sent", "rejected", "wrong tax number"))
	stored, err = svc.GetInvoice(context.Background(), issued.ID)
	require.NoError(t, err)
	assert.Equal(t, "rejected|wrong tax number", string(stored.Status)+"|"+stored.StatusReason)

	_, err = svc.MoveStatus(context.Background(), issued.ID, service.MoveInput{To: models.StatusCanceled, Reason: "x"})
	assert.Equal(t, service.CodeTransition, errors.CodeOf(err),
		"a caller that names no status read is checked as before: %v", err)
}
