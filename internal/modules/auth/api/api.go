// Package api is the auth module's HTTP surface.
//
// Every endpoint of the module lives under /admin/v1; auth has NO storefront
// (/store/v1) endpoint. Its counterpart on the store side is not an endpoint
// but the corehttp.RequireStore middleware that reads the publishable key.
//
// # Endpoints
//
// Identity endpoints (they ASK FOR NO scope, see [Handler.Routes]):
//
//   - POST /admin/v1/auth/login — produces a token; it is the ONLY UNPROTECTED
//     endpoint.
//   - GET /admin/v1/auth/me — reads back the identity of the authenticated
//     caller.
//   - POST /admin/v1/auth/logout — drops ALL of the caller's sessions; a single
//     device cannot be picked (see [Handler.adminLogout]).
//
// Resource endpoints (they ask for [ScopeRead] or [ScopeWrite]):
//
//   - /admin/v1/users, /admin/v1/users/{id}/password
//   - /admin/v1/api-keys, /admin/v1/api-keys/{id}/revoke and the channel links
//   - /admin/v1/sales-channels
//
// # UNPROTECTED ENDPOINT: POST /admin/v1/auth/login
//
// The login endpoint is UNPROTECTED by its very nature: it is the request that
// is going to be authenticated, it is only about to establish the identity.
// When corehttp.RequireAdmin is attached to the admin surface this endpoint
// MUST BE LEFT OUT; if it is taken under protection nobody can log in and the
// system locks itself out.
//
// Attaching the identity middleware is NOT this module's job but the job of
// whoever builds the router; the exemption is defined there as well.
// AUTHORIZATION, on the other hand, is enforced here, endpoint by endpoint
// (see [Handler.Routes] and [ScopeRead], [ScopeWrite]).
//
// # Secrets
//
// The only plaintext secret that leaves this package is the
// [createAPIKeyResponse.Key] field of the key creation response, and it is
// returned once. A password NEVER PASSES through any response; the password
// field in the request body travels as the [secret] type and that type is
// masked when logged.
//
// Handlers DO NOT PICK the status code: the service returns a typed error and
// corehttp.WriteError turns it into a status code (plan Section 2.7).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// maxBodyBytes is the upper bound of a single request body. An unbounded body
// is the cheapest way to exhaust memory with a single request.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidBody is the error code returned when the request body or a
// parameter cannot be parsed.
const codeInvalidBody = "auth_invalid_body"

// The names of the path parameters.
const (
	paramID        = "id"
	paramChannelID = "sales_channel_id"
)

// secret is the string type that carries the plaintext password of the request
// body.
//
// Its only job is to prevent being logged BY ACCIDENT: when a request struct is
// recorded with "%v", "%+v" or with slog, this field shows up masked. Reaching
// the value itself takes an explicit string conversion and that conversion
// stands out in the code — which is exactly what is wanted.
type secret string

// String returns the masked representation.
func (s secret) String() string { return "REDACTED" }

// LogValue returns the masked value in slog output.
func (s secret) LogValue() slog.Value { return slog.StringValue("REDACTED") }

