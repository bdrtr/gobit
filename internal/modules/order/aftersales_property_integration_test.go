//go:build integration

package order_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// modelReturn is the model's copy of one return record.
type modelReturn struct {
	id     string
	units  map[string]int64
	status models.ReturnStatus
}

// TestNoLineGivesBackMoreThanItSold is ADR 0249 on an order's after-sales
// ceiling: any sequence of returns opened, received and canceled and of units
// written off leaves each line's units asked back or written off where a model
// of the calls says, never above what the line sold, and each call is refused
// exactly when the model says, by the code the contract names. A canceled
// return gives its units back to the ceiling; a received one keeps them.
func TestNoLineGivesBackMoreThanItSold(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	rapid.Check(t, func(rt *rapid.T) {
		in := validInput()
		in.Items = nil
		in.Subtotal, in.TaxTotal, in.ShippingTotal = 0, 0, 0
		bought := map[string]int64{}
		for i := range rapid.IntRange(1, 3).Draw(rt, "lines") {
			quantity := rapid.Int64Range(1, 4).Draw(rt, "bought")
			in.Items = append(in.Items, service.CreateOrderItemInput{
				VariantID: fmt.Sprintf("variant_P%d", i), Title: "Property line",
				Quantity: quantity, UnitPrice: 1000, Subtotal: 1000 * quantity, Total: 1000 * quantity,
			})
			in.Subtotal += 1000 * quantity
		}
		in.Total = in.Subtotal
		order, err := svc.CreateOrder(ctx, in)
		require.NoError(rt, err)
		detail, err := svc.GetOrder(ctx, order.ID)
		require.NoError(rt, err)
		lineIDs := make([]string, 0, len(detail.Items))
		for i := range detail.Items {
			lineIDs = append(lineIDs, detail.Items[i].ID)
			bought[detail.Items[i].ID] = detail.Items[i].Quantity
		}

		var returns []*modelReturn
		written := map[string]int64{}
		spokenFor := func(line string) int64 {
			total := written[line]
			for _, r := range returns {
				if r.status != models.ReturnCanceled {
					total += r.units[line]
				}
			}
			return total
		}
		refused := func(rt *rapid.T, err error, code string) {
			require.Error(rt, err)
			require.True(rt, errors.HasKind(err, errors.KindConflict), "%v", err)
			require.Equal(rt, code, errors.CodeOf(err), "%v", err)
		}
		pick := func(rt *rapid.T) *modelReturn {
			if len(returns) == 0 {
				rt.Skip("no return yet")
			}
			return returns[rapid.IntRange(0, len(returns)-1).Draw(rt, "return")]
		}

		rt.Repeat(map[string]func(*rapid.T){
			"open a return": func(rt *rapid.T) {
				lines := rapid.SliceOfNDistinct(rapid.SampledFrom(lineIDs), 1, len(lineIDs), rapid.ID[string]).Draw(rt, "returned lines")
				units := map[string]int64{}
				request := service.CreateReturnInput{OrderID: order.ID, Reason: "property"}
				fits := true
				for _, line := range lines {
					units[line] = rapid.Int64Range(1, 5).Draw(rt, "returned")
					request.Lines = append(request.Lines, service.ReturnLineInput{OrderLineItemID: line, Quantity: units[line]})
					fits = fits && spokenFor(line)+units[line] <= bought[line]
				}
				created, err := svc.CreateReturn(ctx, request)
				if !fits {
					refused(rt, err, service.CodeReturnQuantityExceeded)
					return
				}
				require.NoError(rt, err)
				returns = append(returns, &modelReturn{id: created.ID, units: units, status: models.ReturnRequested})
			},
			"receive a return": func(rt *rapid.T) {
				r := pick(rt)
				_, err := svc.ReceiveReturn(ctx, r.id, "sloc_PROPERTY")
				if r.status == models.ReturnCanceled {
					refused(rt, err, service.CodeAfterSalesTransition)
					return
				}
				require.NoError(rt, err)
				r.status = models.ReturnReceived
			},
			"cancel a return": func(rt *rapid.T) {
				r := pick(rt)
				_, err := svc.CancelReturn(ctx, r.id)
				if r.status == models.ReturnReceived {
					refused(rt, err, service.CodeAfterSalesTransition)
					return
				}
				require.NoError(rt, err)
				r.status = models.ReturnCanceled
			},
			"write units off": func(rt *rapid.T) {
				line := rapid.SampledFrom(lineIDs).Draw(rt, "written-off line")
				quantity := rapid.Int64Range(1, 5).Draw(rt, "written off")
				_, err := svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
					OrderLineItemID: line, Quantity: quantity, Reason: "property",
				})
				if spokenFor(line)+quantity > bought[line] {
					refused(rt, err, service.CodeCancelQuantityExceeded)
					return
				}
				require.NoError(rt, err)
				written[line] += quantity
			},
			"": func(rt *rapid.T) {
				// The records, read back: the units of every return that was
				// not canceled and every write-off, per line.
				stored := map[string]int64{}
				listed, _, err := svc.ListReturns(ctx, order.ID, service.Page{Limit: 100})
				require.NoError(rt, err)
				for i := range listed {
					if listed[i].Status == models.ReturnCanceled {
						continue
					}
					raw, err := svc.ReturnDetailJSON(ctx, listed[i].ID)
					require.NoError(rt, err)
					var detail struct {
						Lines []struct {
							OrderLineItemID string `json:"order_line_item_id"`
							Quantity        int64  `json:"quantity"`
						} `json:"lines"`
					}
					require.NoError(rt, json.Unmarshal(raw, &detail))
					for _, line := range detail.Lines {
						require.NotEmpty(rt, line.OrderLineItemID)
						stored[line.OrderLineItemID] += line.Quantity
					}
				}
				cancellations, err := svc.ListLineCancellations(ctx, order.ID)
				require.NoError(rt, err)
				for i := range cancellations {
					stored[cancellations[i].OrderLineItemID] += cancellations[i].Quantity
				}
				for _, line := range lineIDs {
					require.Equal(rt, spokenFor(line), stored[line], "line %s", line)
					require.LessOrEqual(rt, stored[line], bought[line], "line %s gives back no more than it sold", line)
				}
			},
		})
	})
}
