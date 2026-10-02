package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
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
		user := &page.Items[i]
		scopes := user.Scopes
		if scopes == nil {
			scopes = []string{}
		}
		out = append(out, panelUser{
			ID: user.ID, Email: user.Email, FirstName: user.FirstName, LastName: user.LastName,
			Scopes: scopes, SecondFactor: holders[user.ID], CreatedAt: user.CreatedAt,
		})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, 0, err
	}

	return body, page.Count, nil
}