// Auth is the surface the handlers need from the service.
//
// Keeping it narrow simplifies the tests: the HTTP behavior can be verified
// with a fake a few lines long, without a real database.
type Auth interface {
	// Login produces a session token from an email and a password.
	Login(ctx context.Context, email, password string) (string, time.Time, error)
	// Logout drops ALL of the caller's sessions and returns the revocation
	// moment.
	//
	// The KIND of the identity is passed too: an API key has no session and
	// such a call is rejected with a typed error (see service.Service.Logout).
	Logout(ctx context.Context, principalID, principalKind string) (time.Time, error)

	// CreateUser creates a new admin user; password may be empty.
	CreateUser(ctx context.Context, in service.CreateUserInput, password string) (models.User, error)
	// GetUser returns the user by their identifier.
	GetUser(ctx context.Context, id string) (models.User, error)
	// ListUsers filters and pages the users.
	ListUsers(ctx context.Context, in service.ListUsersInput) (service.Page[models.User], error)
	// UpdateUser updates the given fields of the user.
	UpdateUser(ctx context.Context, id string, in service.UpdateUserInput) (models.User, error)
	// DeleteUser soft-deletes the user.
	DeleteUser(ctx context.Context, id string) error
	// SetPassword assigns the user's password.
	SetPassword(ctx context.Context, userID, password string) error

	// CreateAPIKey produces a new API key and returns its plaintext once.
	CreateAPIKey(ctx context.Context, in service.CreateAPIKeyInput) (models.APIKey, string, error)
	// GetAPIKey returns the key by its identifier.
	GetAPIKey(ctx context.Context, id string) (models.APIKey, error)
	// ListAPIKeys filters and pages the keys.
	ListAPIKeys(ctx context.Context, in service.ListAPIKeysInput) (service.Page[models.APIKey], error)
	// RevokeAPIKey revokes the key.
	RevokeAPIKey(ctx context.Context, id, revokedBy string) (models.APIKey, error)
	// DeleteAPIKey soft-deletes the key.
	DeleteAPIKey(ctx context.Context, id string) error
	// LinkSalesChannel attaches the publishable key to a sales channel.
	LinkSalesChannel(ctx context.Context, apiKeyID, channelID string) error
	// UnlinkSalesChannel removes the link.
	UnlinkSalesChannel(ctx context.Context, apiKeyID, channelID string) error
	// SalesChannelsOfAPIKey returns the channels the key is attached to.
	SalesChannelsOfAPIKey(ctx context.Context, apiKeyID string) ([]models.SalesChannel, error)

	// CreateSalesChannel creates a new sales channel.
	CreateSalesChannel(ctx context.Context, in service.SalesChannelInput) (models.SalesChannel, error)
	// GetSalesChannel returns the channel by its identifier.
	GetSalesChannel(ctx context.Context, id string) (models.SalesChannel, error)
	// ListSalesChannels filters and pages the channels.
	ListSalesChannels(ctx context.Context, in service.ListSalesChannelsInput) (service.Page[models.SalesChannel], error)
	// UpdateSalesChannel updates the given fields of the channel.
	UpdateSalesChannel(ctx context.Context, id string, in service.UpdateSalesChannelInput) (models.SalesChannel, error)
	// DeleteSalesChannel soft-deletes the channel.
	DeleteSalesChannel(ctx context.Context, id string) error
}

// Handler is the auth module's set of HTTP handlers.
type Handler struct {
	svc Auth
}

// New produces the set of handlers that works on the given service.
func New(svc Auth) *Handler {
	return &Handler{svc: svc}
}

// LoginPath is the full path of the login endpoint.
//
// It is published as a constant so that whoever builds the router does not have
// to hand-write the path they leave UNPROTECTED: if the path changes the
// exception changes along with it and does not one day silently fall under
// protection and lock the system out.
const LoginPath = "/admin/v1/auth/login"

// The scope dictionary: the scopes auth's admin endpoints ask for.
//
// The dictionary DELIBERATELY consists of two entries. Defining a separate
// scope per resource ("users:read", "api_keys:write" …) grows the list but
// makes no new decision possible today: this module is the only place that
// hands scopes out, and a scope name that is never handed out is a name nobody
// knows the purpose of on the day it is first granted. The distinction gets
// added when it is genuinely needed; added ahead of time it only gives a false
// sense of precision.
const (
	// ScopeRead is the scope the READ endpoints of auth's admin surface ask
	// for.
	//
	// It is enough to read user records, the MASKED representations of API keys
	// and the sales channels; it opens no write endpoint. Granting it is also
	// unnecessary for fully privileged identities: a caller carrying
	// corehttp.ScopeAdmin satisfies this one too (see
	// corehttp.Principal.HasScope).
	ScopeRead = "auth:read"

	// ScopeWrite is the scope the WRITE endpoints of auth's admin surface ask
	// for; it is corehttp.ScopeAdmin itself.
	//
	// A narrower "auth:write" IS NOT DEFINED and this is not a shortcoming:
	// what is written at these endpoints is scope itself — a user's scope, a
	// key's scope, the sales channel a key will see. An identity that can write
	// scope is already an admin, since it can make itself one in a single
	// request; a separate name would have made a boundary that does not really
	// exist look as if it did.
	ScopeWrite = corehttp.ScopeAdmin
)

