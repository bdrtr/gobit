package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238's default; HMAC does not rest on collision resistance. See the file comment.
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// This file is RFC 6238, written out rather than taken as a dependency.
//
// The algorithm is thirty lines: an HMAC over a counter, a dynamic truncation,
// a modulo. A library would add a module to the graph of everybody who embeds
// gobit (see the direct-require audit) for code whose specification is four pages
// and whose test vectors are in the RFC — and those vectors are what this file's
// tests use, so the implementation is checked against the standard rather than
// against itself.
//
// # Why SHA-1
//
// RFC 6238 allows SHA-1, SHA-256 and SHA-512, and says SHA-1 is the default. What
// decides it here is not the RFC but the AUTHENTICATOR APPS: the otpauth URI's
// `algorithm` parameter is widely ignored, and an app that ignores it computes
// SHA-1 whatever the URI said. Choosing SHA-256 would produce enrollments that
// look correct and then never verify.
//
// The weakness that broke SHA-1 is collision resistance, and HMAC does not rest
// on it — HMAC-SHA1 has no practical attack and remains what TOTP is built on.
// The gosec suppression on the import is that sentence.

// The shape of the code, which is also the part every authenticator assumes.
const (
	// totpDigits is the length of the code a person types.
	totpDigits = 6
	// totpStep is how long one code lives.
	totpStep = 30 * time.Second
	// totpSecretBytes is the entropy behind a secret.
	//
	// Twenty bytes, which is SHA-1's output length and what RFC 4226 calls the
	// recommended size. It base32-encodes to thirty-two characters, which is what
	// an app shows a person who types it by hand.
	totpSecretBytes = 20
	// totpSkew is how many steps either side of now are accepted.
	//
	// One, so a code is valid for at most ninety seconds. It is not zero because
	// a person reading a code at the end of its window types it into the next
	// one, and it is not larger because every extra step widens the window an
	// intercepted code stays usable in.
	totpSkew = 1
)

// totpEncoding is base32 without padding, which is what otpauth URIs carry.
var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret draws a secret and returns it base32-encoded.
func newTOTPSecret() (string, error) {
	raw := make([]byte, totpSecretBytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", errors.Internal(CodeMFAUnavailable,
			"an MFA secret could not be drawn: %v", err)
	}

	return totpEncoding.EncodeToString(raw), nil
}

// otpauthURI is what an authenticator app scans.
//
// The issuer appears TWICE — once as a path prefix and once as a parameter — and
// that is not a mistake: the parameter is the spec's, the prefix is what older
// apps read, and apps that read both expect them to agree.
func otpauthURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)

	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprint(totpDigits))
	query.Set("period", fmt.Sprint(int(totpStep.Seconds())))

	return "otpauth://totp/" + label + "?" + query.Encode()
}

// totpCode computes the code for one step.
func totpCode(secret string, counter uint64) (string, error) {
	key, err := totpEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", errors.Internal(CodeMFASecretUnreadable,
			"the stored MFA secret is not base32")
	}

	message := make([]byte, 8)
	binary.BigEndian.PutUint64(message, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(message)
	sum := mac.Sum(nil)

	// Dynamic truncation, RFC 4226 section 5.3: the low nibble of the last byte
	// picks where to read four bytes from, and the top bit is masked off so the
	// result is positive on every platform.
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	return fmt.Sprintf("%0*d", totpDigits, value%pow10(totpDigits)), nil
}

// totpMatches reports whether the code is one of the ones valid around now.
//
// The comparison is CONSTANT TIME. A six-digit code is a million possibilities
// and an early-exit compare leaks how many leading digits were right, which turns
// a million into sixty tries; the rate limit in front of the endpoint is the other
// half of that answer and neither is enough alone.
func totpMatches(secret, code string, now time.Time) (bool, error) {
	typed := strings.TrimSpace(code)
	if len(typed) != totpDigits {
		return false, nil
	}

	// The arithmetic is int64 throughout and converts ONCE, below, after the value
	// is known to be non-negative — a moment before 1970 has a negative Unix second
	// and converting that to uint64 would wrap to an enormous counter.
	//
	// There is no separate guard for that moment, and there was one: it returned
	// early on a negative clock and a mutation that deleted it broke nothing,
	// because the `at < 0` check below already skips every step. Two checks for one
	// case is the shape ADR 0142 removed rather than tested.
	step := now.Unix() / int64(totpStep.Seconds())

	for skew := int64(-totpSkew); skew <= totpSkew; skew++ {
		at := step + skew
		if at < 0 {
			continue
		}

		expected, err := totpCode(secret, uint64(at))
		if err != nil {
			return false, err
		}
		if hmac.Equal([]byte(expected), []byte(typed)) {
			return true, nil
		}
	}

	return false, nil
}

// pow10 is ten to the n, for the digit count.
//
// Written out rather than math.Pow, because that returns a float64 and the
// modulo below is integer arithmetic: converting back would round a value near a
// power of ten to the wrong side once in a very long while, and the failure would
// be a code that is rejected for no reason anybody could reproduce.
func pow10(n int) uint32 {
	out := uint32(1)
	for range n {
		out *= 10
	}

	return out
}
