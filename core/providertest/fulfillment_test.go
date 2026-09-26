package providertest_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/core/providertest"
)

// carrier is an in-memory shipping provider; each flag breaks one rule.
type carrier struct {
	shipments map[string]string // idempotency key -> shipment id
	canceled  map[string]bool
	opened    int

	echo          func(*provider.Address) any // returned as Data
	newEveryTime  bool
	cancelTwiceNo bool
	failCreate    bool
	emptyID       bool
}

func newCarrier() *carrier {
	return &carrier{shipments: map[string]string{}, canceled: map[string]bool{}}
}

func (c *carrier) ID() string { return "carrier-under-test" }

func (c *carrier) Quote(context.Context, provider.QuoteInput) (provider.ShippingQuote, error) {
	return provider.ShippingQuote{}, nil
}

func (c *carrier) Create(_ context.Context, in provider.CreateFulfillmentInput) (provider.Fulfillment, error) {
	if c.failCreate {
		return provider.Fulfillment{}, errors.Unavailable("carrier_down", "the carrier is down")
	}
	id, seen := c.shipments[in.IdempotencyKey]
	if !seen || c.newEveryTime {
		c.opened++
		id = "ship_" + strconv.Itoa(c.opened)
		c.shipments[in.IdempotencyKey] = id
	}
	if c.emptyID {
		id = ""
	}
	data := json.RawMessage(`{"label":"L-1"}`)
	if c.echo != nil {
		raw, err := json.Marshal(c.echo(in.Destination))
		if err != nil {
			return provider.Fulfillment{}, err
		}
		data = raw
	}

	return provider.Fulfillment{ID: id, Status: provider.FulfillmentPending, Data: data}, nil
}

func (c *carrier) Cancel(_ context.Context, id string) error {
	if c.canceled[id] && c.cancelTwiceNo {
		return errors.Conflict("already_canceled", "shipment %s is already canceled", id)
	}
	c.canceled[id] = true

	return nil
}

// TestAnObedientCarrierIsNotReported is the suite passing the provider it
// should.
func TestAnObedientCarrierIsNotReported(t *testing.T) {
	t.Parallel()

	got := &recorder{}
	providertest.Fulfillment(got, newCarrier(), provider.CreateFulfillmentInput{
		Reference: "ful_1", OptionID: "so_1",
	})

	if len(got.failures) != 0 {
		t.Fatalf("an obedient carrier was reported: %v", got.failures)
	}
}

// TestEveryShippingRuleFires is the suite's own mutation proof for
// [providertest.Fulfillment]: each carrier breaks one rule, and the suite has
// to report that one.
func TestEveryShippingRuleFires(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		breaks func(*carrier)
		phrase string
	}{
		{"the street echoed", func(c *carrier) {
			c.echo = func(a *provider.Address) any { return map[string]string{"to": a.Address1} }
		}, "returned the destination in its data"},
		{"the whole destination echoed", func(c *carrier) {
			c.echo = func(a *provider.Address) any { return a }
		}, "returned the destination in its data"},
		{"the metadata echoed", func(c *carrier) {
			c.echo = func(a *provider.Address) any { return a.Metadata }
		}, "returned the destination in its data"},
		{"a second shipment on a retry", func(c *carrier) { c.newEveryTime = true }, "under one idempotency key"},
		{"a second cancel refused", func(c *carrier) { c.cancelTwiceNo = true }, "Cancel failed the second time"},
		{"no shipment opened", func(c *carrier) { c.failCreate = true }, "Create failed, so no shipment rule"},
		{"an empty identifier", func(c *carrier) { c.emptyID = true }, "empty ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := newCarrier()
			tc.breaks(c)
			got := &recorder{}
			providertest.Fulfillment(got, c, provider.CreateFulfillmentInput{
				Reference: "ful_1", OptionID: "so_1",
			})

			if !got.saw(tc.phrase) {
				t.Fatalf("the suite did not report %q; it reported %v", tc.name, got.failures)
			}
		})
	}
}

// TestTheSuiteNamesTheCountryAsItIs keeps the country a real code: a carrier
// that validates it would refuse a marker, and the suite would report a
// failure to create rather than anything about the provider.
func TestTheSuiteNamesTheCountryAsItIs(t *testing.T) {
	t.Parallel()

	var country string
	c := newCarrier()
	c.echo = func(a *provider.Address) any {
		country = a.CountryCode

		return map[string]string{"country": a.CountryCode}
	}
	got := &recorder{}
	providertest.Fulfillment(got, c, provider.CreateFulfillmentInput{Reference: "ful_1", OptionID: "so_1"})

	if country != "TR" {
		t.Fatalf("the destination's country was %q", country)
	}
	if len(got.failures) != 0 {
		t.Fatalf("returning the country alone was reported: %v", got.failures)
	}
}