// Routes attaches auth's admin routes to the router.
//
// The routes are registered with full paths and NOT with chi's Route/Mount
// helpers: the /admin/v1 prefix is shared by more than one module and mounting
// the same prefix twice would make chi panic. Full-path registration writes
// them side by side into the same tree.
//
// # PROTECTION
//
// There are two layers and both of them are necessary:
//
//  1. IDENTITY — every endpoint OTHER THAN [LoginPath] is protected with
//     corehttp.RequireAdmin. That middleware is attached not in this module but
//     on the side that builds the router (see corehttp.APIGuards); the
//     exemption list lives there too.
//  2. AUTHORIZATION — the endpoints are marked HERE, one by one, with
//     corehttp.RequireScope: read endpoints ask for [ScopeRead], write
//     endpoints for [ScopeWrite].
//
// Without the second layer authentication would take the place of
// authorization. The concrete cost is this: a secret key carrying only
// "orders:read", or a user with no scope at all, could call POST
// /admin/v1/api-keys and produce a fully privileged key for itself — privilege
// escalation in a single request. The same escalation is blocked a second time
// in the service layer (see service.CreateAPIKey), because the map here may one
// day be loosened.
//
// Identity endpoints ASK FOR NO scope: [LoginPath] is only about to establish
// the identity, GET /admin/v1/auth/me reads the established identity itself
// back, and POST /admin/v1/auth/logout ends it. Putting a scope on an identity
// endpoint would mean that a caller without scope could not even learn who they
// are, and that would make debugging impossible while protecting nothing.
// Putting a scope on the logout endpoint would do something worse: the token in
// the hands of an admin whose scope had been taken away would become impossible
// to close until it expired.
func (h *Handler) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	// --- identity (login UNPROTECTED, /me and /logout ask for IDENTITY only) ---
	r.Post(LoginPath, h.adminLogin)
	r.Get("/admin/v1/auth/me", h.adminWhoami)
	r.Post("/admin/v1/auth/logout", h.adminLogout)

	// --- users ---
	write.Post("/admin/v1/users", h.adminCreateUser)
	read.Get("/admin/v1/users", h.adminListUsers)
	read.Get("/admin/v1/users/{id}", h.adminGetUser)
	write.Put("/admin/v1/users/{id}", h.adminUpdateUser)
	write.Delete("/admin/v1/users/{id}", h.adminDeleteUser)
	write.Post("/admin/v1/users/{id}/password", h.adminSetPassword)

	// --- api keys ---
	write.Post("/admin/v1/api-keys", h.adminCreateAPIKey)
	read.Get("/admin/v1/api-keys", h.adminListAPIKeys)
	read.Get("/admin/v1/api-keys/{id}", h.adminGetAPIKey)
	write.Delete("/admin/v1/api-keys/{id}", h.adminDeleteAPIKey)
	write.Post("/admin/v1/api-keys/{id}/revoke", h.adminRevokeAPIKey)
	read.Get("/admin/v1/api-keys/{id}/sales-channels", h.adminListKeyChannels)
	write.Post("/admin/v1/api-keys/{id}/sales-channels", h.adminLinkKeyChannel)
	write.Delete("/admin/v1/api-keys/{id}/sales-channels/{sales_channel_id}", h.adminUnlinkKeyChannel)

	// --- sales channels ---
	//
	// A channel record looks like catalog data but it is authorization data:
	// which catalog a publishable key gets to see is decided by the channel
	// link. That is why the write side asks for [ScopeWrite] too.
	write.Post("/admin/v1/sales-channels", h.adminCreateSalesChannel)
	read.Get("/admin/v1/sales-channels", h.adminListSalesChannels)
	read.Get("/admin/v1/sales-channels/{id}", h.adminGetSalesChannel)
	write.Put("/admin/v1/sales-channels/{id}", h.adminUpdateSalesChannel)
	write.Delete("/admin/v1/sales-channels/{id}", h.adminDeleteSalesChannel)
}

