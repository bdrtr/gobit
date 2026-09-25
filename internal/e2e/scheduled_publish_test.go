//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/jobs/scheduledpublish"
)

// TestAScheduledDraftGoesLiveWhenTheJobFindsItDue is the gate ADRs 0177 and 0179
// rest on, through the production endpoints and the job the binary registers.
//
// A draft is scheduled over the admin API; until its moment the storefront does
// not have it, and the admin surface shows the moment. When the moment has come,
// one pass of the real job publishes it, and the storefront serves it — without
// ever writing the moment into a storefront body.
func TestAScheduledDraftGoesLiveWhenTheJobFindsItDue(t *testing.T) {
	ctx := t.Context()
	handle := fmt.Sprintf("scheduled-%d", fixtureCounter.Add(1))

	created, err := adminRequestWithBody(http.MethodPost, "/admin/v1/products", map[string]any{
		"handle": handle, "title": "Scheduled launch", "status": "draft",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, created.Code, "body: %s", created.Body.String())
	productID, ok := storefrontData(t, created)["id"].(string)
	require.True(t, ok, "body: %s", created.Body.String())

	moment := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	scheduled, err := adminRequestWithBody(http.MethodPut, "/admin/v1/products/"+productID+"/schedule",
		map[string]any{"publish_at": moment.Format(time.RFC3339)})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, scheduled.Code, "body: %s", scheduled.Body.String())
	record := storefrontData(t, scheduled)
	assert.Equal(t, "draft", record["status"], "a scheduled product is still a draft")
	assert.Equal(t, moment.Format(time.RFC3339), record["publish_at"], "the admin surface shows the moment")

	storePath := catalogPath(testChannelID, "/products/"+handle)
	before := storefrontRequest(t, http.MethodGet, storePath, "")
	assert.Equal(t, http.StatusNotFound, before.Code, "before its moment the storefront does not have it")

	// The moment passes. Moving it into the past is how the test makes time go
	// by; the constraint allows it, the product being a draft.
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE product SET publish_at = now() - interval '1 second' WHERE id = $1`, productID)
	require.NoError(t, err)

	require.NoError(t, scheduledpublish.Definition(productSvc, nil).Run(ctx),
		"one pass of the job the binary registers")

	after := storefrontRequest(t, http.MethodGet, storePath, "")
	require.Equal(t, http.StatusOK, after.Code, "the pass published it; body: %s", after.Body.String())
	assert.NotContains(t, after.Body.String(), "publish_at",
		"a storefront body never carries a schedule")

	read, err := adminRequestWithBody(http.MethodGet, "/admin/v1/products/"+productID, nil)
	require.NoError(t, err)
	record = storefrontData(t, read)
	assert.Equal(t, "published", record["status"])
	assert.NotContains(t, record, "publish_at", "the moment is spent")

	// And it leaves (ADR 0179): the live product is scheduled to be archived,
	// the moment passes, and one pass takes it off the storefront.
	leave := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	scheduled, err = adminRequestWithBody(http.MethodPut, "/admin/v1/products/"+productID+"/schedule",
		map[string]any{"archive_at": leave.Format(time.RFC3339)})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, scheduled.Code, "body: %s", scheduled.Body.String())
	assert.Equal(t, leave.Format(time.RFC3339), storefrontData(t, scheduled)["archive_at"])

	still := storefrontRequest(t, http.MethodGet, storePath, "")
	require.Equal(t, http.StatusOK, still.Code, "until its moment it stays on the storefront")
	assert.NotContains(t, still.Body.String(), "archive_at", "a storefront body never says when a product goes")

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE product SET archive_at = now() - interval '1 second' WHERE id = $1`, productID)
	require.NoError(t, err)
	require.NoError(t, scheduledpublish.Definition(productSvc, nil).Run(ctx))

	gone := storefrontRequest(t, http.MethodGet, storePath, "")
	assert.Equal(t, http.StatusNotFound, gone.Code, "the pass archived it; body: %s", gone.Body.String())
	read, err = adminRequestWithBody(http.MethodGet, "/admin/v1/products/"+productID, nil)
	require.NoError(t, err)
	assert.Equal(t, "archived", storefrontData(t, read)["status"])
}
