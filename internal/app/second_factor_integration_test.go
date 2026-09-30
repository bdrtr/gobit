//go:build integration

package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/internal/core/config"
)

// secondFactorAdmin is the bootstrap administrator this file signs in as.
const (
	secondFactorEmail    = "owes-a-factor@gobit.test"
	secondFactorPassword = "second-factor-bootstrap-42"
)

// TestAnInstallationRequiringAFactorLetsItsOperatorsEnrollAndNothingElse is
// ADR 0265 on the production wiring: with the requirement's moment passed, the
// bootstrap administrator, who holds every privilege and no factor, signs in,
// is told they owe one, is refused the catalog, enrolls through the same
// session, and after signing in with the code holds their privileges again and
// is listed among the users who hold a factor.
func TestAnInstallationRequiringAFactorLetsItsOperatorsEnrollAndNothingElse(t *testing.T) {
	ctx := context.Background()

	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", migrateDSN(t))
	t.Setenv("JWT_SECRET", "second-factor-integration-secret-32-bytes")
	t.Setenv("MFA_SECRET_KEY", "second-factor-integration-mfa-key")
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", secondFactorEmail)
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", secondFactorPassword)
	t.Setenv("ADMIN_SECOND_FACTOR_REQUIRED_FROM", "2026-01-01T00:00:00Z")
	t.Setenv("LOG_LEVEL", "warn")

	cfg, err := config.Load()
	require.NoError(t, err)
	log := slog.New(slog.DiscardHandler)
	app, closeApp, err := openApplication(ctx, cfg, log, errorreport.NewSink(), Options{}, publishesOnly)
	require.NoError(t, err)
	defer closeApp()
	handler, err := assemble(ctx, cfg, log, app, Options{})
	require.NoError(t, err)

	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	login := func(code string) string {
		body := fmt.Sprintf(`{"email":%q,"password":%q,"code":%q}`, secondFactorEmail, secondFactorPassword, code)
		rec := call(http.MethodPost, "/admin/v1/auth/login", "", body)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var envelope struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		return envelope.Data.Token
	}
	whoami := func(token string) (scopes []string, owed bool) {
		rec := call(http.MethodGet, "/admin/v1/auth/me", token, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var envelope struct {
			Data struct {
				Scopes []string `json:"scopes"`
				Owed   bool     `json:"second_factor_owed"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		return envelope.Data.Scopes, envelope.Data.Owed
	}

	owing := login("")
	scopes, owed := whoami(owing)
	assert.True(t, owed, "the administrator has no factor and the moment has passed")
	assert.Empty(t, scopes, "a person who owes a factor holds no privilege")
	refused := call(http.MethodGet, "/admin/v1/products", owing, "")
	assert.Equal(t, http.StatusForbidden, refused.Code, "the catalog is closed to them: %s", refused.Body.String())

	enrolled := call(http.MethodPost, "/admin/v1/auth/mfa", owing, "")
	require.Equal(t, http.StatusOK, enrolled.Code, "enrolling is open to them: %s", enrolled.Body.String())
	var enrolment struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(enrolled.Body.Bytes(), &enrolment))
	confirmed := call(http.MethodPost, "/admin/v1/auth/mfa/confirm", owing,
		fmt.Sprintf(`{"code":%q}`, totpCode(t, enrolment.Data.Secret, time.Now())))
	require.Equal(t, http.StatusNoContent, confirmed.Code, confirmed.Body.String())

	proven := login(totpCode(t, enrolment.Data.Secret, time.Now()))
	scopes, owed = whoami(proven)
	assert.False(t, owed)
	assert.NotEmpty(t, scopes, "a proven factor gives the privileges back")
	assert.Equal(t, http.StatusOK, call(http.MethodGet, "/admin/v1/products", proven, "").Code)

	listed := func(secondFactor string) []string {
		rec := call(http.MethodGet, "/admin/v1/users?second_factor="+secondFactor, proven, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var page struct {
			Data []struct {
				Email        string `json:"email"`
				SecondFactor bool   `json:"second_factor"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
		var emails []string
		for _, user := range page.Data {
			assert.Equal(t, secondFactor, fmt.Sprint(user.SecondFactor), "the record says what the filter kept")
			emails = append(emails, user.Email)
		}
		return emails
	}
	assert.Contains(t, listed("true"), secondFactorEmail)
	assert.NotContains(t, listed("false"), secondFactorEmail)
}

// totpCode is the code an authenticator holding the base32 secret shows at
// `at`: RFC 6238, HMAC-SHA1 over the thirty-second step, six digits. It is a
// second implementation on purpose, so the module's arithmetic is not checked
// against itself.
func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()

	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	require.NoError(t, err)
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	return fmt.Sprintf("%06d", value%1_000_000)
}
