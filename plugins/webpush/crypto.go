package webpush

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// The two RFCs this file implements, and why by hand.
//
// RFC 8291 encrypts the payload so that only the browser that minted the
// subscription can read it — the push service forwards a blob it cannot open.
// RFC 8292 (VAPID) proves to the push service which application server is
// sending, so an endpoint captured from one site cannot be used to push from
// another.
//
// Both are done with the standard library: crypto/ecdh, crypto/hkdf,
// crypto/ecdsa and crypto/aes. No dependency is added, following the decision
// ADR 0014 recorded for the error reporters and this repository has followed
// for every plugin since.
//
// # Why the test comes first here
//
// Every mistake in this file has the SAME symptom: the push service answers
// 201, the browser receives an event it cannot decrypt, and nothing reports a
// fault. A swapped key order, a reused salt, a signature one byte short — none
// of them produces an error on the sending side. crypto_test.go therefore pins
// the derivation against the RFC 8291 Appendix A vector, which is the only
// expected value in this package that was not produced by reading this code.

// The aes128gcm content encoding's fixed sizes (RFC 8188).
const (
	// saltLength is the per-message salt.
	saltLength = 16
	// recordSizeLength is the width of the record size field.
	recordSizeLength = 4
	// keyIDLength is an uncompressed P-256 point: 0x04 followed by X and Y.
	keyIDLength = 65
	// headerLength is salt + record size + the key id length byte + the key.
	headerLength = saltLength + recordSizeLength + 1 + keyIDLength
	// authSecretLength is the length of the subscription's auth secret.
	authSecretLength = 16
	// gcmTagLength is the authentication tag AES-GCM appends.
	gcmTagLength = 16
	// delimiterLength is the single 0x02 byte that ends the only record.
	delimiterLength = 1
)

// recordSize is the record size announced in the header.
//
// 4096 is what browsers accept and what every push service sizes its queue for.
// The message is sent as ONE record: a multi-record body is legal and buys
// nothing here, because a push payload that does not fit one record does not
// fit the push service's own limit either.
const recordSize = 4096

// MaxPayloadBytes is the largest plaintext that fits a single record.
//
// It is the record size minus everything the encoding puts around the payload:
// the header, the delimiter byte and the GCM tag. Announcing it as a constant
// rather than discovering it at send time is deliberate — a payload that does
// not fit has to fail where the template is rendered, not after a device list
// has already been walked.
const MaxPayloadBytes = recordSize - headerLength - delimiterLength - gcmTagLength

// The HKDF info strings. Each is terminated by a NUL byte, and the NUL is part
// of the string rather than an accident of C: RFC 8188 defines the info as a
// label followed by a zero octet.
const (
	infoContentEncryptionKey = "Content-Encoding: aes128gcm\x00"
	infoNonce                = "Content-Encoding: nonce\x00"
	infoKeyPrefix            = "WebPush: info\x00"
)

// vapidLifetime is how long a signed token stays valid.
//
// RFC 8292 caps it at 24 hours and services reject anything longer outright.
// Twelve hours is half of that: a token is minted per send, so a long lifetime
// buys nothing, while a value at the cap fails whenever the two clocks disagree
// by a second.
const vapidLifetime = 12 * time.Hour

// Error codes.
const (
	codePayloadTooLarge = "webpush_payload_too_large"
	codeEncryptFailed   = "webpush_encrypt_failed"
	codeBadEndpoint     = "webpush_endpoint_invalid"
	codeSignFailed      = "webpush_vapid_sign_failed"
)

// sealInput carries everything one encryption needs.
//
// The salt and the ephemeral key are PARAMETERS rather than generated inside,
// for one reason: the Appendix A vector fixes both, and a function that mints
// them itself can only be tested against itself. [encrypt] is the caller that
// generates them.
type sealInput struct {
	plaintext  []byte
	salt       []byte
	ephemeral  *ecdh.PrivateKey
	uaPublic   *ecdh.PublicKey
	authSecret []byte
}

