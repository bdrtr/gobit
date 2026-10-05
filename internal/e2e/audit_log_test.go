//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// auditLogPath is the audit log's reader, bound by the composition root rather
// than by a module (ADR 0037).
const auditLogPath = "/admin/v1/audit-log"

// TestAnAdminWriteIsReadBackFromTheAuditLog is the audit ring end to end: an
// admin write made with a secret key is recorded by the server's guard stack,
// and a key holding the audit privilege alone reads it back from the endpoint
// the server binds.
//
// The ground once built a guard stack with no audit ring and mounted no reader,
// so no scenario could read a row back (D254).
func TestAnAdminWriteIsReadBackFromTheAuditLog(t *testing.T) {
	write, err := adminRequestWithBody(http.MethodPost, "/admin/v1/sales-channels", map[string]string{
		"name": fmt.Sprintf("e2e audited channel %d", fixtureCounter.Add(1)),
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, write.Code, write.Body.String())

	reader := apiKeyHolding(t, "audit:read")
	rec := adminRequest(t, http.MethodGet,
		auditLogPath+"?"+url.Values{"path": {"/admin/v1/sales-channels"}}.Encode(), "Bearer "+reader)
	require.Equal(t, http.StatusOK, rec.Code, "a key holding audit:read reads the log; body: %s", rec.Body.String())

	var page struct {
		Data []struct {
			ActorID   string `json:"actor_id"`
			ActorKind string `json:"actor_kind"`
			Method    string `json:"method"`
			Path      string `json:"path"`
			Status    int    `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page), rec.Body.String())

	found := false
	for _, row := range page.Data {
		if row.Method == http.MethodPost && row.Path == "/admin/v1/sales-channels" && row.Status == http.StatusCreated {
			found = true
			assert.NotEmpty(t, row.ActorID, "the row names who wrote")
		}
	}
	assert.True(t, found, "the write is in the audit log; rows: %s", rec.Body.String())
}
