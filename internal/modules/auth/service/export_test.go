package service

import "time"

// This file opens the TOTP primitives to the package's external tests.
//
// The alternative was to compute the expected code inside the test with the same
// arithmetic, which would be a test of the implementation against a copy of
// itself. The RFC's vectors are checked in the INTERNAL test next door
// (totp_test.go); what the external tests need is a way to produce the code an
// authenticator would show, so that enrolling and confirming can be exercised
// end to end.

// TOTPCodeAt is the code an authenticator holding the secret shows at that moment.
func TOTPCodeAt(secret string, at time.Time) (string, error) {
	counter := uint64(at.Unix()) / uint64(totpStep.Seconds())

	return totpCode(secret, counter)
}
