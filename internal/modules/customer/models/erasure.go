package models

import "strings"

// AnonymousEmailDomain is the domain every anonymized customer address ends in.
//
// The ".invalid" top-level domain is reserved by RFC 2606 and is guaranteed
// never to be delegated, so an address built on it can never route anywhere: a
// mail server handed one fails to resolve it instead of delivering it to
// whoever happens to own a real domain. That guarantee is the whole reason a
// reserved TLD is used rather than a plausible one. The column cannot be
// emptied — its CHECK constraints demand a syntactically valid address even
// after the erasure — so what stays behind is a value that LOOKS like a
// mailbox, and a value that looks like a mailbox is exactly what a newsletter
// export or a password-reset job picks up.
const AnonymousEmailDomain = "@erased.invalid"

// AnonymousPlaceholder is the text written into the address columns that are
// NOT NULL and CHECKed non-empty.
//
// customer_address.address_1 and .city cannot be blanked the way the rest of
// the address is: customer_address_address1_check and
// customer_address_city_check refuse an empty string
// (migrations/000001_customer_init.up.sql). A placeholder is the only value
// that satisfies the schema while holding nothing about the person. It is a
// readable word rather than a dash or an X so that anyone looking at the row
// can see what happened to it instead of guessing that the data was lost.
const AnonymousPlaceholder = "anonymized"

// AnonymousEmail is the address that replaces a customer's e-mail when the
// record is anonymized.
//
// It is DERIVED FROM THE CUSTOMER ID, and every part of that shape is forced by
// the schema rather than chosen for looks:
//
//   - The column is NOT NULL and customer_email_check refuses the empty string,
//     so it can be neither nulled nor blanked: something has to be written
//     there.
//   - CHECK (email = lower(email)) folds the case, so the id — Crockford Base32,
//     always upper case — is lowered here. That alphabet has no two characters
//     that fold onto each other, so lowering keeps distinct ids distinct.
//   - CHECK (email ~ '^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$') demands
//     a non-empty local part, exactly one @ and a dotted domain. The prefixed id
//     carries no space and no @, and "erased.invalid" is a dotted domain.
//   - CHECK (length(email) <= 320) is met with room to spare: an id is 31
//     characters ("cust_" plus 26) and the domain 15, so the address is 46.
//   - customer_account_email_uniq is UNIQUE (email) WHERE has_account AND
//     deleted_at IS NULL. One shared constant would therefore collide on the
//     SECOND registered account ever erased, and the erasure would fail with a
//     conflict — on a person who asked to be forgotten. Deriving from the
//     primary key makes that collision impossible.
//
// The derivation is a rule about ONE column and nothing more. It is
// deliberately NOT the module's answer to "has this row been erased already":
// an anonymized record stays live and is patched column by column afterwards,
// so a row can carry this address and a freshly written first name at the same
// time. That question is answered where it can be answered honestly — in the
// UPDATE's own WHERE, against every column the erasure writes (see
// queries/customer.sql, AnonymizeCustomer). Both answers stay free of a schema
// change: no "anonymized_at" column has to be added, written and then trusted.
func AnonymousEmail(customerID string) string {
	return strings.ToLower(customerID) + AnonymousEmailDomain
}

// ErasureCount is what one anonymization pass found and what it wrote.
type ErasureCount struct {
	// Matched is the number of customer rows the subject resolved to, whether or
	// not those rows still held anything.
	//
	// It is kept apart from [ErasureCount.Rewritten] because "this person was
	// never here" and "this person was here and is already anonymous" are two
	// different answers, and a single counter would report both as zero.
	Matched int
	// Rewritten is the number of rows this pass actually overwrote, across the
	// customer and customer_address tables together.
	//
	// It is a receipt for THIS call and not a description of what the person
	// left behind: a second erasure of the same subject finds every row already
	// anonymous, writes nothing and reports zero. Counting untouched rows again
	// would produce a report claiming work that did not happen.
	Rewritten int
}
