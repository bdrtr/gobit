//go:build integration

package product_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file proves the two surfaces the module opens to the outside against
// the REAL core: the "product.interop" read surface and the catalog events.
//
// Both have to be here. The channel filter is in SQL and a fake repository
// cannot verify a condition it writes itself; and that the events really go
// to the bus in the container is seen only once the module is Registered —
// the service's own tests wire the bus themselves, so they miss that wiring.

// storeProductReader defines the "product.interop" surface from the CONSUMER's
// point of view.
//
// A plugin (plugins/**) CANNOT import product; it resolves the surface exactly
// like this, through a narrow interface it defines in its own package and
// through the NAME in the container. Writing the interface out again here is
// the test itself: if the concrete type drifts from the signature, resolution
// fails not at compile time but AT RUN TIME, and this test moves that moment
// out of production and into the integration suite.
type storeProductReader interface {
	StoreProductsByIDsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
}

// interopIDs returns the product IDs in the surface's response IN ORDER.
func interopIDs(t *testing.T, body json.RawMessage) []string {
	t.Helper()

	var out struct {
		Products []struct {
			ID string `json:"id"`
		} `json:"products"`
	}
	require.NoError(t, json.Unmarshal(body, &out), "response: %s", string(body))

	ids := make([]string, 0, len(out.Products))
	for _, p := range out.Products {
		ids = append(ids, p.ID)
	}
	return ids
}

// TestInteropChannelFilterIsAppliedInRealSQL proves that the surface applies
// the sales channel filter with the REAL query.
//
// The claim is this: a product assigned to another channel is NOT RETURNED in
// the response even when its ID is asked for explicitly. That is the only
// proof that the lookup has not become a bypass of channel filtering, and it
// can only be given against a real database — the filter is an EXISTS
// condition against the link table that core/link creates at run time.
func TestInteropChannelFilterIsAppliedInRealSQL(t *testing.T) {
	ctx := context.Background()
	sys := newSystem(t)

	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err)
	reader, err := container.Resolve[storeProductReader](sys.container, product.InteropName)
	require.NoError(t, err, "the surface must be resolvable under the name %q", product.InteropName)

	ours := createStoreProduct(t, svc, "interop-ours")
	theirs := createStoreProduct(t, svc, "interop-other")
	unassigned := createStoreProduct(t, svc, "interop-unassigned")

	require.NoError(t, svc.AddProductSalesChannel(ctx, ours.ID, "sc_interop_ours"))
	require.NoError(t, svc.AddProductSalesChannel(ctx, theirs.ID, "sc_interop_other"))

	request := fmt.Sprintf(`{"ids": [%q, %q, %q], "sales_channel_ids": ["sc_interop_ours"]}`,
		theirs.ID, ours.ID, unassigned.ID)
	body, err := reader.StoreProductsByIDsJSON(ctx, json.RawMessage(request))
	require.NoError(t, err)

	assert.Equal(t, []string{ours.ID, unassigned.ID}, interopIDs(t, body),
		"the other channel's product must not be returned; the rest must keep the request's order")

	// A request without channels does not filter: the meaning is the same as
	// in the storefront listing.
	all := fmt.Sprintf(`{"ids": [%q, %q]}`, theirs.ID, ours.ID)
	body, err = reader.StoreProductsByIDsJSON(ctx, json.RawMessage(all))
	require.NoError(t, err)
	assert.Equal(t, []string{theirs.ID, ours.ID}, interopIDs(t, body))
}

// TestInteropMakesTheSameDecisionAsTheStoreEndpoint verifies that the surface
// and the HTTP storefront endpoint make the same channel decision for the same
// product.
//
// This is the proof that the visibility rule has a SINGLE definition: if one
// day the rule changes on only one of the two paths, this test fails.
func TestInteropMakesTheSameDecisionAsTheStoreEndpoint(t *testing.T) {
	ctx := context.Background()
	sys := newSystem(t)

	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err)
	reader, err := container.Resolve[storeProductReader](sys.container, product.InteropName)
	require.NoError(t, err)

	hidden := createStoreProduct(t, svc, "interop-hidden")
	require.NoError(t, svc.AddProductSalesChannel(ctx, hidden.ID, "sc_interop_hidden"))

	// The storefront single endpoint returns NotFound for another channel's ID.
	rec := sys.storeChannelRequest(t, "/store/v1/products/"+hidden.ID, []string{"sc_interop_another"})
	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())

	// The surface makes the same decision: the record is not in the response
	// at all.
	request := fmt.Sprintf(`{"ids": [%q], "sales_channel_ids": ["sc_interop_another"]}`, hidden.ID)
	body, err := reader.StoreProductsByIDsJSON(ctx, json.RawMessage(request))
	require.NoError(t, err)
	assert.Empty(t, interopIDs(t, body),
		"a product hidden in the storefront must not be returned by the surface either")
}

