package api

import (
	"net/http"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// --- identity -----------------------------------------------------------------

// adminLogin produces a session token from an email and a password
// (POST /admin/v1/auth/login).
//
// # THIS ENDPOINT IS UNPROTECTED
//
// It is the request that is going to be authenticated; it is only about to
// establish the identity. When corehttp.RequireAdmin is attached to the admin
// surface, [LoginPath] MUST BE LEFT OUT — if it is protected nobody can log in
// and the system locks itself out.
//
// Every failed attempt produces the SAME error and roughly the same duration;
// had a distinction been made, the response itself would have handed out the
// information "this email is registered" (see service.Service.Login).
func (h *Handler) adminLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req loginRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	// The plaintext password is converted to a plain string only here; the
	// conversion standing out is deliberate (see [secret]).
	token, expiresAt, err := h.svc.Login(ctx, req.Email, string(req.Password))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, itemEnvelope{Data: loginResponse{
		Token:     token,
		ExpiresAt: expiresAt,
		TokenType: "Bearer",
	}})
}

// adminWhoami returns the identity of the authenticated caller
// (GET /admin/v1/auth/me).
//
// The identity comes FROM THE CORE: corehttp.RequireAdmin puts it into the
// context. This endpoint is therefore also the proof that the protection is
// really attached — with no protection there is no identity either and a 401 is
// returned.
//
// # THIS ENDPOINT ASKS FOR NO SCOPE
//
// Identity is enough: the endpoint reads back the scopes the caller ALREADY
// holds. Had it asked for a scope, a caller without scope could not even learn
// what they are not entitled to, and could not see the reason for their 403 by
// looking at their own identity.
func (h *Handler) adminWhoami(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	principal, ok := corehttp.PrincipalFromContext(ctx)
	if !ok {
		corehttp.WriteError(ctx, w, coreerrors.Unauthorized(
			corehttp.CodeUnauthenticated, "authentication is required"))
		return
	}

	writeItem(w, r, http.StatusOK, principalResponse{
		ID:              principal.ID,
		Kind:            principal.Kind,
		Scopes:          orEmpty(principal.Scopes),
		SalesChannelIDs: principal.SalesChannelIDs,
	})
}

// adminLogout closes the caller's sessions (POST /admin/v1/auth/logout).
//
// # ALL OF THE CALLER'S SESSIONS DROP
//
// The endpoint closes not one device but all of the caller's sessions: an admin
// logging out from their phone has closed their session on the laptop too.
// Dropping a single device does not exist and cannot exist today either — that
// would take a jti-based deny list, that is, a new store read on every request
// (see service.Service.Logout).
//
// # THIS ENDPOINT ASKS FOR NO SCOPE
//
// Closing your own session is not a privilege. Had it asked for a scope, the
// token of an admin whose scope had been taken away could not be closed until
// it expired.
//
// # RESPONSE: 200 and a body, NOT 204
//
// A bodyless 204 would have been idiomatic but would have fallen short here:
// the caller believes "I logged out of this device" while the server has closed
// ALL OF THEM, and a status code cannot say that. The body states two things
// explicitly: that the revocation is WHOLESALE ([logoutResponse.AllSessions])
// and the MOMENT it rests on ([logoutResponse.RevokedAt]); the second lets the
// client see without trial and error that a token still in its hands is now
// invalid.
//
// If it is called with an API key the request is rejected with a typed error:
// a key has no session, and silently returning a 2xx would have left the
// illusion that the key had been closed (see service.Service.Logout).
func (h *Handler) adminLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// The identity comes FROM THE CORE; had who logs out been read from a value
	// the client declares in the body, the endpoint would have been the way to
	// close somebody else's session.
	principal, ok := corehttp.PrincipalFromContext(ctx)
	if !ok {
		corehttp.WriteError(ctx, w, coreerrors.Unauthorized(
			corehttp.CodeUnauthenticated, "authentication is required"))
		return
	}

	revokedAt, err := h.svc.Logout(ctx, principal.ID, principal.Kind)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	writeItem(w, r, http.StatusOK, logoutResponse{
		AllSessions: true,
		RevokedAt:   revokedAt,
	})
}

