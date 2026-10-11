package models

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// A completion secret answers the storefront's completion of a payment it
// opened once the provider's report completed the cart (ADR 0438, ADR 0439).
//
// A completed cart is the unknown cart's 404 at the storefront, and the cart's
// id and the session's id are ids, which gobit's logs and the shop's pages
// carry. The secret is handed to the storefront in the opening's answer
// alone; the module keeps its digest, and the completion that presents it is
// answered once with the order and the order's access token.

// CompletionSecretPrefix is the prefix of a completion secret, for leak
// scanners, as the order token's "ordtok_" is.
const CompletionSecretPrefix = "cpsec_"

// completionSecretEntropyBytes is the byte count of a secret's random body:
// 256 bits, the width of the stored digest, so the digest needs no slow hash.
const completionSecretEntropyBytes = 32

// NewCompletionSecret returns a new secret and the digest the module keeps.
func NewCompletionSecret() (plaintext string, digest []byte, err error) {
	body := make([]byte, completionSecretEntropyBytes)
	if _, err := rand.Read(body); err != nil {
		return "", nil, err
	}
	plaintext = CompletionSecretPrefix + base64.RawURLEncoding.EncodeToString(body)

	return plaintext, CompletionSecretDigest(plaintext), nil
}

// CompletionSecretDigest is the digest the module keeps of a secret.
func CompletionSecretDigest(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))

	return sum[:]
}
