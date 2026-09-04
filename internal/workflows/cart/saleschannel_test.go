package cart

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/query"
)

// Sales channel IDs used in the tests.
const (
	testChannelA = "sc_a"
	testChannelB = "sc_b"
)

// storefrontContext produces a context carrying a publishable key principal bound to
// the given channels.
//
// In production the principal is placed by corehttp.RequireStore; the reason it is
// placed by hand here is that what is under test is the workflow READING FROM THE
// PRINCIPAL — who puts the principal where is the HTTP layer's job and is proven in
// the end-to-end test (see internal/e2e/channel_cart_test.go).
func storefrontContext(channels []string) context.Context {
	return corehttp.WithPrincipal(context.Background(), corehttp.Principal{
		ID:   "apk_test",
		Kind: "api_key",
		// The scope list is EMPTY: a publishable key carries no scopes, and channel
		// scoping is NOT a permission check but the scope of the principal itself.
		SalesChannelIDs: channels,
	})
}

// catalogQuery returns the ONE variant query that went to the fake catalog.
func catalogQuery(t *testing.T, h *harness) query.GraphSpec {
	t.Helper()

	for _, spec := range h.catalog.specs {
		if spec.Entity == EntityVariant {
			return spec
		}
	}

	t.Fatalf("no variant query reached the catalog; queries seen: %v", h.catalog.specs)
	return query.GraphSpec{}
}

// addLine adds a line to the cart; it returns the line that was written and the
// workflow's error.
//
// The write record is returned too because half the assertions in this file are about
// the line NOT BEING WRITTEN; seeing the error is not enough — a workflow that returns
// an error while still writing the line would also pass the test.
func addLine(t *testing.T, h *harness, ctx context.Context, variantID string) (*addedLine, error) {
	t.Helper()

	serveSnapshot(h.carts,
		snapshotOf(0, nil, nil),
		snapshotOf(1, []SnapshotItem{{ID: testLineA, VariantID: variantID, Quantity: 1}}, nil),
	)
	seen := recordAddLine(h.carts, testLineA)

	_, err := h.wf.AddLineItem(ctx, AddLineItemInput{
		CartID:    testCartID,
		VariantID: variantID,
		Quantity:  1,
	})
	return seen, err
}

// TestTheCatalogQueryReadsChannelsFromThePrincipal verifies how three principal states
// are reflected into the catalog query.
//
// The three states must be the SAME as the ones on the read surface
// (see saleschannel.go and product/graph.SalesChannelIDsFromContext); a write path that
// behaves differently leaves a hole in the scoping on one of the surfaces. What is
// tested here is not the behavior itself but the value PUT INTO the query: the side
// that enforces the rule is the product module, and asking it the right question is
// this workflow's only responsibility.
func TestTheCatalogQueryReadsChannelsFromThePrincipal(t *testing.T) {
	cases := map[string]struct {
		ctx       func() context.Context
		hasFilter bool
		channels  []string
	}{
		"no principal -> NO filter": {
			ctx:       context.Background,
			hasFilter: false,
		},
		"principal without channels -> EMPTY SET": {
			ctx:       func() context.Context { return storefrontContext(nil) },
			hasFilter: true,
			channels:  []string{},
		},
		"principal with channels -> those channels": {
			ctx:       func() context.Context { return storefrontContext([]string{testChannelA, testChannelB}) },
			hasFilter: true,
			channels:  []string{testChannelA, testChannelB},
		},
	}

	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			_, err := addLine(t, h, tt.ctx(), testVariantA)
			require.NoError(t, err)

			spec := catalogQuery(t, h)
			raw, inQuery := spec.Filters[FilterSalesChannelIDs]

			if !tt.hasFilter {
				assert.False(t, inQuery,
					"a request without a principal must NEVER get a channel filter; if it did, "+
						"a setup without auth could not find any variant for the cart")
				return
			}

			require.True(t, inQuery,
				"a request with a principal must get a channel filter; without it the write path stays unscoped")
			assert.Equal(t, tt.channels, raw,
				"the filter must carry the principal's channels EXACTLY")
			assert.NotNil(t, raw,
				"the empty set is NOT nil: nil means 'no filtering' and would open the catalog "+
					"of every channel to a key with no channels")
		})
	}
}

// TestAnOutOfScopeVariantCannotEnterTheCart verifies that for an out-of-scope variant
// the line is NEVER written.
//
// If the workflow carried on and wrote a titleless line when the catalog returned no
// record, or swallowed the error, scoping would be reduced to a mere diagnostic
// message.
func TestAnOutOfScopeVariantCannotEnterTheCart(t *testing.T) {
	h := newHarness(t)
	h.catalog.scopedOut = map[string]bool{testVariantA: true}

	seen, err := addLine(t, h, storefrontContext([]string{testChannelB}), testVariantA)

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err),
		"an out-of-scope variant must be in the NOT FOUND class; error: %v", err)
	assert.Equal(t, 0, seen.calls,
		"no cart line may be WRITTEN for an out-of-scope variant")
}

// TestAnOutOfScopeVariantDoesNotRevealItsExistence verifies that the error for an
// out-of-scope variant is INDISTINGUISHABLE from the error for a variant that does not
// exist at all.
//
// If it were distinguishable, the hiding would be punctured: a competitor arriving with
// any publishable key in hand would learn, by trying variant IDs, which of them are sold
// in ANOTHER channel. The read surface makes the same decision and the same assertion
// lives there too (see e2e TestAHiddenProductDoesNotRevealItselfViaTheErrorCode).
//
// The comparison covers both the CODE and the MESSAGE; here the two may be identical,
// because both messages only echo the requested ID.
func TestAnOutOfScopeVariantDoesNotRevealItsExistence(t *testing.T) {
	ctx := storefrontContext([]string{testChannelB})

	hidden := newHarness(t)
	hidden.catalog.scopedOut = map[string]bool{testVariantA: true}
	_, hiddenErr := addLine(t, hidden, ctx, testVariantA)
	require.Error(t, hiddenErr)

	missing := newHarness(t)
	delete(missing.catalog.titles, testVariantA)
	_, missingErr := addLine(t, missing, ctx, testVariantA)
	require.Error(t, missingErr)

	assert.Equal(t, errors.CodeOf(missingErr), errors.CodeOf(hiddenErr),
		"an out-of-scope variant and a nonexistent variant must return the SAME error code")
	assert.Equal(t, errors.KindOf(missingErr), errors.KindOf(hiddenErr),
		"the error CLASS of the two cases must match too; the class changes the client's decision")
	assert.Equal(t, missingErr.Error(), hiddenErr.Error(),
		"the messages must not diverge either; the day they do, the difference becomes a leak channel")
}

// TestAVariantInItsOwnChannelEntersTheCart verifies that the scope check is not a gate
// that rejects EVERYTHING.
//
// Without this assertion the other tests are worthless: a change that breaks the
// catalog read entirely would also pass the "an out-of-scope variant cannot be added"
// test.
func TestAVariantInItsOwnChannelEntersTheCart(t *testing.T) {
	h := newHarness(t)
	// The catalog COUNTS this variant as within the request's scope; that is the only
	// difference from the out-of-scope scenario.
	seen, err := addLine(t, h, storefrontContext([]string{testChannelA}), testVariantA)

	require.NoError(t, err)
	assert.Equal(t, 1, seen.calls, "a variant in its own channel must enter the cart")
	assert.Equal(t, "Red T-Shirt / M", seen.title,
		"the title must still be copied from the catalog; the scope check must not break the read")
}
