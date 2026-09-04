package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// DTOs are kept SEPARATE from the domain models: JSON field names are an
// external contract and a rename made in the model must not break the client.
//
// The separation has a second job in this module: fields that are present on
// the model but MUST NOT GO OUT (such as [models.APIKey.TokenHash]) take no
// place at all in the DTO. Had the response body been derived from the model
// automatically, a secret field added to the model would have leaked silently
// into the API.

// --- request bodies ---------------------------------------------------------

// loginRequest is the body of the login request.
type loginRequest struct {
	// Email is the user's email.
	Email string `json:"email"`
	// Password is the user's plaintext password; it travels as the [secret]
	// type and is masked when logged.
	Password secret `json:"password"`
}

// createUserRequest is the body of the user creation request.
type createUserRequest struct {
	// Email is the user's email; it is required.
	Email string `json:"email"`
	// FirstName is the user's first name.
	FirstName string `json:"first_name"`
	// LastName is the user's last name.
	LastName string `json:"last_name"`
	// AvatarURL is the address of the profile image.
	AvatarURL string `json:"avatar_url"`
	// Scopes are the user's scopes; if not given, "admin" is applied.
	//
	// A scope the caller DOES NOT HOLD cannot be granted; such a request
	// returns 403 (see service.CreateUser). Not giving the field at all is a
	// scope request too: the default is full scope and that goes through the
	// same check.
	Scopes []string `json:"scopes"`
	// Password is the user's first password; it may be left empty and the
	// password is then assigned later.
	Password secret `json:"password"`
	// Metadata is free structured context.
	Metadata map[string]any `json:"metadata"`
}

// updateUserRequest is the body of the user update request.
//
// The password field is DELIBERATELY absent: the password is changed through a
// separate endpoint (POST /admin/v1/users/{id}/password). Had it been in the
// same body, it would have been possible for a request updating a name to
// change the password by accident.
type updateUserRequest struct {
	// Email is the new email address.
	Email *string `json:"email"`
	// FirstName is the new first name.
	FirstName *string `json:"first_name"`
	// LastName is the new last name.
	LastName *string `json:"last_name"`
	// AvatarURL is the new avatar address.
	AvatarURL *string `json:"avatar_url"`
	// Scopes is the new scope list; if not given it is left untouched.
	//
	// A scope the caller DOES NOT HOLD cannot be granted; such a request
	// returns 403 (see service.UpdateUser). Removing a scope is free.
	Scopes []string `json:"scopes"`
	// Metadata is the new metadata map.
	Metadata map[string]any `json:"metadata"`
}

// setPasswordRequest is the body of the password assignment request.
type setPasswordRequest struct {
	// Password is the new plaintext password.
	Password secret `json:"password"`
}

// createAPIKeyRequest is the body of the key creation request.
type createAPIKeyRequest struct {
	// Type is the key's type: "publishable" or "secret".
	Type string `json:"type"`
	// Title is the key's display name.
	Title string `json:"title"`
	// Scopes are the secret key's scopes; they cannot be given on a
	// publishable key.
	//
	// A scope the caller DOES NOT HOLD cannot be granted; such a request
	// returns 403 (see service.CreateAPIKey). On a secret key, not giving the
	// field at all means "admin" and that goes through the same check.
	Scopes []string `json:"scopes"`
	// SalesChannelIDs are the channels the publishable key will be attached
	// to; they cannot be given on a secret key.
	SalesChannelIDs []string `json:"sales_channel_ids"`
}

// linkChannelRequest is the body of the key-to-channel attachment request.
type linkChannelRequest struct {
	// SalesChannelID is the identifier of the channel to attach.
	SalesChannelID string `json:"sales_channel_id"`
}

// salesChannelRequest is the body of the channel creation request.
type salesChannelRequest struct {
	// Name is the channel's name; it is required.
	Name string `json:"name"`
	// Description is the channel's description.
	Description string `json:"description"`
	// IsDisabled makes the channel open disabled.
	IsDisabled bool `json:"is_disabled"`
	// Metadata is free structured context.
	Metadata map[string]any `json:"metadata"`
}

