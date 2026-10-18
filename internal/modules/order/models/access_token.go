package models

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// An order's access token opens the order at the storefront (ADR 0439).
//
// The order id does not: gobit hands it to every webhook receiver of an order
// topic, so it is a name rather than a key. A token is issued to the one who
// completed the cart, in the completion's answer, and to every mail that names
// the order; each issue is a new token, and the order keeps only the digest of
// each.

// AccessTokenPrefix is the prefix of an order access token.
//
// It is for leak scanners, as the API keys' "sk_" is: a string starting with
// it found in a log or a repository is an order's access.
const AccessTokenPrefix = "ordtok_"

// accessTokenEntropyBytes is the byte count of a token's random body: 256
// bits, the width of the stored digest, so the digest needs no slow hash (the
// API keys' reasoning, auth's models.HashToken).
const accessTokenEntropyBytes = 32

// AccessTokenDigestBytes is the length of the stored digest, SHA-256's.
const AccessTokenDigestBytes = sha256.Size

// NewAccessToken returns a new token and the digest the order keeps of it.
//
// The plaintext is the only copy and the caller hands it to its one recipient;
// nothing in this module stores it.
func NewAccessToken() (plaintext string, digest []byte, err error) {
	body := make([]byte, accessTokenEntropyBytes)
	if _, err := rand.Read(body); err != nil {
		return "", nil, err
	}
	plaintext = AccessTokenPrefix + base64.RawURLEncoding.EncodeToString(body)

	return plaintext, AccessTokenDigest(plaintext), nil
}

// AccessTokenDigest is the digest an order keeps of a token.
func AccessTokenDigest(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))

	return sum[:]
}

// AccessTokenMatches reports whether presented is a token whose digest is
// among digests.
//
// The comparison is constant-time over every digest and stops at none of
// them, so the answer's timing says neither which digest matched nor how much
// of one did. An empty token matches nothing: it is a request that presented
// none, not a token.
func AccessTokenMatches(presented string, digests [][]byte) bool {
	if presented == "" {
		return false
	}
	want := AccessTokenDigest(presented)

	matched := 0
	for _, digest := range digests {
		matched |= subtle.ConstantTimeCompare(want, digest)
	}

	return matched == 1
}

// A transfer's token is made and compared as an access token is (ADR 0450):
// it answers an order's transfer request and is mailed to the order's address.

// NewTransferToken returns a new transfer token and the digest a transfer
// keeps of it, made as an access token is (256 random bits, SHA-256).
func NewTransferToken() (plaintext string, digest []byte, err error) {
	body := make([]byte, accessTokenEntropyBytes)
	if _, err := rand.Read(body); err != nil {
		return "", nil, err
	}
	plaintext = TransferTokenPrefix + base64.RawURLEncoding.EncodeToString(body)

	return plaintext, AccessTokenDigest(plaintext), nil
}

// TransferTokenMatches reports whether presented is the token whose digest the
// transfer keeps, in constant time. An empty token and a transfer holding no
// digest match nothing.
func (t OrderTransfer) TransferTokenMatches(presented string) bool {
	if presented == "" || len(t.TokenHash) == 0 {
		return false
	}

	return subtle.ConstantTimeCompare(AccessTokenDigest(presented), t.TokenHash) == 1
}