// TestCatalogEventsReachTheEventBus verifies that once the module is
// Registered, the catalog events go to the REAL bus in the container.
//
// The service's own tests wire the bus by hand; only this test sees that the
// wiring is established during Register. Its breaking produces no error at all
// — the events are simply never published — and that is why it is pinned by a
// test.
func TestCatalogEventsReachTheEventBus(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	links := link.New(testPool, nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", links))
	require.NoError(t, c.Provide("core.query", query.New(links, c, nil)))

	bus := eventbus.NewInMemory(nil)
	require.NoError(t, c.Provide("core.eventbus", bus))

	// The subscription is set up BEFORE the module comes up: a subscriber that
	// attaches later cannot see the events published before it (the in-memory
	// backend keeps no history, it delivers AT MOST ONCE).
	ledger := &eventLedger{}
	for _, name := range []string{
		service.EventProductCreated,
		service.EventProductUpdated,
		service.EventProductDeleted,
	} {
		require.NoError(t, bus.Subscribe(name, ledger.record))
	}

	mod := product.New(product.Options{})
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, product.ServiceName)
	require.NoError(t, err)

	prod, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:  "Event product",
		Handle: uniqueHandle("event-product"),
		Status: productmodels.StatusDraft,
	})
	require.NoError(t, err)
	_, err = svc.UpdateProduct(ctx, prod.ID, service.UpdateProductInput{
		Status: statusPtr(productmodels.StatusPublished),
	})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteProduct(ctx, prod.ID))

	created := ledger.waitFor(t, service.EventProductCreated, prod.ID)
	assert.Equal(t, productmodels.StatusDraft.String(), created.Data[service.EventFieldStatus],
		"the event must carry the status the product had AT THE MOMENT IT WAS WRITTEN")

	updated := ledger.waitFor(t, service.EventProductUpdated, prod.ID)
	assert.Equal(t, productmodels.StatusPublished.String(), updated.Data[service.EventFieldStatus])

	deleted := ledger.waitFor(t, service.EventProductDeleted, prod.ID)
	assert.NotContains(t, deleted.Data, service.EventFieldStatus,
		"the delete event does not carry the status")
}

// TestRegisterFailsWithoutEventBus verifies that startup STOPS while the event
// bus is not registered.
//
// The decision is deliberate: had "let the events be skipped silently" been
// chosen, the catalog would keep working, no error would show up and the gap
// would be noticed only once the search index went stale — that is, in
// production. Like core.db and core.link, core.eventbus is a core service
// registered BEFORE the modules; its absence is not a deployment shape but a
// setup error.
func TestRegisterFailsWithoutEventBus(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	links := link.New(testPool, nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", links))
	require.NoError(t, c.Provide("core.query", query.New(links, c, nil)))
	// core.eventbus is DELIBERATELY not registered.

	err := product.New(product.Options{}).Register(ctx, c)
	require.Error(t, err, "Register must not succeed without the bus")
	assert.Equal(t, "product_module_setup_failed", coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "core.eventbus",
		"the error message must name the missing service; a setup error must be understood in the first second")
}

// eventLedger is the test side's record of the catalog events that land on the
// bus.
//
// The type is safe for concurrent use: the in-memory backend runs every
// handler in a separate goroutine, and reading and writing share the same
// lock.
type eventLedger struct {
	mu     sync.Mutex
	events []eventbus.Event
}

// record writes the event into the ledger.
func (d *eventLedger) record(_ context.Context, e eventbus.Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, e)
	return nil
}

// waitFor waits for and returns the SINGLE event belonging to the given name
// and product.
//
// Waiting is MANDATORY: Publish does NOT WAIT for the handlers, so the event
// may not be visible in the ledger yet even though the write has returned.
// Uniqueness is checked as well — two events for one write means the
// subscribers do the work twice.
func (d *eventLedger) waitFor(t *testing.T, name, productID string) eventbus.Event {
	t.Helper()

	var found []eventbus.Event
	require.Eventually(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()

		found = found[:0]
		for i := range d.events {
			id, _ := d.events[i].Data[service.EventFieldProductID].(string)
			if d.events[i].Name == name && id == productID {
				found = append(found, d.events[i])
			}
		}
		return len(found) > 0
	}, 5*time.Second, 20*time.Millisecond,
		"the %q event must be published for product %s; if it is not, subscribers such as the search "+
			"index stay UNAWARE of the catalog and the gap produces no error at all", name, productID)

	require.Len(t, found, 1, "the %q event must be published ONCE", name)
	return found[0]
}

// createStoreProduct creates a product that is visible in the storefront
// (published).
func createStoreProduct(t *testing.T, svc *service.Service, handle string) productmodels.Product {
	t.Helper()

	prod, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:  handle,
		Handle: uniqueHandle(handle),
		Status: productmodels.StatusPublished,
	})
	require.NoError(t, err)
	return prod
}

// statusPtr returns the address of the given status.
func statusPtr(s productmodels.Status) *productmodels.Status { return &s }