// encrypt produces the aes128gcm body for one subscription.
//
// A fresh salt and a fresh ephemeral keypair are minted per call, and that is
// the whole of the nonce-reuse defense: both the content encryption key and the
// nonce are derived from them, so reusing either across two messages to the
// same device reuses an AES-GCM nonce — which is a total break, and which
// delivers perfectly while it happens.
func encrypt(plaintext []byte, uaPublic *ecdh.PublicKey, authSecret []byte) ([]byte, error) {
	if len(plaintext) > MaxPayloadBytes {
		return nil, coreerrors.Invalid(codePayloadTooLarge,
			"the push payload is %d bytes; at most %d fit one record", len(plaintext), MaxPayloadBytes)
	}

	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeEncryptFailed,
			"the message salt could not be produced")
	}

	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeEncryptFailed,
			"the ephemeral key pair could not be produced")
	}

	return seal(sealInput{
		plaintext:  plaintext,
		salt:       salt,
		ephemeral:  ephemeral,
		uaPublic:   uaPublic,
		authSecret: authSecret,
	})
}

// seal runs the RFC 8291 derivation and produces the body.
//
// The order of what follows is the specification's and none of it is
// negotiable; the Appendix A vector in the test is what proves each step.
func seal(in sealInput) ([]byte, error) {
	shared, err := in.ephemeral.ECDH(in.uaPublic)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeEncryptFailed,
			"the shared secret could not be computed; the subscription's public key is not on P-256")
	}

	asPublic := in.ephemeral.PublicKey().Bytes()
	// The length goes into a single header byte, so it has to fit one. A P-256
	// uncompressed point is always 65 bytes; checking rather than converting
	// blindly means a future curve change fails here instead of writing a
	// truncated length that no browser can parse.
	if len(asPublic) != keyIDLength {
		return nil, coreerrors.Internal(codeEncryptFailed,
			"the ephemeral public key is %d bytes; the header carries %d",
			len(asPublic), keyIDLength)
	}

	// The initial keying material binds the shared secret to BOTH public keys,
	// which is what stops a captured endpoint from being pushed to with a
	// different server key. The user agent's key comes first; swapping the two
	// produces a body no browser can open, and the push service still says 201.
	ikm, err := hkdf.Key(sha256.New, shared, in.authSecret,
		string(keyInfo(in.uaPublic.Bytes(), asPublic)), sha256.Size)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeEncryptFailed,
			"the initial keying material could not be derived")
	}

	prk, err := hkdf.Extract(sha256.New, ikm, in.salt)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeEncryptFailed,
			"the pseudo-random key could not be extracted")
	}

	cek, err := hkdf.Expand(sha256.New, prk, infoContentEncryptionKey, 16)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeEncryptFailed,
			"the content encryption key could not be derived")
	}

	nonce, err := hkdf.Expand(sha256.New, prk, infoNonce, 12)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeEncryptFailed,
			"the nonce could not be derived")
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeEncryptFailed,
			"the cipher could not be built")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeEncryptFailed,
			"the AEAD could not be built")
	}

	// The record is the plaintext followed by a single 0x02 delimiter, which is
	// RFC 8188's marker for "this is the LAST record". No padding is added: it
	// would hide the payload's length from an observer who can already see the
	// body's total size.
	record := make([]byte, 0, len(in.plaintext)+delimiterLength)
	record = append(record, in.plaintext...)
	record = append(record, 0x02)

	var body bytes.Buffer
	body.Grow(headerLength + len(record) + gcmTagLength)
	body.Write(in.salt)
	_ = binary.Write(&body, binary.BigEndian, uint32(recordSize))
	body.WriteByte(keyIDLength)
	body.Write(asPublic)
	body.Write(aead.Seal(nil, nonce, record, nil))

	return body.Bytes(), nil
}

// keyInfo builds the RFC 8291 key derivation context.
//
// The USER AGENT's key comes first. This is the single most consequential byte
// order in the plugin and the reason the Appendix A vector is in the test
// suite: reversed, everything still runs and nothing can read the result.
func keyInfo(uaPublic, asPublic []byte) []byte {
	info := make([]byte, 0, len(infoKeyPrefix)+len(uaPublic)+len(asPublic))
	info = append(info, infoKeyPrefix...)
	info = append(info, uaPublic...)
	info = append(info, asPublic...)

	return info
}

