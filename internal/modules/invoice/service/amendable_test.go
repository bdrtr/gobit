package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// TestTheAmendableReadNamesTheRefundsOfEachRow: each row of the amendable read
// names the live refunds that gave back on it, once each and in the order they
// were issued (ADR 0432); a canceled refund and a charge are not among them,
// and a row nothing gave back on names none.
func TestTheAmendableReadNamesTheRefundsOfEachRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(newFakeRepo())
	sale := issuedSale(t, svc)
	first, err := svc.Issue(ctx, refundOf(sale, 1200, 200))
	require.NoError(t, err)
	voided, err := svc.Issue(ctx, refundOf(sale, 120, 20))
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, voided.ID, service.MoveInput{To: models.StatusCanceled, Reason: "void"})
	require.NoError(t, err)
	_, err = svc.Issue(ctx, chargeOf(sale, 120, 20, false))
	require.NoError(t, err)
	twice := refundOf(sale, 240, 40)
	twice.Lines = append(twice.Lines, twice.Lines[0])
	twice.Lines[0].Total, twice.Lines[0].TaxTotal, twice.Lines[0].UnitPrice, twice.Lines[0].Subtotal = 120, 20, 100, 100
	twice.Lines[1].Total, twice.Lines[1].TaxTotal, twice.Lines[1].UnitPrice, twice.Lines[1].Subtotal = 120, 20, 100, 100
	second, err := svc.Issue(ctx, twice)
	require.NoError(t, err)

	raw, err := service.NewInterop(svc).AmendableJSON(ctx, sale.ID)
	require.NoError(t, err)
	var read struct {
		Rows []struct {
			GivenBackBy []struct {
				ID     string `json:"id"`
				Number string `json:"number"`
			} `json:"given_back_by"`
		} `json:"rows"`
	}
	require.NoError(t, json.Unmarshal(raw, &read))
	require.Len(t, read.Rows, 1)
	named := read.Rows[0].GivenBackBy
	require.Len(t, named, 2, "the live refunds, the one naming the row twice once: %s", raw)
	assert.Equal(t, [2]string{first.ID, first.Number}, [2]string{named[0].ID, named[0].Number})
	assert.Equal(t, [2]string{second.ID, second.Number}, [2]string{named[1].ID, named[1].Number})
}
