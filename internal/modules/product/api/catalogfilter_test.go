package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file covers what the STOREFRONT LISTING reads off its query string for
// the three filters ADR 0039, 0040 and 0041 decide, and the two combinations it
// refuses.
//
// It stops where the service begins. Whether a bracket is usable is
// service.PriceBracket.Validate's question, and it is asked there so that the
// GraphQL surface -- which sends the same bracket as an input object and never
// passes through this package -- refuses the same requests. What is HERE is the
// part only a query string has: three flat parameters standing for one nested
// object, and a tri-state boolean where an absent value is a third answer.

// TestTheThreeFiltersReachTheServiceAsGiven walks each parameter from the query
// string to the option the service receives.
//
// A parameter that is described and not read is the failure this repository
// keeps naming: the generated client offers it, the caller sends it, and the
// server silently ignores it. The arch audit binds the DOCUMENT to the reading;
// this binds the reading to the MEANING.
func TestTheThreeFiltersReachTheServiceAsGiven(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		query string
		want  func(*testing.T, service.StoreListOptions)
	}{
		{
			name:  "an option value arrives unfolded, because the fold is the service's",
			query: "?option_value=K%C4%B1rm%C4%B1z%C4%B1",
			want: func(t *testing.T, opts service.StoreListOptions) {
				require.NotNil(t, opts.OptionValue)
				// The handler must NOT fold: the fold lives in one place on the
				// way down (see service.Service.ListProducts) so that both read
				// surfaces get the same matching, and a second fold here would
				// be a second definition that can drift.
				assert.Equal(t, turkishRed, *opts.OptionValue)
			},
		},
		{
			name:  "in_stock=true is a filter",
			query: "?in_stock=true",
			want: func(t *testing.T, opts service.StoreListOptions) {
				require.NotNil(t, opts.InStock)
				assert.True(t, *opts.InStock)
			},
		},
		{
			name:  "in_stock=false is a DIFFERENT filter, not the absence of one",
			query: "?in_stock=false",
			want: func(t *testing.T, opts service.StoreListOptions) {
				require.NotNil(t, opts.InStock)
				assert.False(t, *opts.InStock)
			},
		},
		{
			name:  "an absent in_stock filters nothing",
			query: "",
			want: func(t *testing.T, opts service.StoreListOptions) {
				assert.Nil(t, opts.InStock,
					"nil is the third answer; collapsing it into false would hide every "+
						"sellable product from a request that asked for no filter")
				assert.Nil(t, opts.Price)
			},
		},
		{
			name:  "the three price parameters become one bracket",
			query: "?currency_code=try&min_price=1000&max_price=5000",
			want: func(t *testing.T, opts service.StoreListOptions) {
				require.NotNil(t, opts.Price)
				assert.Equal(t, "try", opts.Price.CurrencyCode,
					"the code travels as the client wrote it: the case is folded in the "+
						"service, once, so that the GraphQL input object -- which never "+
						"passes through here -- is compared the same way")
				require.NotNil(t, opts.Price.Min)
				require.NotNil(t, opts.Price.Max)
				assert.Equal(t, int64(1000), *opts.Price.Min)
				assert.Equal(t, int64(5000), *opts.Price.Max)
			},
		},
		{
			name:  "one bound alone leaves the other end open",
			query: "?currency_code=TRY&max_price=5000",
			want: func(t *testing.T, opts service.StoreListOptions) {
				require.NotNil(t, opts.Price)
				assert.Nil(t, opts.Price.Min, "an absent bound is an OPEN end, not zero")
				require.NotNil(t, opts.Price.Max)
				assert.Equal(t, int64(5000), *opts.Price.Max)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, got := listedChannels(t, storeProductsPath+tc.query, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			tc.want(t, got)
		})
	}
}

// TestAnUnreadablePriceParameterIsRefused covers the half of the price filter
// that is genuinely this layer's.
//
// A bound that is not a number cannot become an int64 at all, so it is refused
// where the parsing happens. Everything the bracket has to SATISFY -- a bound
// without a currency, a currency without a bound, a negative amount, a reversed
// pair -- is judged in service.PriceBracket.Validate instead, so that the
// GraphQL surface refuses the same requests without a second copy of the rules;
// those cases are covered where they live, and that the refusal really travels
// to a client as a 422 is proved end to end in internal/e2e.
//
// The tests here run against a FAKE service, so an assertion about the
// service's judgement made in this file would be an assertion about the fake.
func TestAnUnreadablePriceParameterIsRefused(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"a lower bound that is not a number":  "?currency_code=TRY&min_price=cheap",
		"an upper bound that is not a number": "?currency_code=TRY&max_price=expensive",
	}

	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec, _ := listedChannels(t, storeProductsPath+query, nil)
			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code,
				"an unreadable amount is the client's mistake and must be said out loud: %s",
				rec.Body.String())
		})
	}
}