// updateSalesChannelRequest is the body of the channel update request.
type updateSalesChannelRequest struct {
	// Name is the channel's new name.
	Name *string `json:"name"`
	// Description is the channel's new description.
	Description *string `json:"description"`
	// IsDisabled is the channel's new enablement state.
	IsDisabled *bool `json:"is_disabled"`
	// Metadata is the new metadata map.
	Metadata map[string]any `json:"metadata"`
}

// --- response bodies --------------------------------------------------------

// loginResponse is the body of the login response.
//
// The token is a SECRET: the client stores it and sends it in the
// Authorization header on every request. It is written nowhere outside the
// response body (log, audit record, error message).
type loginResponse struct {
	// Token is the signed session token (HS256 JWT).
	Token string `json:"token"`
	// ExpiresAt is the token's expiry moment (RFC3339, UTC).
	ExpiresAt time.Time `json:"expires_at"`
	// TokenType is the scheme the token is to be used with in the
	// Authorization header.
	TokenType string `json:"token_type"`
}

// logoutResponse is the body of the logout response.
//
// The body says what the status code cannot: that the logout covers ALL OF THE
// CALLER and which moment it rests on. An empty 204 would not have corrected a
// client that thought "I logged out of this device".
type logoutResponse struct {
	// AllSessions reports that the revocation covers ALL of the caller's
	// sessions.
	//
	// The field is always true today and this is not a shortcoming but the
	// contract itself: there is no way to drop a single device (see
	// service.Service.Logout). Being constant it could have been dropped, but
	// then the only place a client could learn about the wholesale revocation
	// would have been the documentation, and a developer looking at the
	// response would have been left alone with their wrong assumption.
	AllSessions bool `json:"all_sessions"`
	// RevokedAt is the moment the revocation rests on (RFC3339, UTC).
	//
	// Every session token produced BEFORE this moment is rejected from now on;
	// the token used in the request itself is included in that.
	RevokedAt time.Time `json:"revoked_at"`
}

// principalResponse is the identity of the authenticated caller.
type principalResponse struct {
	// ID is the caller's identifier (user or API key).
	ID string `json:"id"`
	// Kind is the identity's type: "user" | "api_key".
	Kind string `json:"kind"`
	// Scopes are the caller's scopes.
	Scopes []string `json:"scopes"`
	// SalesChannelIDs are the channels the publishable key is attached to; on
	// an admin identity it is empty.
	SalesChannelIDs []string `json:"sales_channel_ids,omitempty"`
}