// vapidHeader builds the Authorization header value for one endpoint.
//
// A token is minted per request rather than cached per endpoint. Caching would
// save an ECDSA signature — microseconds — and would need an expiry the cache
// gets wrong exactly once, at which point every push to that service starts
// failing with a 401 and the cause is a clock.
func vapidHeader(endpoint string, key *ecdsa.PrivateKey, subject string, now time.Time) (string, error) {
	audience, err := audienceOf(endpoint)
	if err != nil {
		return "", err
	}

	header, err := json.Marshal(map[string]string{"typ": "JWT", "alg": "ES256"})
	if err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindInternal, codeSignFailed,
			"the token header could not be encoded")
	}
	claims, err := json.Marshal(map[string]any{
		"aud": audience,
		"exp": now.Add(vapidLifetime).Unix(),
		"sub": subject,
	})
	if err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindInternal, codeSignFailed,
			"the token claims could not be encoded")
	}

	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(claims)

	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindInternal, codeSignFailed,
			"the token could not be signed")
	}

	// ES256 is the raw pair, each half padded to 32 bytes. FillBytes rather
	// than Bytes: Bytes drops leading zeros, so about one signature in 256 is
	// short and is rejected — an intermittent 401 that reads as a flaky push
	// service and cannot be reproduced on demand.
	//
	// ecdsa.SignASN1 is the other trap: it produces the DER structure every
	// other Go ECDSA caller wants, and every push service rejects it.
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])

	token := signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)

	publicKey, err := key.PublicKey.Bytes()
	if err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindInternal, codeSignFailed,
			"the server public key could not be encoded")
	}

	return "vapid t=" + token + ", k=" + base64.RawURLEncoding.EncodeToString(publicKey), nil
}

// audienceOf reduces an endpoint to the origin the token is addressed to.
//
// Scheme and host, nothing else. The full endpoint URL is the natural thing to
// put here and it is wrong: services check the origin, and the mismatch answers
// 401 — which sends whoever debugs it to rotate a key that was never the
// problem.
func audienceOf(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindInvalid, codeBadEndpoint,
			"the push endpoint could not be parsed")
	}
	if u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", coreerrors.Invalid(codeBadEndpoint,
			"a push endpoint has to be an absolute http(s) URL")
	}

	return u.Scheme + "://" + u.Host, nil
}

// publicKeyOf returns the application server key a browser passes as
// applicationServerKey when it subscribes.
//
// It is DERIVED from the private key rather than configured separately, and
// that removes a whole failure class: two settings that must agree eventually
// disagree, and when they do every existing subscription keeps working while
// every new one is minted against a key the server does not hold.
func publicKeyOf(key *ecdsa.PrivateKey) (string, error) {
	raw, err := key.PublicKey.Bytes()
	if err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindInternal, codeSignFailed,
			"the server public key could not be encoded")
	}

	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// fingerprintOf identifies which signing key a subscription was minted under.
//
// It is stored with every subscription, and it exists because of the one piece
// of durable state this plugin holds that no migration and no backup covers:
// the VAPID private key. Rotate it and every subscription ever issued becomes
// permanently unusable — the push service answers 401 forever, and 401 must
// never delete a row (a temporary auth fault would otherwise wipe the table).
//
// The fingerprint turns that invisible graveyard into a countable one: rows
// minted under a vanished key can only ever fail, so they are deleted with a
// warning, and the count is logged at startup.
func fingerprintOf(publicKey string) string {
	sum := sha256.Sum256([]byte(publicKey))

	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

// decodeKey decodes a base64url value a browser produced.
//
// Browsers differ on padding, so both forms are accepted. What is NOT relaxed
// is the length and curve check the caller does afterwards: a subscription
// stored with a malformed key fails on every send forever, and the only place
// that can be reported usefully is the subscribe request itself.
func decodeKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if raw, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return raw, nil
	}
	if raw, err := base64.URLEncoding.DecodeString(value); err == nil {
		return raw, nil
	}

	return nil, coreerrors.Invalid(codeBadEndpoint, "the value is not base64url")
}
