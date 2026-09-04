package webpush

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The crypto here is the one part of this plugin that cannot be checked by
// reading it. Every mistake in RFC 8291 produces the same symptom — the push
// service answers 201, the browser receives an event it cannot decrypt, and
// nothing anywhere reports a fault. A wrong key_info byte order, a reused salt
// and a truncated signature are all invisible from the sending side.
//
// So the expected values below are NOT this package's output. They are the test
// vector printed in RFC 8291 Appendix A, and reproducing it byte for byte is
// the only evidence that the derivation is the one browsers implement.

// The RFC 8291 Appendix A vector.
const (
	vecPlaintext  = "When I grow up, I want to be a watermelon"
	vecSalt       = "DGv6ra1nlYgDCS1FRnbzlw"
	vecASPrivate  = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	vecASPublic   = "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"
	vecUAPublic   = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	vecAuthSecret = "BTBZMqHH6r4Tts7J_aSIgg"
	// The whole aes128gcm body: header (salt, record size, key id) followed by
	// the sealed record.
	vecBody = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6" +
		"TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

// b64 decodes an unpadded base64url value from the vector.
func b64(t *testing.T, s string) []byte {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(s)
	require.NoError(t, err)

	return raw
}

// TestTheAppendixAVectorIsReproduced is the load-bearing test of this package.
//
// It pins the whole derivation at once: the ECDH, the key_info string and the
// ORDER of the two public keys inside it, the HKDF chain with its two info
// strings, the header layout, the 0x02 record delimiter and the AES-128-GCM
// seal. Any one of them being wrong changes the output, and none of them
// produces an error when wrong.
func TestTheAppendixAVectorIsReproduced(t *testing.T) {
	asPriv, err := ecdh.P256().NewPrivateKey(b64(t, vecASPrivate))
	require.NoError(t, err)
	uaPub, err := ecdh.P256().NewPublicKey(b64(t, vecUAPublic))
	require.NoError(t, err)

	body, err := seal(sealInput{
		plaintext:  []byte(vecPlaintext),
		salt:       b64(t, vecSalt),
		ephemeral:  asPriv,
		uaPublic:   uaPub,
		authSecret: b64(t, vecAuthSecret),
	})

	require.NoError(t, err)
	assert.Equal(t, vecBody, base64.RawURLEncoding.EncodeToString(body),
		"the aes128gcm body does not match RFC 8291 Appendix A; the derivation is not the one browsers implement")
}

// TestTheKeyInfoOrderMatters proves the vector test is actually pinning the
// byte order, rather than passing by luck.
//
// The user agent's key comes FIRST in key_info. Swapping the two produces a
// valid-looking body that no browser can decrypt, and the push service still
// answers 201.
func TestTheKeyInfoOrderMatters(t *testing.T) {
	ua := b64(t, vecUAPublic)
	as := b64(t, vecASPublic)

	correct := keyInfo(ua, as)
	swapped := keyInfo(as, ua)

	assert.NotEqual(t, correct, swapped)
	assert.True(t, strings.HasPrefix(string(correct), "WebPush: info\x00"),
		"key_info starts with the label and a NUL")
	assert.Equal(t, append(append([]byte("WebPush: info\x00"), ua...), as...), correct,
		"the user agent's key comes first")
}

// TestEverySealUsesAFreshSaltAndEphemeralKey is the nonce-reuse guard.
//
// The content encryption key and the nonce are both derived from the salt and
// the ephemeral key. Reusing either across two messages to the same device
// reuses an AES-GCM nonce, which is a total break of the encryption — and
// every push still arrives, so nothing looks wrong.
func TestEverySealUsesAFreshSaltAndEphemeralKey(t *testing.T) {
	uaPub, err := ecdh.P256().NewPublicKey(b64(t, vecUAPublic))
	require.NoError(t, err)
	auth := b64(t, vecAuthSecret)

	const runs = 50
	salts := make(map[string]struct{}, runs)
	keyIDs := make(map[string]struct{}, runs)

	for range runs {
		body, err := encrypt([]byte("the same plaintext every time"), uaPub, auth)
		require.NoError(t, err)
		require.Greater(t, len(body), headerLength)

		salts[string(body[:saltLength])] = struct{}{}
		keyIDs[string(body[saltLength+recordSizeLength+1:headerLength])] = struct{}{}
	}

	assert.Len(t, salts, runs, "every message must carry its own salt")
	assert.Len(t, keyIDs, runs, "every message must carry its own ephemeral public key")
}

// TestAPayloadThatCannotFitIsRefused proves an oversized payload is an error
// rather than a truncation.
//
// A truncated body decrypts to broken JSON in the service worker, which shows
// up as "the notification sometimes does not appear" — a fault with no trace on
// the server at all.
func TestAPayloadThatCannotFitIsRefused(t *testing.T) {
	uaPub, err := ecdh.P256().NewPublicKey(b64(t, vecUAPublic))
	require.NoError(t, err)

	_, err = encrypt(make([]byte, MaxPayloadBytes+1), uaPub, b64(t, vecAuthSecret))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "payload")
}

