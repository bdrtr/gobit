package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"io"

	"github.com/bdrtr/gobit/core/errors"
)

// This file is the ONE place this module encrypts anything, and it exists
// because one secret cannot be hashed.
//
// Everything else the auth module stores is a value it only has to RECOGNIZE: a
// password is argon2id, an API key and an invitation token are SHA-256, and none
// of them can be read back out of the database. A TOTP secret is different in
// kind — verifying a six-digit code means recomputing it, which means holding the
// secret the authenticator app holds.
//
// So it is stored encrypted, and the key comes from the installation. What that
// buys is precise and worth stating as precisely: it defends against a database
// that is read WITHOUT the process — a stolen backup, a replica handed to an
// analyst, an injection that can select. It does not defend against a compromised
// host, because a process that can decrypt is a process an attacker who owns it
// can also use.

// mfaKeyInfo separates the encryption key from the configured string.
//
// The stored key is text an operator can type; AES needs exactly 32 bytes. The
// derivation is a plain SHA-256 of the configured value with a domain separator,
// which is enough for what it is: a KDF's stretching work defends a low-entropy
// password, and this value is a deployment secret held to the same length rule as
// the signing one.
const mfaKeyInfo = "gobit.auth.mfa.secret.v1\x00"

// secretBox seals and opens a module secret with AES-GCM.
//
// A zero value is unusable on purpose: [newSecretBox] is the only way to get one,
// and it refuses a key the installation did not set.
type secretBox struct {
	aead cipher.AEAD
}

// newSecretBox derives the key and builds the cipher.
//
// An empty key is NOT an error here — it produces a zero box, and the caller asks
// [secretBox.ready]. The distinction matters because a module whose installation
// has no MFA key must still start: what is refused is the enrollment, at the
// moment somebody asks for one, with a message that says which variable is
// missing.
func newSecretBox(key string) (secretBox, error) {
	if key == "" {
		return secretBox{}, nil
	}

	derived := sha256.Sum256(append([]byte(mfaKeyInfo), key...))

	block, err := aes.NewCipher(derived[:])
	if err != nil {
		return secretBox{}, errors.Internal(CodeMFAUnavailable,
			"the MFA encryption key could not be prepared: %v", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return secretBox{}, errors.Internal(CodeMFAUnavailable,
			"the MFA cipher could not be prepared: %v", err)
	}

	return secretBox{aead: aead}, nil
}

// ready reports whether the installation configured a key.
func (b secretBox) ready() bool { return b.aead != nil }

// seal encrypts the plaintext and returns nonce || ciphertext.
//
// The nonce is random per call and stored in front of the ciphertext, which is
// the shape GCM's own documentation recommends: a nonce reused with one key lets
// an attacker recover the XOR of two plaintexts, and a counter would have to be
// persisted somewhere that survives a restart.
func (b secretBox) seal(plaintext []byte) ([]byte, error) {
	if !b.ready() {
		return nil, errors.Internal(CodeMFAUnavailable,
			"this installation has no MFA encryption key, so nothing can be sealed")
	}

	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, errors.Internal(CodeMFAUnavailable,
			"a nonce could not be drawn: %v", err)
	}

	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// open decrypts what [secretBox.seal] produced.
//
// A value that does not open is an error rather than an empty result, and the
// message says what it means: the key changed. Reading it as "no secret" would
// turn a rotated key into an account that silently has no second factor.
func (b secretBox) open(sealed []byte) ([]byte, error) {
	if !b.ready() {
		return nil, errors.Internal(CodeMFAUnavailable,
			"this installation has no MFA encryption key, so nothing can be opened")
	}
	if len(sealed) <= b.aead.NonceSize() {
		return nil, errors.Internal(CodeMFASecretUnreadable,
			"the stored MFA secret is too short to carry a nonce")
	}

	nonce, ciphertext := sealed[:b.aead.NonceSize()], sealed[b.aead.NonceSize():]

	plaintext, err := b.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.Internal(CodeMFASecretUnreadable,
			"the stored MFA secret could not be opened; the encryption key is not the "+
				"one it was sealed with (MFA_SECRET_KEY)")
	}

	return plaintext, nil
}
