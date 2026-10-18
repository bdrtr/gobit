package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// reviseKeyScopesRequest is the body that changes a secret key's privileges
// (ADR 0455).
type reviseKeyScopesRequest struct {
	// ScopesRead are the privileges the caller read on the key, as a set; the
	// write is refused when the key holds others by then.
	ScopesRead []string `json:"scopes_read"`
	// Scopes are the privileges the key holds from its next request.
	Scopes []string `json:"scopes"`
}

// adminReviseAPIKeyScopes writes a secret key's privileges from the ones the
// caller read (PUT /admin/v1/api-keys/{id}/scopes).
//
// The key's secret is unchanged, so every deployment holding it gains or loses
// the privileges on its next request; the audit ring records the write and its
// caller. A privilege the caller lacks is refused as on creation (ADR 0455).
func (h *Handler) adminReviseAPIKeyScopes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req reviseKeyScopesRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	key, err := h.svc.ReviseAPIKeyScopes(ctx, pathParam(r, paramID), req.ScopesRead, req.Scopes)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toAPIKeyDTO(key))
}
