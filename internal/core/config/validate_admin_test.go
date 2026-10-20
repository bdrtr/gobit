package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheAdminSurfacesSettingsAreHeld is ADR 0451 and ADR 0452: an admin
// origin is an origin and never the wildcard, and the sign-in quota is off at
// zero, refused below it and needs a window while on. The defaults load as
// the record states them.
func TestTheAdminSurfacesSettingsAreHeld(t *testing.T) {
	base := validConfig(t)
	assert.Empty(t, base.AdminCORSAllowedOrigins, "no admin CORS unless an operator lists a site")
	assert.Equal(t, 20, base.AdminSignInLimit)
	assert.Equal(t, 15*time.Minute, base.AdminSignInWindow)

	for _, origins := range [][]string{
		{"https://admin.example.com"}, {"http://localhost:5173", " https://satici.example.com/ "}, {""},
		{"https://admin.example.com:8443"}, {"https://xn--ynetim-zua.example.com"},
	} {
		cfg := base
		cfg.AdminCORSAllowedOrigins = origins
		require.NoError(t, cfg.Validate(), "%q", origins)
	}
	for _, origin := range []string{
		"*", "admin.example.com", "https://admin.example.com/panel", "ftp://admin.example.com",
		"https://admin.example.com?x=1", "https://user@admin.example.com", "https://",
		"https://admin.example.com:443", "http://admin.example.com:80", "https://b\u00fccher.example.com",
	} {
		cfg := base
		cfg.AdminCORSAllowedOrigins = []string{origin}
		assert.Error(t, cfg.Validate(), "%q was taken", origin)
	}

	cfg := base
	cfg.AdminSignInLimit = 0
	cfg.AdminSignInWindow = 0
	require.NoError(t, cfg.Validate(), "zero turns the quota off and needs no window")
	cfg.AdminSignInLimit = -1
	assert.Error(t, cfg.Validate())
	cfg.AdminSignInLimit = 10
	assert.Error(t, cfg.Validate(), "a quota with no window")
}
