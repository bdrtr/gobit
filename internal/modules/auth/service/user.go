package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// CreateUserInput is the write input of an admin user.
//
// THE PLAINTEXT PASSWORD IS NOT IN THIS STRUCT and that is deliberate: input
// structs fall into the log, into the error context and into the output of
// tests through "%+v". The password is given to [Service.CreateUser] as a
// SEPARATE parameter, is put into no struct and is dropped once it has been
// hashed.
type CreateUserInput struct {
	// Email is the user's email address; it is required, it is stored
	// normalized to lower case and it is used as the login identity as well.
	Email string
	// FirstName is the user's first name; it may be left empty.
	FirstName string
	// LastName is the user's last name; it may be left empty.
	LastName string
	// AvatarURL is the address of the profile image; it may be left empty.
	AvatarURL string
	// Scopes are the user's privileges.
	//
	// If nil is given [models.ScopeAdmin] is applied: the default for an admin
	// user is full privilege. An EMPTY but non-nil slice, on the other hand, is
	// a real request and produces a user with no scope — it can log in and read
	// its own identity (GET /admin/v1/auth/me), but can reach no other admin
	// endpoint. Separating the two cases preserves the difference between "I
	// forgot the scope field" and "let it have no scope".
	//
	// A scope the caller does not have itself cannot be granted (see
	// [requireGrantableScopes]).
	Scopes []string
	// Metadata is free structured context; it may be left empty.
	Metadata map[string]any
}

// CreateUser creates a new admin user.
//
// If password IS NOT empty the user and the login identity are written IN A
// SINGLE TRANSACTION: an admin user whose password could not be assigned can
// never log in and you only notice it on the first login attempt. If password
// is empty only the user is written and the password is assigned later with
// [Service.SetPassword].
//
// The plaintext password lives inside this call: it is validated, it is hashed
// and it is never used again. It is neither logged nor does it appear in an
// error message.
//
// The new user cannot receive a scope THE CALLER DOES NOT HAVE ITSELF (see
// [requireGrantableScopes]): could it, a narrowly scoped administrator would
// create a fully privileged user for itself and log in with it.
//
// If the email address is already in use errors.Conflict is returned.
func (s *Service) CreateUser(
	ctx context.Context,
	in CreateUserInput,
	password string,
) (models.User, error) {
	if err := s.ready(); err != nil {
		return models.User{}, err
	}

	email, err := normalizeEmail(in.Email)
	if err != nil {
		return models.User{}, err
	}
	if err := validateUserFields(in.FirstName, in.LastName, in.AvatarURL); err != nil {
		return models.User{}, err
	}
	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return models.User{}, err
	}
	if scopes == nil {
		scopes = []string{models.ScopeAdmin}
	}
	// The check is made AFTER the default has been applied: a request that does
	// not fill the scope field in at all gives birth to a fully privileged user,
	// and saying "I did not grant it" is not enough to have not granted it.
	if err := requireGrantableScopes(ctx, scopes); err != nil {
		return models.User{}, err
	}

	var identity *models.AuthIdentity
	now := s.clock()
	if password != "" {
		if err := validatePassword(password); err != nil {
			return models.User{}, err
		}
		hash, hashErr := s.hashPassword(password)
		if hashErr != nil {
			return models.User{}, hashErr
		}
		identity = &models.AuthIdentity{
			ID:               models.NewAuthIdentityID(now),
			Provider:         models.ProviderEmailPass,
			ProviderIdentity: email,
			PasswordHash:     hash,
			CreatedAt:        now,
		}
	}

	created, err := s.repo.CreateUser(ctx, models.User{
		ID:        models.NewUserID(now),
		Email:     email,
		FirstName: in.FirstName,
		LastName:  in.LastName,
		AvatarURL: in.AvatarURL,
		Scopes:    scopes,
		Metadata:  in.Metadata,
		CreatedAt: now,
	}, identity)
	if err != nil {
		return models.User{}, err
	}

	// The email address is sensitive data and is not logged (plan Section 8);
	// the identifier and the password state are enough to trace a call.
	s.log.InfoContext(ctx, "admin user created",
		slog.String("user_id", created.ID),
		slog.Bool("password_assigned", identity != nil),
	)
	return created, nil
}

// GetUser returns the user with the given identifier; errors.NotFound if there
// is none.
func (s *Service) GetUser(ctx context.Context, id string) (models.User, error) {
	if err := s.ready(); err != nil {
		return models.User{}, err
	}
	if err := requireID(id, models.UserIDPrefix, "the user identifier"); err != nil {
		return models.User{}, err
	}
	return s.repo.GetUser(ctx, id)
}

// GetUserByEmail returns the user with the given email address;
// errors.NotFound if there is none.
//
// This method is for the ADMIN surface. The login flow DOES NOT USE it: in
// order not to leak the difference between "no such user" and "wrong password"
// to the outside, the login runs its own branch (see [Service.Login]).
func (s *Service) GetUserByEmail(ctx context.Context, email string) (models.User, error) {
	if err := s.ready(); err != nil {
		return models.User{}, err
	}
	normalized, err := normalizeEmail(email)
	if err != nil {
		return models.User{}, err
	}
	return s.repo.GetUserByEmail(ctx, normalized)
}

