package service

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// signingMethod is the ONLY accepted signing method of session tokens.
//
// HS256 was chosen because the side that produces the token and the side that
// verifies it are the same process; an asymmetric signature (RS256) carries
// value only when the verifier must not be able to sign, and there is no such
// party here.
var signingMethod = jwt.SigningMethodHS256

// tokenClaims are the claims a session token carries.
//
// The registered claims (sub, exp, iat, iss) are embedded; scopes is the only
// custom claim this module adds. NEITHER the email address NOR the name is PUT
// into the token: the body of the token is signed but IT IS NOT ENCRYPTED and
// anyone who can decode base64 can read it. The least information needed for
// the scope decision is carried.
type tokenClaims struct {
	// Scopes are the caller's privileges.
	Scopes []string `json:"scopes"`
	jwt.RegisteredClaims
}

// issueToken produces a signed session token for the user.
//
// The claims that are filled in: sub (the user identifier), scopes, iat, exp,
// iss. "nbf" is deliberately not written — the token is valid the moment it is
// produced and a session that starts in the future has no counterpart in this
// flow.
//
// If there is no signing secret errors.Unavailable is returned: producing no
// token at all is right, rather than producing an unsigned one or one with a
// fixed secret.
func (s *Service) issueToken(userID string, scopes []string, now time.Time) (string, time.Time, error) {
	if len(s.secret) == 0 {
		return "", time.Time{}, errors.Unavailable(CodeSecretMissing,
			"the JWT signing secret is not configured; a session token cannot be produced")
	}

	expiresAt := now.Add(s.tokenTTL)
	claims := tokenClaims{
		Scopes: scopes,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    s.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	signed, err := jwt.NewWithClaims(signingMethod, claims).SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, errors.Wrap(err, errors.KindInternal, CodeTokenInvalid,
			"the session token could not be signed")
	}
	return signed, expiresAt, nil
}

// parsedToken is the information a verified token carries.
type parsedToken struct {
	// Subject is the identifier of the user who owns the token.
	Subject string
	// Scopes are the privileges the token carries.
	Scopes []string
	// IssuedAt is the moment the token was produced ("iat", UTC).
	//
	// The field is not merely information, it is the ground of session
	// REVOCATION: when the password changes, tokens produced before this moment
	// are rejected (see [parsedToken.issuedBefore] and
	// [Service.principalFromToken]). Its resolution is SECONDS.
	IssuedAt time.Time
}

// issuedBefore reports that the token was DEFINITELY produced before the given
// moment.
//
// # Why "definitely"
//
// The "iat" claim has second resolution (jwt.TimePrecision): a token signed at
// 10:00:00.900 carries 10:00:00. That is, the real moment the token was
// produced is not a single point but the RANGE from iat up to (but not
// including) iat+1s, and if that range falls in the same second as the moment
// being compared against, which one came first CANNOT BE READ from the token.
//
// The ambiguous second is resolved in favor of USABILITY: "before" is said only
// if the whole range is before the moment. The opposite choice — rejecting the
// ambiguous second too, in favor of security — would drop the FRESH token of a
// user who changes their password and logs in right away: the login returns
// 200, the first request made with that token gets a 401, and the user would
// have to wait a second and try again. Setup scripts (create user, assign
// password, log in) run in exactly this interval, so the deviation is not rare
// but TYPICAL.
//
// The accepted price is that a token produced in the SAME second as the moment
// survives. In the scenario the revocation targets — a token leaked minutes or
// hours ago — no such collision exists; for an attacker to profit from it, they
// would have had to obtain the token WITHIN the second in which the victim
// changed the password.
func (p parsedToken) issuedBefore(moment time.Time) bool {
	return !p.IssuedAt.Add(jwt.TimePrecision).After(moment)
}

// parseToken verifies the token and returns its claims.
//
// # The attacks that are rejected
//
// The verification does the following four things EXPLICITLY, and none of the
// four is left to the library's default:
//
//  1. "alg: none" and ALGORITHM CONFUSION. jwt.WithValidMethods accepts only
//     HS256; on top of that, the signing method being *jwt.SigningMethodHMAC is
//     checked once more inside keyfunc. Both gates are necessary: should the
//     method list be loosened one day, the second gate still prevents an
//     attacker from writing "alg: RS256" and using the HMAC secret as a public
//     key.
//  2. EXPIRY. jwt.WithExpirationRequired rejects a token that HAS NO "exp"
//     claim. Without this condition, producing a token with no lifetime would
//     be possible merely by omitting a field, without capturing the signature.
//  3. THE ISSUER. jwt.WithIssuer prevents a token produced by another system
//     with the same secret from being accepted here.
//  4. THE ABSENCE OF THE ISSUE TIME. The "iat" claim is treated as MANDATORY.
//     The library's jwt.WithIssuedAt option validates the claim only IF IT IS
//     THERE (that it is not in the future); it passes over its absence silently
//     and the value would fall to zero. Session revocation rests on this value
//     (see [Service.principalFromToken]) and a zero "iat" gives the right result
//     BY COINCIDENCE today: the zero time is before every password change, so
//     the token is rejected. The check is still written explicitly so that the
//     rejection rests on a RULE and not on a coincidence — should the
//     comparison change one day (if, for example, a convenience such as "skip
//     the check when there is no iat" is added), a missing claim must not turn
//     into silent acceptance.
//
// The signature itself is compared in constant time by the library.
//
// The verification DOES NOT END in this function: whether the user who owns the
// token still exists and whether the token was produced BEFORE the password
// change are asked on the calling side (see interop.go).
func (s *Service) parseToken(raw string) (parsedToken, error) {
	if len(s.secret) == 0 {
		return parsedToken{}, errors.Unavailable(CodeSecretMissing,
			"the JWT signing secret is not configured; a session token cannot be verified")
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{signingMethod.Alg()}),
		jwt.WithIssuer(s.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(0),
		jwt.WithTimeFunc(s.clock),
	)

	var claims tokenClaims
	token, err := parser.ParseWithClaims(raw, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		// The detail of the error (was it expired, did the signature not hold)
		// DOES NOT GO to the client; the caller logs it. The difference would
		// tell an attacker at which stage their attempt got stuck.
		return parsedToken{}, errors.Wrap(err, errors.KindUnauthorized, CodeTokenInvalid,
			"the session token is invalid")
	}
	if !token.Valid {
		return parsedToken{}, errors.Unauthorized(CodeTokenInvalid, "the session token is invalid")
	}
	if claims.Subject == "" {
		return parsedToken{}, errors.Unauthorized(CodeTokenInvalid,
			"the session token carries no user identifier")
	}
	if claims.IssuedAt == nil {
		return parsedToken{}, errors.Unauthorized(CodeTokenInvalid,
			"the session token carries no issue time")
	}

	return parsedToken{
		Subject:  claims.Subject,
		Scopes:   claims.Scopes,
		IssuedAt: claims.IssuedAt.UTC(),
	}, nil
}
