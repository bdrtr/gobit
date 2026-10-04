//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// TestAnOperatorClosesOneSessionAndThenTheOthers is ADR 0267 on the production
// wiring: three sign-ins are three sessions, the listing marks the one the
// request is made with, closing one refuses that token alone, and closing the
// others leaves only the current one.
func TestAnOperatorClosesOneSessionAndThenTheOthers(t *testing.T) {
	ctx := context.Background()
	email := fmt.Sprintf("sessions-%d@gobit.test", fixtureCounter.Add(1))
	const password = "sessions-password-42"
	_, err := authSvc.CreateUser(ctx, authsvc.CreateUserInput{
		Email: email, FirstName: "Many", LastName: "Devices", Scopes: []string{"product:read"},
	}, password)
	require.NoError(t, err)

	laptop, tablet, phone := obtainToken(t, email, password), obtainToken(t, email, password), obtainToken(t, email, password)

	type listed struct {
		ID      string `json:"id"`
		Current bool   `json:"current"`
	}
	list := func(token string) []listed {
		rec := tokenedRequest(t, http.MethodGet, authapi.SessionsPath, token, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var page struct {
			Data []listed `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
		return page.Data
	}
	me := func(token string) int {
		return tokenedRequest(t, http.MethodGet, "/admin/v1/auth/me", token, "").Code
	}

	sessions := list(phone)
	require.Len(t, sessions, 3, "each sign-in is a session")
	current := 0
	for _, s := range sessions {
		if s.Current {
			current++
		}
	}
	assert.Equal(t, 1, current, "exactly the session the request was made with is current")

	// The laptop is the oldest, so it is listed last.
	laptopSession := sessions[len(sessions)-1].ID
	revoked := tokenedRequest(t, http.MethodPost,
		authapi.SessionsPath+"/"+laptopSession+"/revoke", phone, "")
	require.Equal(t, http.StatusNoContent, revoked.Code, revoked.Body.String())
	assert.Equal(t, http.StatusUnauthorized, me(laptop), "the closed session's token is refused")
	assert.Equal(t, http.StatusOK, me(tablet), "the others go on")

	again := tokenedRequest(t, http.MethodPost, authapi.SessionsPath+"/"+laptopSession+"/revoke", phone, "")
	assert.Equal(t, http.StatusNotFound, again.Code)
	assert.Contains(t, again.Body.String(), authsvc.CodeSessionNotOpen)

	others := tokenedRequest(t, http.MethodPost, authapi.SessionsRevokeOthersPath, phone, "")
	require.Equal(t, http.StatusOK, others.Code, others.Body.String())
	assert.Contains(t, others.Body.String(), `"revoked":1`)
	assert.Equal(t, http.StatusUnauthorized, me(tablet))
	assert.Equal(t, http.StatusOK, me(phone), "the current session stays")
	assert.Len(t, list(phone), 1)

	byKey := tokenedRequest(t, http.MethodGet, authapi.SessionsPath, secretKey, "")
	assert.Equal(t, http.StatusUnprocessableEntity, byKey.Code, "a key is a machine with no sessions")
	assert.Contains(t, byKey.Body.String(), authapi.CodeSessionsNotAPerson)
}
