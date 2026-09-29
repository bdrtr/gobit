//go:build integration

package order_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestABundleLineKeepsWhatItWasMadeOf is ADR 0235's column on the real schema:
// a line that sold a bundle keeps its components in the order given, the
// interop answer the put-back acts read carries them, and a line of its own
// reads none.
func TestABundleLineKeepsWhatItWasMadeOf(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	in := validInput()
	in.CartID = "cart_BUNDLE"
	box := in.Items[0]
	box.VariantID = "variant_box"
	box.Components = []service.CreateOrderLineComponentInput{
		{VariantID: "variant_towel", Quantity: 1},
		{VariantID: "variant_soap", Quantity: 2},
	}
	plain := in.Items[0]
	in.Items = []service.CreateOrderItemInput{box, plain}
	in.Subtotal += plain.Subtotal
	in.TaxTotal += plain.TaxTotal
	in.Total += plain.Total

	ord, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 2)
	assert.Equal(t, []models.OrderLineComponent{
		{VariantID: "variant_towel", Quantity: 1}, {VariantID: "variant_soap", Quantity: 2},
	}, detail.Items[0].Components)
	assert.Nil(t, detail.Items[1].Components, "a line of its own is made of nothing")

	raw, err := service.NewInterop(svc).DispatchableLinesJSON(ctx, ord.ID)
	require.NoError(t, err)
	var lines []struct {
		LineItemID string          `json:"line_item_id"`
		Components json.RawMessage `json:"components"`
	}
	require.NoError(t, json.Unmarshal(raw, &lines))
	require.Len(t, lines, 2)
	assert.JSONEq(t, `[{"variant_id":"variant_towel","quantity":1},{"variant_id":"variant_soap","quantity":2}]`,
		string(lines[0].Components))
	assert.Empty(t, lines[1].Components, "the key is absent on a line of its own")

	var stored string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT components::text FROM order_line_items WHERE id = $1`, detail.Items[1].ID).Scan(&stored))
	assert.Equal(t, "[]", stored, "none is an empty array, never NULL")
}

// TestAnOrderLinesComponentsAreAnArray is the column's CHECK, witnessed in raw
// SQL: the service writes an array, and the database refuses anything else.
func TestAnOrderLinesComponentsAreAnArray(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	in := validInput()
	in.CartID = "cart_BUNDLE_CHECK"
	ord, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE order_line_items SET components = '{"variant_id":"v","quantity":1}'::jsonb WHERE id = $1`,
		detail.Items[0].ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `check constraint "order_line_items_components_is_array"`)
}

// replacedBox opens an order that sold a box of a towel and two soaps beside
// a line of its own, and records a replacement of one of each against a claim.
func replacedBox(ctx context.Context, t *testing.T, svc *service.Service, cartID string) service.ReplacementRecord {
	t.Helper()

	in := validInput()
	in.CartID = cartID
	box := in.Items[0]
	box.VariantID = "variant_box"
	box.Components = []service.CreateOrderLineComponentInput{
		{VariantID: "variant_towel", Quantity: 1},
		{VariantID: "variant_soap", Quantity: 2},
	}
	plain := in.Items[0]
	in.Items = []service.CreateOrderItemInput{box, plain}
	in.Subtotal += plain.Subtotal
	in.TaxTotal += plain.TaxTotal
	in.Total += plain.Total

	ord, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 2)
	claim, err := svc.CreateClaim(ctx, service.CreateClaimInput{OrderID: ord.ID, Type: models.ClaimReplace})
	require.NoError(t, err)

	request := requestOf(claim.ID, detail.Items[0].ID, 1)
	request.Lines = append(request.Lines, service.ReplacementLineInput{OrderLineItemID: detail.Items[1].ID, Quantity: 1})
	record, err := svc.CreateReplacement(ctx, request)
	require.NoError(t, err)

	return record
}

// TestAReplacedBoxKeepsItsPartsOnTheRealSchema is ADR 0238's table: the item
// that replaces a box is written with the line's parts in their rank, each
// part takes its own promise, and the record is sent once both hold one.
func TestAReplacedBoxKeepsItsPartsOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	record := replacedBox(ctx, t, svc, "cart_REPLACED_BOX")
	box := record.Items[0].ID

	read, err := svc.GetReplacement(ctx, record.ID)
	require.NoError(t, err)
	require.Len(t, read.Items, 2)
	assert.Equal(t, []models.ReplacementItemPart{
		{VariantID: "variant_towel", Quantity: 1}, {VariantID: "variant_soap", Quantity: 2},
	}, read.Items[0].Parts, "the parts come back in the order the box was sold with")
	assert.Nil(t, read.Items[1].Parts)

	require.NoError(t, svc.RecordReplacementReservation(ctx, record.ID, box, "variant_soap", "invres_soap"))
	_, err = svc.MarkReplacementDispatched(ctx, record.ID, "ful_box")
	require.Error(t, err, "the towel and the other line hold nothing yet")

	require.NoError(t, svc.RecordReplacementReservation(ctx, record.ID, box, "variant_towel", "invres_towel"))
	require.NoError(t, svc.RecordReplacementReservation(ctx, record.ID, record.Items[1].ID, "variant_A", "invres_line"))
	_, err = svc.MarkReplacementDispatched(ctx, record.ID, "ful_box")
	require.NoError(t, err)

	rows, err := testPool.Pool().Query(ctx,
		`SELECT variant_id, rank, reservation_id FROM order_replacement_item_parts
		 WHERE order_replacement_item_id = $1 ORDER BY rank`, box)
	require.NoError(t, err)
	defer rows.Close()
	var stored []string
	for rows.Next() {
		var variant, reservation string
		var rank int32
		require.NoError(t, rows.Scan(&variant, &rank, &reservation))
		stored = append(stored, fmt.Sprintf("%d:%s:%s", rank, variant, reservation))
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"0:variant_towel:invres_towel", "1:variant_soap:invres_soap"}, stored)

	var own *string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT reservation_id FROM order_replacement_items WHERE id = $1`, box).Scan(&own))
	assert.Nil(t, own, "a box is held part by part, never as itself")
}

// TestAReplacementPartIsHeldToAShape witnesses the table's constraints in raw
// SQL, below the service that never writes such a row.
func TestAReplacementPartIsHeldToAShape(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	box := replacedBox(ctx, t, svc, "cart_REPLACED_BOX_SHAPE").Items[0].ID

	for constraint, statement := range map[string]string{
		"order_replacement_item_parts_quantity": `UPDATE order_replacement_item_parts SET quantity = 0
			WHERE order_replacement_item_id = $1 AND rank = 0`,
		"order_replacement_item_parts_variant_not_blank": `INSERT INTO order_replacement_item_parts
			(order_replacement_item_id, variant_id, quantity, rank) VALUES ($1, '', 1, 5)`,
		"order_replacement_item_parts_reservation_not_blank": `UPDATE order_replacement_item_parts
			SET reservation_id = '' WHERE order_replacement_item_id = $1 AND rank = 0`,
	} {
		t.Run(constraint, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, statement, box)
			require.Error(t, err)
			assert.Contains(t, err.Error(), `check constraint "`+constraint+`"`)
		})
	}

	_, err := testPool.Pool().Exec(ctx, `INSERT INTO order_replacement_item_parts
		(order_replacement_item_id, variant_id, quantity, rank) VALUES ($1, 'variant_other', 1, 0)`, box)
	require.Error(t, err, "two parts cannot share a rank")
	assert.Contains(t, err.Error(), "order_replacement_item_parts_rank_unique")
}