// userDTO is the response body of an admin user.
//
// A password or a password hash IS NOT HERE and never will be.
type userDTO struct {
	// ID is the user's identifier.
	ID string `json:"id"`
	// Email is the user's email (normalized to lower case).
	Email string `json:"email"`
	// FirstName is the user's first name.
	FirstName string `json:"first_name"`
	// LastName is the user's last name.
	LastName string `json:"last_name"`
	// AvatarURL is the address of the profile image.
	AvatarURL string `json:"avatar_url"`
	// Scopes are the user's scopes.
	Scopes []string `json:"scopes"`
	// Metadata is free structured context; if empty it does not appear in the
	// body.
	Metadata map[string]any `json:"metadata,omitempty"`
	// CreatedAt is the creation moment (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the last update moment (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// apiKeyDTO is the response body of an API key.
//
// The [models.APIKey.TokenHash] field IS NOT HERE: even though the digest is
// not a secret, handing it outside is unnecessary, and handing it out would
// have led to the belief that "we have found the key". The plaintext, on the
// other hand, is present only in the creation response
// ([createAPIKeyResponse]).
type apiKeyDTO struct {
	// ID is the key's identifier.
	ID string `json:"id"`
	// Type is the key's type: "publishable" | "secret".
	Type string `json:"type"`
	// Title is the key's display name.
	Title string `json:"title"`
	// Redacted is the masked display (e.g. "pk_...a1b2"); it cannot be used to
	// authenticate.
	Redacted string `json:"redacted"`
	// Scopes are the key's scopes; on a publishable key it is empty.
	Scopes []string `json:"scopes"`
	// CreatedBy is the identity of whoever produced the key.
	CreatedBy string `json:"created_by"`
	// LastUsedAt is the last use moment; it is APPROXIMATE and does not appear
	// in the body if the key has never been used.
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	// RevokedAt is the revocation moment; it does not appear in the body if
	// the key has not been revoked.
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	// RevokedBy is the identity of whoever performed the revocation; if empty
	// it does not appear in the body.
	RevokedBy string `json:"revoked_by,omitempty"`
	// CreatedAt is the creation moment.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the last update moment.
	UpdatedAt time.Time `json:"updated_at"`
}

// createAPIKeyResponse is the body of the key creation response.
//
// THE PLAINTEXT KEY IS RETURNED ONLY HERE. The value can never again be read
// from any endpoint; if the client does not store it now the key is lost and
// the only remedy is to revoke it and produce a new one. This is the direct
// consequence of storage being done over the digest alone (see
// [models.APIKey]).
type createAPIKeyResponse struct {
	// APIKey is the key's record.
	APIKey apiKeyDTO `json:"api_key"`
	// Key is the PLAINTEXT of the key; it is not shown again.
	Key string `json:"key"`
}

// salesChannelDTO is the response body of a sales channel.
type salesChannelDTO struct {
	// ID is the channel's identifier.
	ID string `json:"id"`
	// Name is the channel's name.
	Name string `json:"name"`
	// Description is the channel's description.
	Description string `json:"description"`
	// IsDisabled reports that the channel is disabled.
	IsDisabled bool `json:"is_disabled"`
	// Metadata is free structured context; if empty it does not appear in the
	// body.
	Metadata map[string]any `json:"metadata,omitempty"`
	// CreatedAt is the creation moment.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the last update moment.
	UpdatedAt time.Time `json:"updated_at"`
}

// --- conversions ------------------------------------------------------------

// toUserDTO converts the domain user into the response body.
func toUserDTO(u models.User) userDTO {
	return userDTO{
		ID:        u.ID,
		Email:     u.Email,
		FirstName: u.FirstName,
		LastName:  u.LastName,
		AvatarURL: u.AvatarURL,
		Scopes:    orEmpty(u.Scopes),
		Metadata:  u.Metadata,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

// toAPIKeyDTO converts the domain key into the response body.
func toAPIKeyDTO(k models.APIKey) apiKeyDTO {
	return apiKeyDTO{
		ID:         k.ID,
		Type:       k.Type.String(),
		Title:      k.Title,
		Redacted:   k.Redacted,
		Scopes:     orEmpty(k.Scopes),
		CreatedBy:  k.CreatedBy,
		LastUsedAt: k.LastUsedAt,
		RevokedAt:  k.RevokedAt,
		RevokedBy:  k.RevokedBy,
		CreatedAt:  k.CreatedAt,
		UpdatedAt:  k.UpdatedAt,
	}
}

// toSalesChannelDTO converts the domain channel into the response body.
func toSalesChannelDTO(c models.SalesChannel) salesChannelDTO {
	return salesChannelDTO{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		IsDisabled:  c.IsDisabled,
		Metadata:    c.Metadata,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

// toCreateUserInput converts the request body into the service input.
//
// The password IS NOT CARRIED IN THIS CONVERSION: the service takes it as a
// separate parameter and it is put in no struct.
func toCreateUserInput(req createUserRequest) service.CreateUserInput {
	return service.CreateUserInput{
		Email:     req.Email,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		AvatarURL: req.AvatarURL,
		Scopes:    req.Scopes,
		Metadata:  req.Metadata,
	}
}

// toUpdateUserInput converts the request body into the service input.
func toUpdateUserInput(req updateUserRequest) service.UpdateUserInput {
	return service.UpdateUserInput{
		Email:     req.Email,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		AvatarURL: req.AvatarURL,
		Scopes:    req.Scopes,
		Metadata:  req.Metadata,
	}
}

// orEmpty converts a nil slice into an empty slice.
//
// Seeing [] in JSON instead of "scopes": null is a uniform surface for the
// consumer; a client seeing null would have been forced to write an extra
// check before looping over the array.
func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