// TestAnUnreadableInStockValueIsRefused keeps the parameter from falling back.
//
// "in_stock=yes" reading as no filter at all hands back the whole catalog, and a
// client cannot tell that from a shop where everything is in stock.
func TestAnUnreadableInStockValueIsRefused(t *testing.T) {
	t.Parallel()

	rec, _ := listedChannels(t, storeProductsPath+"?in_stock=yes", nil)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestTheCounterIsRefusedBesideAnEnrichedFilter covers the collision the
// endpoint documents.
//
// The count of an in-stock or price filtered catalog cannot be produced without
// enriching the whole catalog. Dropping the "count" field silently would leave a
// storefront that always sends with_count=true computing zero pages the first
// time somebody adds an in-stock toggle, so an EXPLICIT with_count=true is
// refused instead.
func TestTheCounterIsRefusedBesideAnEnrichedFilter(t *testing.T) {
	t.Parallel()

	refused := []string{
		"?in_stock=true&with_count=true",
		"?currency_code=TRY&max_price=5000&with_count=true",
	}
	for _, query := range refused {
		t.Run(query, func(t *testing.T) {
			t.Parallel()

			rec, _ := listedChannels(t, storeProductsPath+query, nil)
			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		})
	}

	served := map[string]string{
		"the counter switched off":    "?in_stock=true&with_count=false",
		"the counter never asked for": "?in_stock=true",
	}
	for name, query := range served {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec, got := listedChannels(t, storeProductsPath+query, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.True(t, got.SkipCount,
				"a filtered listing is not counted whichever way the client left the switch")
		})
	}
}

// TestTheStorefrontBodyCarriesTheInStockBadge is the badge's half of ADR 0040.
//
// The definition is the service's; what this pins is that the answer LEAVES the
// server, on the product and on the variant, under the name a client reads. A
// badge computed and never serialized is a definition with no consumer, which is
// the state gap A17 was filed against.
func TestTheStorefrontBodyCarriesTheInStockBadge(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		listStoreProducts: func(
			_ context.Context, _ service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			return service.ListResult[service.StoreProduct]{
				Items: []service.StoreProduct{{
					InStock: true,
					Variants: []service.StoreVariant{
						{InStock: true},
						{InStock: false},
					},
				}},
			}, nil
		},
	}

	rec := storeRequest(t, newRouter(catalog), storeProductsPath, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body struct {
		Data []struct {
			InStock  bool `json:"in_stock"`
			Variants []struct {
				InStock bool `json:"in_stock"`
			} `json:"variants"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	require.Len(t, body.Data[0].Variants, 2)

	assert.True(t, body.Data[0].InStock, "the product badge must be written")
	assert.True(t, body.Data[0].Variants[0].InStock)
	assert.False(t, body.Data[0].Variants[1].InStock,
		"the variant badges must be written per variant, not copied from the product")
}

// TestTheDescribedFiltersAreTheOnesTheHandlerReads is a spelling guard.
//
// The handler and the OpenAPI description spell each filter as a string literal
// in two files -- deliberately, because the arch audit that binds the two
// resolves a parameter name from a literal and reads a constant back as unknown.
// A literal spelled one way here and another way there would drop the parameter
// out of that audit's population entirely, so the pair is checked directly: the
// name is sent and the option it fills is asserted.
func TestTheDescribedFiltersAreTheOnesTheHandlerReads(t *testing.T) {
	t.Parallel()

	filled := map[string]func(service.StoreListOptions) bool{
		"option_value=red":  func(o service.StoreListOptions) bool { return o.OptionValue != nil },
		"in_stock=true":     func(o service.StoreListOptions) bool { return o.InStock != nil },
		"min_price=1":       func(o service.StoreListOptions) bool { return o.Price != nil },
		"max_price=1":       func(o service.StoreListOptions) bool { return o.Price != nil },
		"currency_code=TRY": func(o service.StoreListOptions) bool { return o.Price != nil },
	}

	for query, filledBy := range filled {
		t.Run(query, func(t *testing.T) {
			t.Parallel()

			// The price parameters are sent with a partner so the request is
			// usable; what is being checked is that the NAME lands, not that
			// the bracket is complete.
			target := fmt.Sprintf("%s?%s&currency_code=TRY&min_price=1", storeProductsPath, query)
			rec, got := listedChannels(t, target, nil)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.True(t, filledBy(got),
				"the handler must read the parameter the document describes under this name")
		})
	}
}

// turkishRed is one Turkish color name written without a Turkish letter in this
// file (ADR 0012).
//
// Spelled out: "K", the DOTLESS i (U+0131), "rm", the dotless i again, "z" and
// the dotless i once more. It is the value the query string above sends
// percent-encoded, and it is written here the same way the two other files
// covering it write it -- service/catalogfilter_test.go and
// filterbody_integration_test.go.
const turkishRed = "K\u0131rm\u0131z\u0131"