// itemEnvelope is the envelope of single-record responses (plan Section 8).
type itemEnvelope struct {
	// Data is the body of the single record.
	Data any `json:"data"`
}

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	// Data are the records on the current page.
	Data any `json:"data"`
	// Count is the TOTAL number of records matching the filter.
	Count int64 `json:"count"`
	// Offset is the number of skipped records that was applied.
	Offset int64 `json:"offset"`
	// Limit is the page size that was applied.
	Limit int64 `json:"limit"`
}

// writeItem writes a single-record response with its envelope.
func writeItem(w http.ResponseWriter, r *http.Request, status int, data any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: data})
}

// writeItems writes an unpaged list with its envelope.
//
// Limit is EQUAL to the number of returned records and is NOT CLAMPED with
// [service.MaxLimit]: there is no page here, the single page is all of the
// records. Had it been clamped the client would have mistaken the page size and
// entered a paging loop.
func writeItems[T any](w http.ResponseWriter, r *http.Request, items []T) {
	if items == nil {
		items = []T{}
	}
	count := int64(len(items))
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  count,
		Offset: 0,
		Limit:  count,
	})
}

// writePage writes the service page with the list envelope.
func writePage[S any, T any](w http.ResponseWriter, r *http.Request, page service.Page[S], convert func(S) T) {
	items := make([]T, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, convert(item))
	}
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  page.Count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// convertAll converts a slice into a DTO slice; a nil slice turns into an empty
// slice.
func convertAll[S any, T any](items []S, convert func(S) T) []T {
	out := make([]T, 0, len(items))
	for _, item := range items {
		out = append(out, convert(item))
	}
	return out
}

// decodeBody decodes the request body into the destination.
//
// Unknown fields are REJECTED: a silently ignored field means a value the client
// believes it sent is never written. The body size is bounded too; if the bound
// is exceeded it comes back as a parse error.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	reader := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(reader)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidBody, "request body cannot be empty")
		}
		// The text of the parse error MAY CONTAIN A QUOTE FROM THE BODY and the
		// body of the login request holds a password. The underlying error is
		// therefore wrapped but its message IS NOT WRITTEN; the detail only
		// lands in the log.
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidBody,
			"request body could not be parsed")
	}

	// A single JSON document is expected; were a second document following it
	// silently ignored, the client would believe what it sent had been
	// processed.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidBody,
			"request body has to be a single JSON document")
	}
	return nil
}

// pathParam reads a path parameter.
func pathParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

// actorID returns the identity making the request; the empty string if there is
// no identity.
//
// It fills the audit fields (created_by, revoked_by). The identity comes FROM
// THE CORE (corehttp.RequireAdmin puts it into the context); a value the client
// declares in the body IS NOT USED — had it been used, the client would be the
// one writing the audit record.
func actorID(ctx context.Context) string {
	principal, ok := corehttp.PrincipalFromContext(ctx)
	if !ok {
		return ""
	}
	return principal.ID
}

// pageParams reads the paging parameters from the query string.
//
// A missing parameter returns zero and the service applies its default; a value
// that CANNOT BE CONVERTED to a number returns an error instead — silently
// falling back to zero would have made the client get the first page rather
// than the page it asked for.
func pageParams(r *http.Request) (limit, offset int64, err error) {
	limit, err = intParam(r, "limit")
	if err != nil {
		return 0, 0, err
	}
	offset, err = intParam(r, "offset")
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}

// intParam reads a single numeric query parameter; returns zero if it is
// absent.
func intParam(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Invalid(codeInvalidBody,
			"the %q parameter has to be an integer, %q given", name, raw)
	}
	return value, nil
}

// boolParam reads a boolean query parameter; returns nil if it is absent.
//
// The difference between nil and false is meaningful here: "is_disabled=false"
// filters for enabled channels, whereas not giving the parameter at all filters
// nothing.
func boolParam(r *http.Request, name string) (*bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, coreerrors.Invalid(codeInvalidBody,
			"the %q parameter has to be a boolean (true/false), %q given", name, raw)
	}
	return &value, nil
}

// stringParam reads a text query parameter; returns nil if it is absent.
func stringParam(r *http.Request, name string) *string {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil
	}
	return &raw
}