// ListUsersInput is the input of a user listing.
type ListUsersInput struct {
	// Email, if given, restricts the result to the user with this email
	// address.
	Email *string
	// Scope, if given, restricts the result to users holding this scope.
	Scope *string
	// Limit is the page size; [DefaultLimit] is applied if it is 0.
	Limit int64
	// Offset is the number of records to skip.
	Offset int64
}

// ListUsers returns the filtered and paginated list of users.
func (s *Service) ListUsers(ctx context.Context, in ListUsersInput) (Page[models.User], error) {
	if err := s.ready(); err != nil {
		return Page[models.User]{}, err
	}

	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.User]{}, err
	}

	var filter models.UserFilter
	if in.Email != nil {
		normalized, emailErr := normalizeEmail(*in.Email)
		if emailErr != nil {
			return Page[models.User]{}, emailErr
		}
		filter.Email = &normalized
	}
	if in.Scope != nil {
		scopes, scopeErr := normalizeScopes([]string{*in.Scope})
		if scopeErr != nil {
			return Page[models.User]{}, scopeErr
		}
		filter.Scope = &scopes[0]
	}

	items, total, err := s.repo.ListUsers(ctx, filter, limit, offset)
	if err != nil {
		return Page[models.User]{}, err
	}
	return Page[models.User]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdateUserInput is the partial update input of a user.
//
// A nil field means "do not touch", a filled field means "write this value".
// THE PASSWORD CANNOT BE CHANGED HERE: the password is a separate operation
// ([Service.SetPassword]) and had it been put into the same body as the profile
// update, a request that changes a name could accidentally reset the password
// as well.
type UpdateUserInput struct {
	// Email is the new email address; the login identity is updated along with
	// it.
	Email *string
	// FirstName is the new first name.
	FirstName *string
	// LastName is the new last name.
	LastName *string
	// AvatarURL is the new avatar address.
	AvatarURL *string
	// Scopes is the new scope list; if nil it is not touched, an empty slice
	// REMOVES all scopes.
	//
	// A scope the caller does not have itself cannot be granted (see
	// [requireGrantableScopes]). REMOVING a scope is allowed: coming down to a
	// narrower list is not an escalation.
	Scopes []string
	// Metadata is the new metadata map; it replaces the whole column.
	Metadata map[string]any
}

// UpdateUser updates the given fields of the user.
//
// If the email address changes the login identity is updated in the same
// transaction; otherwise the user could not log in with their new address. If
// the email address belongs to another user errors.Conflict is returned.
//
// The scope list CANNOT EXCEED THE CALLER'S OWN (see
// [requireGrantableScopes]): could it, a narrowly scoped identity would update
// its own record and become admin.
func (s *Service) UpdateUser(ctx context.Context, id string, in UpdateUserInput) (models.User, error) {
	if err := s.ready(); err != nil {
		return models.User{}, err
	}
	if err := requireID(id, models.UserIDPrefix, "the user identifier"); err != nil {
		return models.User{}, err
	}

	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return models.User{}, err
	}
	if err := requireGrantableScopes(ctx, scopes); err != nil {
		return models.User{}, err
	}

	patch := models.UserPatch{
		FirstName: in.FirstName,
		LastName:  in.LastName,
		AvatarURL: in.AvatarURL,
		Scopes:    scopes,
		Metadata:  in.Metadata,
	}
	if in.Email != nil {
		normalized, emailErr := normalizeEmail(*in.Email)
		if emailErr != nil {
			return models.User{}, emailErr
		}
		patch.Email = &normalized
	}
	if err := validateUserPatch(patch); err != nil {
		return models.User{}, err
	}

	return s.repo.UpdateUser(ctx, id, patch, s.clock())
}

// DeleteUser soft deletes the user and their login identities.
//
// The identities are deleted as well; had they stayed alive the user could log
// in EVEN AFTER BEING DELETED. A session token produced earlier IS NOT ACCEPTED
// either, even though its signature is valid: authentication asks on every
// request whether the user still exists (see interop.go).
func (s *Service) DeleteUser(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.UserIDPrefix, "the user identifier"); err != nil {
		return err
	}
	if err := s.repo.DeleteUser(ctx, id, s.clock()); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "admin user deleted", slog.String("user_id", id))
	return nil
}

// validateUserFields validates the length bounds of the user's text fields.
func validateUserFields(firstName, lastName, avatarURL string) error {
	if err := checkLen("the first name", firstName, models.MaxNameLen); err != nil {
		return err
	}
	if err := checkLen("the last name", lastName, models.MaxNameLen); err != nil {
		return err
	}
	return checkLen("the avatar address", avatarURL, models.MaxURLLen)
}

// validateUserPatch validates the fields in a partial update.
//
// nil fields are skipped: the distinction between "do not touch" and "write
// empty" is preserved and no length error is produced for a field that was not
// given.
func validateUserPatch(patch models.UserPatch) error {
	if patch.FirstName != nil {
		if err := checkLen("the first name", *patch.FirstName, models.MaxNameLen); err != nil {
			return err
		}
	}
	if patch.LastName != nil {
		if err := checkLen("the last name", *patch.LastName, models.MaxNameLen); err != nil {
			return err
		}
	}
	if patch.AvatarURL != nil {
		if err := checkLen("the avatar address", *patch.AvatarURL, models.MaxURLLen); err != nil {
			return err
		}
	}
	return nil
}
