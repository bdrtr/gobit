//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAProductPageReadsItsNeighbors is the gate ADR 0180 rests on, through the
// production router and a publishable key.
//
// The operator relates a draft and a published product to a published one over
// the admin API. The storefront reads the published neighbor and not the
// draft; once the draft is published it appears where the operator put it.
func TestAProductPageReadsItsNeighbors(t *testing.T) {
	n := fixtureCounter.Add(1)
	create := func(handle, status string) string {
		t.Helper()

		rec, err := adminRequestWithBody(http.MethodPost, "/admin/v1/products", map[string]any{
			"handle": handle, "title": handle, "status": status,
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
		id, ok := storefrontData(t, rec)["id"].(string)
		require.True(t, ok, "body: %s", rec.Body.String())
		return id
	}
	shirtHandle := fmt.Sprintf("neighbor-shirt-%d", n)
	beltHandle := fmt.Sprintf("neighbor-belt-%d", n)
	launchHandle := fmt.Sprintf("neighbor-launch-%d", n)
	shirt := create(shirtHandle, "published")
	belt := create(beltHandle, "published")
	launch := create(launchHandle, "draft")

	set, err := adminRequestWithBody(http.MethodPut, "/admin/v1/products/"+shirt+"/relations/cross_sell",
		map[string]any{"product_ids": []string{launch, belt}})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, set.Code, "body: %s", set.Body.String())
	assert.Equal(t, []any{launch, belt}, storefrontData(t, set)["cross_sell"])

	read := func() []string {
		t.Helper()

		rec := storefrontRequest(t, http.MethodGet,
			catalogPath(testChannelID, "/products/"+shirtHandle+"/related?type=cross_sell"), "")
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

		var body struct {
			Data []struct {
				Handle string `json:"handle"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "body: %s", rec.Body.String())
		handles := make([]string, 0, len(body.Data))
		for _, p := range body.Data {
			handles = append(handles, p.Handle)
		}
		return handles
	}

	assert.Equal(t, []string{beltHandle}, read(), "the draft lined up is not shown before its launch")

	published, err := adminRequestWithBody(http.MethodPatch, "/admin/v1/products/"+launch,
		map[string]any{"status": "published"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, published.Code, "body: %s", published.Body.String())

	assert.Equal(t, []string{launchHandle, beltHandle}, read(), "after the launch it is where the operator put it")
}