// TestTheLargestAllowedPayloadStillFits proves the limit is not off by one.
func TestTheLargestAllowedPayloadStillFits(t *testing.T) {
	uaPub, err := ecdh.P256().NewPublicKey(b64(t, vecUAPublic))
	require.NoError(t, err)

	body, err := encrypt(make([]byte, MaxPayloadBytes), uaPub, b64(t, vecAuthSecret))

	require.NoError(t, err)
	assert.LessOrEqual(t, len(body), recordSize,
		"the encrypted body must fit the record size announced in its own header")
}

// --- VAPID (RFC 8292) --------------------------------------------------------

// newVAPIDKey mints a signing key for the tests.
func newVAPIDKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	return key
}

// TestTheAudienceIsTheOriginOnly pins the claim push services actually check.
//
// Putting the full endpoint URL in `aud` produces a 401 that reads as a key
// problem, and sends whoever debugs it to rotate a key that was never wrong.
func TestTheAudienceIsTheOriginOnly(t *testing.T) {
	for name, endpoint := range map[string]string{
		"mozilla": "https://updates.push.services.mozilla.com/wpush/v2/gAAAAABlongopaquetoken",
		"fcm":     "https://fcm.googleapis.com/fcm/send/dQw4w9WgXcQ:APA91bHqRs",
		"apple":   "https://web.push.apple.com/QBCbBiKZ0hE3vJ0zGqM?query=1",
		"port":    "https://push.example.test:8443/push/abc",
	} {
		t.Run(name, func(t *testing.T) {
			aud, err := audienceOf(endpoint)

			require.NoError(t, err)
			assert.NotContains(t, aud, "/wpush")
			assert.NotContains(t, aud, "?")
			assert.True(t, strings.HasPrefix(aud, "https://"))
			assert.Equal(t, 2, strings.Count(aud, "/"),
				"the audience is scheme://host and nothing else, got %q", aud)
		})
	}
}

// TestTheSignatureIsAlwaysSixtyFourBytes is the FillBytes guard.
//
// ES256 is raw r||s, each padded to 32 bytes. Using big.Int.Bytes() instead
// drops leading zeros, so roughly one signature in 256 is short — an
// intermittent 401 that reads as a flaky push service and is nearly impossible
// to reproduce on purpose.
//
// A thousand runs make a miss almost certain to be caught: the chance of no
// short signature in 1000 tries is about 0.04%.
func TestTheSignatureIsAlwaysSixtyFourBytes(t *testing.T) {
	key := newVAPIDKey(t)

	for range 1000 {
		header, err := vapidHeader("https://push.example.test/p/abc", key, "mailto:ops@example.test", time.Now())
		require.NoError(t, err)

		jwt := tokenOf(t, header)
		parts := strings.Split(jwt, ".")
		require.Len(t, parts, 3)

		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		require.NoError(t, err)
		require.Len(t, sig, 64,
			"an ES256 signature is exactly 64 bytes; a short one is an intermittent 401")
	}
}

