package config

import (
	"fmt"
	"net/url"
	"strings"
)

// validateAdminSurface holds the admin surface's browser origins and its
// sign-in quota (ADR 0451, ADR 0452).
//
// An admin origin is refused unless it is one: a scheme, http or https, and a
// host, with nothing after them. An entry with a path or a trailing word never
// matches the Origin a browser sends, so the panel it was written for would
// be turned away with nothing saying why; "*" is refused because a site that
// may read admin answers is one an operator named.
func (c Config) validateAdminSurface() error {
	for _, origin := range c.AdminCORSAllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		if origin == "*" {
			return fmt.Errorf("config: ADMIN_CORS_ALLOWED_ORIGINS cannot be \"*\"; name each " +
				"site whose pages may call the admin surface")
		}
		u, err := url.Parse(strings.TrimRight(origin, "/"))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return fmt.Errorf("config: ADMIN_CORS_ALLOWED_ORIGINS holds %q, which is not an origin "+
				"(scheme://host[:port])", origin)
		}
	}
	if c.AdminSignInLimit < 0 {
		return fmt.Errorf("config: ADMIN_SIGN_IN_LIMIT cannot be negative, %d given; zero turns "+
			"the sign-in quota off", c.AdminSignInLimit)
	}
	if c.AdminSignInLimit > 0 && c.AdminSignInWindow <= 0 {
		return fmt.Errorf("config: ADMIN_SIGN_IN_WINDOW has to be positive while "+
			"ADMIN_SIGN_IN_LIMIT is on, %s given", c.AdminSignInWindow)
	}

	return nil
}