// --- users --------------------------------------------------------------------

// adminCreateUser creates a new admin user (POST /admin/v1/users).
//
// The password in the body may be left empty; the user is then created without
// a password and POST /admin/v1/users/{id}/password has to be called first
// before they can log in.
//
// The endpoint asks for [ScopeWrite]; furthermore the requested scopes CANNOT
// EXCEED the caller's own (see service.CreateUser).
func (h *Handler) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createUserRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	created, err := h.svc.CreateUser(ctx, toCreateUserInput(req), string(req.Password))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toUserDTO(created))
}

// adminListUsers lists the users, filtering and paging them
// (GET /admin/v1/users).
//
// Filters: email, scope.
func (h *Handler) adminListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := h.svc.ListUsers(ctx, service.ListUsersInput{
		Email:  stringParam(r, "email"),
		Scope:  stringParam(r, "scope"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toUserDTO)
}

// adminGetUser returns a single user (GET /admin/v1/users/{id}).
func (h *Handler) adminGetUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user, err := h.svc.GetUser(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toUserDTO(user))
}

// adminUpdateUser updates the given fields of the user
// (PUT /admin/v1/users/{id}).
//
// The endpoint asks for [ScopeWrite]; the scope list in the body CANNOT EXCEED
// the caller's own (see service.UpdateUser).
func (h *Handler) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req updateUserRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	updated, err := h.svc.UpdateUser(ctx, pathParam(r, paramID), toUpdateUserInput(req))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toUserDTO(updated))
}

// adminDeleteUser soft-deletes the user and their login credentials
// (DELETE /admin/v1/users/{id}).
func (h *Handler) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteUser(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// adminSetPassword assigns the user's password
// (POST /admin/v1/users/{id}/password).
//
// The endpoint is separate: had it been put into the same body as the profile
// update, it would have been possible for a request changing a name to reset
// the password by accident.
//
// The response HAS NO BODY (204): there is no point in writing anything
// password-related back.
func (h *Handler) adminSetPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req setPasswordRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	if err := h.svc.SetPassword(ctx, pathParam(r, paramID), string(req.Password)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// --- api keys -----------------------------------------------------------------

// adminCreateAPIKey produces a new API key (POST /admin/v1/api-keys).
//
// THE PLAINTEXT KEY IS RETURNED ONLY IN THIS RESPONSE and can never again be
// read from any endpoint. The created_by field is filled NOT from the body but
// from the authenticated identity; otherwise the client would be the one
// writing the audit record.
//
// The endpoint asks for [ScopeWrite]; the scope list in the body CANNOT EXCEED
// the caller's own (see service.CreateAPIKey). Together the two prevent this
// endpoint from turning into a privilege escalation tool.
func (h *Handler) adminCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createAPIKeyRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	key, plaintext, err := h.svc.CreateAPIKey(ctx, service.CreateAPIKeyInput{
		Type:            models.APIKeyType(req.Type),
		Title:           req.Title,
		Scopes:          req.Scopes,
		CreatedBy:       actorID(ctx),
		SalesChannelIDs: req.SalesChannelIDs,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusCreated, itemEnvelope{Data: createAPIKeyResponse{
		APIKey: toAPIKeyDTO(key),
		Key:    plaintext,
	}})
}

// adminListAPIKeys lists the keys, filtering and paging them
// (GET /admin/v1/api-keys).
//
// Filters: type, revoked. There is NO plaintext key in the response; only the
// masked representation is there.
func (h *Handler) adminListAPIKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	revoked, err := boolParam(r, "revoked")
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	var keyType *models.APIKeyType
	if raw := stringParam(r, "type"); raw != nil {
		value := models.APIKeyType(*raw)
		keyType = &value
	}

	page, err := h.svc.ListAPIKeys(ctx, service.ListAPIKeysInput{
		Type:    keyType,
		Revoked: revoked,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toAPIKeyDTO)
}

// adminGetAPIKey returns a single key (GET /admin/v1/api-keys/{id}).
func (h *Handler) adminGetAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	key, err := h.svc.GetAPIKey(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toAPIKeyDTO(key))
}

// adminRevokeAPIKey revokes the key
// (POST /admin/v1/api-keys/{id}/revoke).
//
// REVOCATION IS NOT DELETION: the record stays in the list and when and by whom
// it was closed remains visible. On an already revoked key a 409 is returned.
func (h *Handler) adminRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	revoked, err := h.svc.RevokeAPIKey(ctx, pathParam(r, paramID), actorID(ctx))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toAPIKeyDTO(revoked))
}

