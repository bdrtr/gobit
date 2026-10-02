package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// The second-factor tabs of the panel's Users screen (ADR 0345).
const (
	// SecondFactorProven lists the users who have proven an authenticator.
	SecondFactorProven = "proven"
	// SecondFactorMissing lists the users who have not.
	SecondFactorMissing = "missing"
)

// panelUser is one user as the panel's Users screen lists them (ADR 0345);
// the json tags are the contract with the panel, which cannot import this
// package.
type panelUser struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	FirstName    string    `json:"first_name"`
	LastName     string    `json:"last_name"`
	Scopes       []string  `json:"scopes"`
	SecondFactor bool      `json:"second_factor"`
	CreatedAt    time.Time `json:"created_at"`
}

// listedUser is the user as the panel lists them, saying whether they have
// proven an authenticator.
func listedUser(user *models.User, secondFactor bool) panelUser {
	scopes := user.Scopes
	if scopes == nil {
		scopes = []string{}
	}

	return panelUser{
		ID: user.ID, Email: user.Email, FirstName: user.FirstName, LastName: user.LastName,
		Scopes: scopes, SecondFactor: secondFactor, CreatedAt: user.CreatedAt,
	}
}

// UsersJSON lists the shop's users, the newest first, a page at a time, with
// how many there are: the one with the e-mail when it is given, and those
// who have or have not proven an authenticator when secondFactor is
// [SecondFactorProven] or [SecondFactorMissing]. Each says whether they have
// proven one, read for the page in one query as the API does (ADR 0345).
func (s *AccountSurface) UsersJSON(
	ctx context.Context, email, secondFactor string, limit, offset int32,
) (json.RawMessage, int64, error) {
	in := ListUsersInput{Limit: int64(limit), Offset: int64(offset)}
	if email != "" {
		in.Email = &email
	}
	switch secondFactor {
	case "":
	case SecondFactorProven, SecondFactorMissing:
		proven := secondFactor == SecondFactorProven
		in.SecondFactor = &proven
	default:
		return nil, 0, errors.Invalid(CodeInvalidInput, "unknown second-factor tab: %q", secondFactor)
	}

	page, err := s.svc.ListUsers(ctx, in)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]string, 0, len(page.Items))
	for i := range page.Items {
		ids = append(ids, page.Items[i].ID)
	}
	holders, err := s.svc.SecondFactorHolders(ctx, ids)
	if err != nil {
		return nil, 0, err
	}

	out := make([]panelUser, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, listedUser(&page.Items[i], holders[page.Items[i].ID]))
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, 0, err
	}

	return body, page.Count, nil
}

// UserJSON returns the user as the Users screen lists them (ADR 0347).
func (s *AccountSurface) UserJSON(ctx context.Context, id string) (json.RawMessage, error) {
	user, err := s.svc.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	holders, err := s.svc.SecondFactorHolders(ctx, []string{user.ID})
	if err != nil {
		return nil, err
	}

	return json.Marshal(listedUser(&user, holders[user.ID]))
}

// ReviseUserScopes writes the user's privileges from the ones the operator
// read, and refuses when another writer changed them since (ADR 0347).
func (s *AccountSurface) ReviseUserScopes(ctx context.Context, id string, read, next []string) error {
	_, err := s.svc.ReviseUserScopes(ctx, id, read, next)

	return err
}

// InviteUser opens a user with the e-mail, name and privileges, without a
// password, and sends them an invitation from the operator (ADR 0348). No
// privilege given is none: a nil list would open an administrator. The
// user's id comes back whenever they were opened, with the invitation's
// error when it could not be sent, so the panel can name whom it opened.
func (s *AccountSurface) InviteUser(
	ctx context.Context, invitedBy, email, firstName, lastName string, scopes []string,
) (string, error) {
	user, err := s.svc.CreateUser(ctx, CreateUserInput{
		Email: email, FirstName: firstName, LastName: lastName, Scopes: append([]string{}, scopes...),
	}, "")
	if err != nil {
		return "", err
	}

	return user.ID, s.svc.InviteUser(ctx, user.ID, invitedBy)
}

// ResendInvitation sends the user a new invitation from the operator, which
// replaces the one pending (ADR 0348).
func (s *AccountSurface) ResendInvitation(ctx context.Context, userID, invitedBy string) error {
	return s.svc.InviteUser(ctx, userID, invitedBy)
}
