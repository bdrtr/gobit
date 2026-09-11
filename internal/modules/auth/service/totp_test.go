package service

import (
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file checks the implementation against RFC 6238 rather than against
// itself.
//
// An implementation tested with codes it produced is a test that a function is
// deterministic. The RFC publishes test vectors precisely so that does not have
// to be the standard of proof, and the vectors below are its Appendix B — the
// SHA-1 rows, which are the ones this module computes and the ones every
// authenticator app computes.

// rfc6238Secret is the ASCII seed the RFC's vectors use: "12345678901234567890".
//
// The RFC states it as ASCII and TOTP takes base32, so it is encoded here. A
// test that hard-coded the base32 string would be hiding the one step where an
// implementation most often goes wrong.
var rfc6238Secret = base32.StdEncoding.WithPadding(base32.NoPadding).
	EncodeToString([]byte("12345678901234567890"))

// TestTheCodesAreTheONESRFC6238Publishes is the standard, executed.
func TestTheCodesAreTheONESRFC6238Publishes(t *testing.T) {
	t.Parallel()

	// Appendix B, the SHA-1 column. The RFC prints eight digits; TOTP as every
	// authenticator implements it shows six, which is the LAST six of the same
	// number — the truncation is the same and only the modulo differs.
	for _, tc := range []struct {
		seconds int64
		want    string
	}{
		{seconds: 59, want: "287082"},
		{seconds: 1111111109, want: "081804"},
		{seconds: 1111111111, want: "050471"},
		{seconds: 1234567890, want: "005924"},
		{seconds: 2000000000, want: "279037"},
	} {
		counter := uint64(tc.seconds) / uint64(totpStep.Seconds())

		got, err := totpCode(rfc6238Secret, counter)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got,
			"RFC 6238 Appendix B says the code at T=%d is %s", tc.seconds, tc.want)
	}
}

// TestACodeIsAcceptedOnlyAroundNow pins the window.
//
// One step either side, so ninety seconds in total: a person reading a code at
// the end of its window types it into the next one, and every extra step widens
// how long an intercepted code stays usable.
func TestACodeIsAcceptedOnlyAroundNow(t *testing.T) {
	t.Parallel()

	now := time.Unix(1111111111, 0).UTC()
	counter := uint64(now.Unix()) / uint64(totpStep.Seconds())

	for _, tc := range []struct {
		name string
		step int64
		want bool
	}{
		{name: "the code for now", step: 0, want: true},
		{name: "the one just before", step: -1, want: true},
		{name: "the one just after", step: 1, want: true},
		{name: "two steps back is too late", step: -2, want: false},
		{name: "two steps ahead is too early", step: 2, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			code, err := totpCode(rfc6238Secret, uint64(int64(counter)+tc.step))
			require.NoError(t, err)

			matches, err := totpMatches(rfc6238Secret, code, now)
			require.NoError(t, err)
			assert.Equal(t, tc.want, matches)
		})
	}
}

// TestAnythingThatIsNotSixDigitsIsRefused covers what a person types wrong.
//
// It is checked BEFORE the HMAC, so a pasted word costs nothing, and it is not a
// typed error: a wrong code and a malformed one are the same answer to somebody
// guessing (see CodeMFACodeWrong).
func TestAnythingThatIsNotSixDigitsIsRefused(t *testing.T) {
	t.Parallel()

	now := time.Unix(1111111111, 0).UTC()

	for _, code := range []string{"", "1", "12345", "1234567", "abcdef", "  "} {
		matches, err := totpMatches(rfc6238Secret, code, now)

		require.NoError(t, err, "a malformed code is not a fault of the installation")
		assert.False(t, matches, "%q must not match", code)
	}
}

// TestTheCodeIsPaddedToSixDigits is the leading-zero case.
//
// One of the RFC's own vectors is "005924", and an implementation that formatted
// the number without padding would answer "5924" — which no authenticator shows
// and no person could type.
func TestTheCodeIsPaddedToSixDigits(t *testing.T) {
	t.Parallel()

	code, err := totpCode(rfc6238Secret, uint64(1234567890)/uint64(totpStep.Seconds()))

	require.NoError(t, err)
	assert.Equal(t, "005924", code)
	assert.Len(t, code, totpDigits)
}

// TestASecretIsDrawnFreshEveryTime is what makes re-enrolling meaningful.
func TestASecretIsDrawnFreshEveryTime(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for range 16 {
		secret, err := newTOTPSecret()
		require.NoError(t, err)
		require.False(t, seen[secret], "a drawn secret repeated: %q", secret)
		seen[secret] = true

		raw, err := totpEncoding.DecodeString(secret)
		require.NoError(t, err, "a secret has to be the base32 an app can read")
		assert.Len(t, raw, totpSecretBytes)
	}
}

// TestTheURIIsWhatAnAuthenticatorScans pins the parameters apps actually read.
func TestTheURIIsWhatAnAuthenticatorScans(t *testing.T) {
	t.Parallel()

	uri := otpauthURI("Acme Shop", "ada@example.com", rfc6238Secret)

	parsed, err := url.Parse(uri)
	require.NoError(t, err)
	assert.Equal(t, "otpauth", parsed.Scheme)
	assert.Equal(t, "totp", parsed.Host)

	label, err := url.PathUnescape(strings.TrimPrefix(parsed.Path, "/"))
	require.NoError(t, err)
	assert.Equal(t, "Acme Shop:ada@example.com", label,
		"the label is issuer:account, and older apps read the issuer from HERE")

	query := parsed.Query()
	assert.Equal(t, rfc6238Secret, query.Get("secret"))
	assert.Equal(t, "Acme Shop", query.Get("issuer"),
		"and newer ones read it from the parameter; the two have to agree")
	assert.Equal(t, "SHA1", query.Get("algorithm"))
	assert.Equal(t, "6", query.Get("digits"))
	assert.Equal(t, "30", query.Get("period"))
}

// TestAnIssuerWithAColonCannotSplitTheLabel is the injection this label has.
//
// The label is `issuer:account`. An issuer carrying a colon would make the app
// read the rest as the account name, so a shop called "a:b" would show every
// administrator an account that is not theirs.
func TestAnIssuerWithAColonCannotSplitTheLabel(t *testing.T) {
	t.Parallel()

	uri := otpauthURI(mfaIssuer("Acme: Shop"), "ada@example.com", rfc6238Secret)

	parsed, err := url.Parse(uri)
	require.NoError(t, err)

	label, err := url.PathUnescape(strings.TrimPrefix(parsed.Path, "/"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(label, ":"),
		"the label may carry exactly one colon, the one that separates it: %q", label)
}

// TestANamelessIssuerFallsBackRatherThanRefusing keeps a cosmetic fault cosmetic.
func TestANamelessIssuerFallsBackRatherThanRefusing(t *testing.T) {
	t.Parallel()

	assert.Equal(t, defaultMFAIssuer, mfaIssuer("   "))
	assert.Equal(t, defaultMFAIssuer, mfaIssuer(""))
}