// TestTheSignatureVerifiesAsRawRS proves the signature is in the encoding push
// services parse, not ASN.1.
//
// ecdsa.SignASN1 produces a 70-72 byte DER structure that every push service
// rejects. It is the natural thing to reach for and it is wrong here.
func TestTheSignatureVerifiesAsRawRS(t *testing.T) {
	key := newVAPIDKey(t)

	header, err := vapidHeader("https://push.example.test/p/abc", key, "mailto:ops@example.test", time.Now())
	require.NoError(t, err)

	jwt := tokenOf(t, header)
	parts := strings.Split(jwt, ".")
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)

	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])

	assert.True(t, ecdsa.Verify(&key.PublicKey, digest[:], r, s),
		"the signature has to verify as raw r||s")
}

// TestTheClaimsCarryWhatAPushServiceRequires pins the three claims.
//
// `sub` is treated as required rather than optional: RFC 8292 lets a service
// demand it, no test here can ask a real one, and omitting it fails at exactly
// one service with a 401 that says nothing.
func TestTheClaimsCarryWhatAPushServiceRequires(t *testing.T) {
	key := newVAPIDKey(t)
	now := time.Date(2026, 3, 14, 9, 26, 53, 0, time.UTC)

	header, err := vapidHeader("https://push.example.test/p/abc", key, "mailto:ops@example.test", now)
	require.NoError(t, err)

	parts := strings.Split(tokenOf(t, header), ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)

	var claims map[string]any
	require.NoError(t, json.Unmarshal(raw, &claims))

	assert.Equal(t, "https://push.example.test", claims["aud"])
	assert.Equal(t, "mailto:ops@example.test", claims["sub"])
	require.Contains(t, claims, "exp")

	rawExp, ok := claims["exp"].(float64)
	require.True(t, ok, "exp has to be a number")
	exp := int64(rawExp)
	assert.Greater(t, exp, now.Unix())
	assert.LessOrEqual(t, exp, now.Add(24*time.Hour).Unix(),
		"RFC 8292 caps the lifetime at 24 hours; a longer one is rejected outright")
}

// TestTheHeaderCarriesTheServerPublicKey proves the k= field is present and is
// the uncompressed point a browser can match against applicationServerKey.
func TestTheHeaderCarriesTheServerPublicKey(t *testing.T) {
	key := newVAPIDKey(t)

	header, err := vapidHeader("https://push.example.test/p/abc", key, "mailto:ops@example.test", time.Now())
	require.NoError(t, err)

	require.True(t, strings.HasPrefix(header, "vapid t="))
	_, after, found := strings.Cut(header, ", k=")
	require.True(t, found, "the header has to carry the k= field: %q", header)

	pub, err := base64.RawURLEncoding.DecodeString(after)
	require.NoError(t, err)
	assert.Len(t, pub, 65, "an uncompressed P-256 point is 65 bytes")
	assert.Equal(t, byte(0x04), pub[0], "an uncompressed point starts with 0x04")
}

// TestAnUnparseableEndpointIsRefused proves a bad endpoint fails at signing
// rather than producing a header aimed at nothing.
func TestAnUnparseableEndpointIsRefused(t *testing.T) {
	for _, endpoint := range []string{"", "not a url", "/relative/only", "ftp://push.example.test/p"} {
		_, err := audienceOf(endpoint)
		require.Error(t, err, "%q must not produce an audience", endpoint)
	}
}

// tokenOf extracts the JWT from an Authorization header value.
func tokenOf(t *testing.T, header string) string {
	t.Helper()

	rest := strings.TrimPrefix(header, "vapid t=")
	jwt, _, found := strings.Cut(rest, ", k=")
	require.True(t, found, "malformed header: %q", header)

	return jwt
}
