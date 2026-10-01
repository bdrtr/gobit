//go:build integration

package order_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestThePanelSurfaceKeepsWhatIsTypedBesideALine is ADR 0279 over the real
// schema: a return the panel opens keeps each line's part of the refund, and a
// replacement it opens keeps more of an order line and a variant the order
// never sold as two items, the second naming no line.
func TestThePanelSurfaceKeepsWhatIsTypedBesideALine(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	bus := eventbus.NewInMemory(nil)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = bus.Shutdown(shutdownCtx)
	})
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.eventbus", bus))
	require.NoError(t, c.Provide("core.query", query.New(link.New(testPool, nil), c, nil)))
	// The catalog answers the replacement's variant as goods of their own,
	// made of no parts.
	require.NoError(t, c.Provide(service.CatalogEntityVariant+".query", plainVariants{}))
	require.NoError(t, order.New().Register(ctx, c))
	svc, err := container.Resolve[*service.Service](c, order.ServiceName)
	require.NoError(t, err)
	surface, err := container.Resolve[*order.AfterSalesSurface](c, order.AdminName)
	require.NoError(t, err)

	placed, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, placed.ID)
	require.NoError(t, err)
	lineID := detail.Items[0].ID

	returnID, err := surface.OpenReturn(ctx, placed.ID, []string{lineID}, []int64{2}, []int64{1_250}, 1_250,
		"two came back scratched")
	require.NoError(t, err)
	var lineRefund int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT refund_amount FROM order_return_items WHERE order_return_id = $1 AND order_line_item_id = $2`,
		returnID, lineID).Scan(&lineRefund))
	assert.Equal(t, int64(1_250), lineRefund, "the line's part of the refund is kept on its return line")

	claim, err := svc.CreateClaim(ctx, service.CreateClaimInput{OrderID: placed.ID, Type: models.ClaimReplace})
	require.NoError(t, err)
	replacementID, err := surface.OpenReplacement(ctx, claim.ID, "", []string{lineID}, []int64{1},
		[]string{"variant_ring_gold"}, []int64{1}, "so_standard", "sloc_main")
	require.NoError(t, err)

	record, err := svc.GetReplacement(ctx, replacementID)
	require.NoError(t, err)
	require.Len(t, record.Items, 2)
	byWhat := map[string]models.ReplacementItem{}
	for _, item := range record.Items {
		if item.OrderLineItemID != "" {
			byWhat["line"] = item
		} else {
			byWhat["variant"] = item
		}
	}
	assert.Equal(t, lineID, byWhat["line"].OrderLineItemID)
	assert.Equal(t, int64(1), byWhat["line"].Quantity)
	assert.Equal(t, "variant_ring_gold", byWhat["variant"].VariantID, "a variant the order never sold names no line")
	assert.Equal(t, int64(1), byWhat["variant"].Quantity)
}

// plainVariants is a catalog whose every variant is a plain product.
type plainVariants struct{}

func (plainVariants) Entity() string { return service.CatalogEntityVariant }

func (plainVariants) List(context.Context, query.ListOptions) ([]query.Record, error) {
	return nil, nil
}

func (plainVariants) FetchByIDs(_ context.Context, ids, _ []string) ([]query.Record, error) {
	out := make([]query.Record, 0, len(ids))
	for _, id := range ids {
		out = append(out, query.Record{query.IDField: id})
	}

	return out, nil
}

// TestThePanelSurfaceKeepsAClaimsEvidence is ADR 0325 against a real
// PostgreSQL: the surface binds an upload to a claim with its caption, lists
// the claim's evidence oldest first under the panel's JSON, removes one piece
// and leaves the other, and refuses a claim that is not there.
func TestThePanelSurfaceKeepsAClaimsEvidence(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	bus := eventbus.NewInMemory(nil)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = bus.Shutdown(shutdownCtx)
	})
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.eventbus", bus))
	require.NoError(t, c.Provide("core.query", query.New(link.New(testPool, nil), c, nil)))
	require.NoError(t, order.New().Register(ctx, c))
	svc, err := container.Resolve[*service.Service](c, order.ServiceName)
	require.NoError(t, err)
	surface, err := container.Resolve[*order.AfterSalesSurface](c, order.AdminName)
	require.NoError(t, err)

	placed, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	claim, err := svc.CreateClaim(ctx, service.CreateClaimInput{OrderID: placed.ID, Type: models.ClaimRefund})
	require.NoError(t, err)

	dent, err := surface.AttachClaimEvidence(ctx, claim.ID, "upl_dent", " the dent ")
	require.NoError(t, err)
	_, err = surface.AttachClaimEvidence(ctx, claim.ID, "upl_box", "")
	require.NoError(t, err)

	raw, err := surface.ClaimEvidenceJSON(ctx, claim.ID)
	require.NoError(t, err)
	var rows []struct {
		ID        string    `json:"id"`
		UploadID  string    `json:"upload_id"`
		Caption   string    `json:"caption"`
		CreatedAt time.Time `json:"created_at"`
	}
	require.NoError(t, json.Unmarshal(raw, &rows))
	require.Len(t, rows, 2)
	assert.Equal(t, []string{dent, "upl_dent", "the dent"}, []string{rows[0].ID, rows[0].UploadID, rows[0].Caption},
		"oldest first, the caption trimmed")
	assert.Equal(t, "upl_box", rows[1].UploadID)
	assert.False(t, rows[0].CreatedAt.IsZero())

	require.NoError(t, surface.DetachClaimEvidence(ctx, dent))
	raw, err = surface.ClaimEvidenceJSON(ctx, claim.ID)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &rows))
	require.Len(t, rows, 1, "the other piece stays")
	assert.Equal(t, "upl_box", rows[0].UploadID)

	_, err = surface.AttachClaimEvidence(ctx, "claim_missing", "upl_x", "")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a claim that is not there: %v", err)
}