// adminDeleteAPIKey soft-deletes the key
// (DELETE /admin/v1/api-keys/{id}).
//
// After a leak the operation to prefer is revocation; deletion is for cleaning
// up a record that was created by mistake.
func (h *Handler) adminDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteAPIKey(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// adminListKeyChannels returns the channels the key is attached to
// (GET /admin/v1/api-keys/{id}/sales-channels).
//
// Disabled channels are listed too: the admin surface has to show the link as
// it is.
func (h *Handler) adminListKeyChannels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	channels, err := h.svc.SalesChannelsOfAPIKey(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, convertAll(channels, toSalesChannelDTO))
}

// adminLinkKeyChannel attaches the publishable key to a sales channel
// (POST /admin/v1/api-keys/{id}/sales-channels).
func (h *Handler) adminLinkKeyChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyID := pathParam(r, paramID)

	var req linkChannelRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	if err := h.svc.LinkSalesChannel(ctx, keyID, req.SalesChannelID); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	// Once the link is established the CURRENT list is returned: the client is
	// spared having to make a second request.
	channels, err := h.svc.SalesChannelsOfAPIKey(ctx, keyID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, convertAll(channels, toSalesChannelDTO))
}

// adminUnlinkKeyChannel removes the link
// (DELETE /admin/v1/api-keys/{id}/sales-channels/{sales_channel_id}).
func (h *Handler) adminUnlinkKeyChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.UnlinkSalesChannel(
		ctx, pathParam(r, paramID), pathParam(r, paramChannelID),
	); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// --- sales channels -----------------------------------------------------------

// adminCreateSalesChannel creates a new sales channel
// (POST /admin/v1/sales-channels).
func (h *Handler) adminCreateSalesChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req salesChannelRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	created, err := h.svc.CreateSalesChannel(ctx, service.SalesChannelInput{
		Name:        req.Name,
		Description: req.Description,
		IsDisabled:  req.IsDisabled,
		Metadata:    req.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toSalesChannelDTO(created))
}

// adminListSalesChannels lists the channels, filtering and paging them
// (GET /admin/v1/sales-channels).
//
// Filters: name, is_disabled.
func (h *Handler) adminListSalesChannels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	isDisabled, err := boolParam(r, "is_disabled")
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := h.svc.ListSalesChannels(ctx, service.ListSalesChannelsInput{
		Name:       stringParam(r, "name"),
		IsDisabled: isDisabled,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toSalesChannelDTO)
}

// adminGetSalesChannel returns a single channel
// (GET /admin/v1/sales-channels/{id}).
func (h *Handler) adminGetSalesChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	channel, err := h.svc.GetSalesChannel(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toSalesChannelDTO(channel))
}

// adminUpdateSalesChannel updates the given fields of the channel
// (PUT /admin/v1/sales-channels/{id}).
func (h *Handler) adminUpdateSalesChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req updateSalesChannelRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	updated, err := h.svc.UpdateSalesChannel(ctx, pathParam(r, paramID), service.UpdateSalesChannelInput{
		Name:        req.Name,
		Description: req.Description,
		IsDisabled:  req.IsDisabled,
		Metadata:    req.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toSalesChannelDTO(updated))
}

// adminDeleteSalesChannel soft-deletes the channel and removes the key links
// (DELETE /admin/v1/sales-channels/{id}).
func (h *Handler) adminDeleteSalesChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteSalesChannel(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
