package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// The revoked tabs of the panel's API keys screen (ADR 0350).
const (
	// KeysOpen lists the keys still accepted.
	KeysOpen = "open"
	// KeysRevoked lists the keys revoked.
	KeysRevoked = "revoked"
)

// panelKey is one API key as the panel's API keys screen lists it (ADR
// 0350), its token only as redacted; the json tags are the contract with
// the panel, which cannot import this package.
type panelKey struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Title      string     `json:"title"`
	Redacted   string     `json:"redacted"`
	Scopes     []string   `json:"scopes"`
	CreatedBy  string     `json:"created_by"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	RevokedBy  string     `json:"revoked_by"`
	CreatedAt  time.Time  `json:"created_at"`
}

// APIKeysJSON lists the API keys, the newest first, a page at a time, with
// how many there are: those still accepted when revoked is [KeysOpen], those
// revoked when it is [KeysRevoked], and every one when it is empty (ADR
// 0350).
func (s *AccountSurface) APIKeysJSON(ctx context.Context, revoked string, limit, offset int32) (json.RawMessage, int64, error) {
	in := ListAPIKeysInput{Limit: int64(limit), Offset: int64(offset)}
	switch revoked {
	case "":
	case KeysOpen, KeysRevoked:
		closed := revoked == KeysRevoked
		in.Revoked = &closed
	default:
		return nil, 0, errors.Invalid(CodeInvalidInput, "unknown api key tab: %q", revoked)
	}

	page, err := s.svc.ListAPIKeys(ctx, in)
	if err != nil {
		return nil, 0, err
	}
	out := make([]panelKey, 0, len(page.Items))
	for i := range page.Items {
		key := &page.Items[i]
		scopes := key.Scopes
		if scopes == nil {
			scopes = []string{}
		}
		out = append(out, panelKey{
			ID: key.ID, Type: string(key.Type), Title: key.Title, Redacted: key.Redacted, Scopes: scopes,
			CreatedBy: key.CreatedBy, LastUsedAt: key.LastUsedAt, RevokedAt: key.RevokedAt,
			RevokedBy: key.RevokedBy, CreatedAt: key.CreatedAt,
		})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, 0, err
	}

	return body, page.Count, nil
}

// RevokeAPIKey revokes the key in the operator's name; a revoked key is
// never accepted again, and stays listed with when and by whom (ADR 0350).
func (s *AccountSurface) RevokeAPIKey(ctx context.Context, id, revokedBy string) error {
	_, err := s.svc.RevokeAPIKey(ctx, id, revokedBy)

	return err
}
