package webhookout

import "github.com/bdrtr/gobit/core/webhook"

// The signature scheme and the names a receiver reads moved to core/webhook
// (ADR 0435), whose package documentation is the rule a receiver implements
// and whose names the sender signs and heads every delivery with. This plugin
// had exported them for the receiver, while ADR 0026 promises core/ alone; a
// receiver that built against these names was building against no promise.
//
// They stay here as aliases, so a receiver built against them keeps compiling
// and is told by its linter where they went. Removing them is a breaking change
// of its own, and nothing dates it.
//
// Each is a constant, a type alias or a function that calls the new one, and
// none is a variable. An exported package variable holding VerifySignature
// would be a security check that any package importing this one could replace
// for every caller.

// SignatureVersion is [webhook.SignatureVersion].
//
// Deprecated: use [webhook.SignatureVersion] (ADR 0435).
const SignatureVersion = webhook.SignatureVersion

// The header names a receiver reads, as aliases.
const (
	// HeaderEvent is [webhook.HeaderEvent].
	//
	// Deprecated: use [webhook.HeaderEvent] (ADR 0435).
	HeaderEvent = webhook.HeaderEvent
	// HeaderDelivery is [webhook.HeaderDelivery].
	//
	// Deprecated: use [webhook.HeaderDelivery] (ADR 0435).
	HeaderDelivery = webhook.HeaderDelivery
	// HeaderAttempt is [webhook.HeaderAttempt].
	//
	// Deprecated: use [webhook.HeaderAttempt] (ADR 0435).
	HeaderAttempt = webhook.HeaderAttempt
	// HeaderTimestamp is [webhook.HeaderTimestamp].
	//
	// Deprecated: use [webhook.HeaderTimestamp] (ADR 0435).
	HeaderTimestamp = webhook.HeaderTimestamp
	// HeaderSignature is [webhook.HeaderSignature].
	//
	// Deprecated: use [webhook.HeaderSignature] (ADR 0435).
	HeaderSignature = webhook.HeaderSignature
)

// SignedFields is [webhook.SignedFields], and the same type: a value built
// under the old name is passed to the new functions as it is.
//
// Deprecated: use [webhook.SignedFields] (ADR 0435).
type SignedFields = webhook.SignedFields

// Sign is [webhook.Sign].
//
// Deprecated: use [webhook.Sign] (ADR 0435).
func Sign(secret string, f SignedFields) string { return webhook.Sign(secret, f) }

// VerifySignature is [webhook.VerifySignature].
//
// Deprecated: use [webhook.VerifySignature] (ADR 0435).
func VerifySignature(secret, header string, f SignedFields) bool {
	return webhook.VerifySignature(secret, header, f)
}
