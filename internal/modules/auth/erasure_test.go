package auth_test

import (
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/auth"
	"github.com/bdrtr/gobit/internal/schemaaudit"
)

// These tests need NO database and no setup. A declaration is a property of the
// code rather than of the data — the same sentences on an empty installation as
// on a full one — which is why [personaldata.Declarer] takes no context and returns
// no error, and why the audit of it is a plain unit test anybody can run.
//
// The audit is written against the MIGRATION and not against the declaration.
// A test that walked the declaration and checked it was well-formed would pass
// on the day somebody adds a "phone" column and declares nothing, which is the
// only failure worth catching here.

// notPersonalColumns lists, per table, the columns that hold nothing about a
// person, and it is the load-bearing half of
// [TestPersonalDataCoversEveryPersonalColumn]: every column NOT named here has
// to appear in the declaration. A column added to the schema tomorrow therefore
// fails this test until somebody decides which side of the line it falls on,
// and that moment of deciding is the whole point — a rule that inferred would
// wave a "date_of_birth" past on the grounds that it looked technical.
//
// The five tables all appear, including the ones with nothing personal in them,
// because the test also refuses a table that is missing from this map. A new
// table is exactly where an undeclared column would hide.
//
// Why each of these is not personal data:
//
//   - IDENTIFIERS (id, user_id, api_key_id, sales_channel_id, created_by,
//     revoked_by). A synthetic key names a ROW, not a person, and it is only a
//     person via the columns beside it — which are declared. Declaring the key
//     as well would double-count the same staff member and send an auditor to a
//     column with no name in it. api_key.created_by and .revoked_by are the same
//     case even though they point at people: they carry a "user_…" identifier
//     with no foreign key, and it resolves through auth_user, which is declared.
//   - ROW LIFECYCLE (created_at, updated_at, deleted_at). These timestamp a
//     WRITE to the row, not an act of the person. auth_identity.updated_at is
//     the honest boundary case, because some of those writes are the person's
//     own sign-ins; it is left out because last_login_at carries that fact
//     explicitly and declaring both would name the same event twice, once in a
//     column that means something else on every other table in the schema.
//   - PRIVILEGES (auth_user.scopes, api_key.scopes, api_key.type,
//     sales_channel.is_disabled). What an account may do is a fact about the
//     account. It identifies nobody, and it is the same value for everyone who
//     holds the same role.
//   - THE AUTHENTICATION METHOD (auth_identity.provider). The value is
//     "emailpass" or a provider's name and it is identical for every staff
//     member who signs in the same way; what identifies the person is
//     provider_identity beside it, which is declared and whose reason says so.
//   - MACHINE SECRETS (api_key.token_hash, api_key.redacted). Both are derived
//     from 256 random bits gobit generated itself, so they are derived from
//     nobody. This is the exact contrast that puts auth_identity.password_hash
//     on the other side of the line, where the input was a secret a person
//     chose.
//   - A MACHINE'S HISTORY (api_key.last_used_at, api_key.revoked_at). A key is
//     an identity that processes and any number of people hold, so its use
//     points at no one person — the contrast that makes
//     auth_identity.last_login_at personal, since an identity row belongs to
//     exactly one named human.
var notPersonalColumns = map[string][]string{
	"auth_user": {
		"id", "scopes", "created_at", "updated_at", "deleted_at",
	},
	"auth_identity": {
		"id", "user_id", "provider", "created_at", "updated_at", "deleted_at",
	},
	// token_hash is the digest of a secret and describes nobody; user_id and
	// invited_by are join keys, on the same side as auth_identity.user_id above;
	// expires_at is created_at plus a constant and says nothing created_at does
	// not. What IS declared is created_at, because the row's existence is the fact
	// about a person and a date is where that fact lives.
	"auth_user_invitation": {
		"token_hash", "user_id", "invited_by", "expires_at",
	},
	// user_id is a join key, the same side as auth_identity.user_id. secret is a
	// CIPHERTEXT of a TOTP seed: it describes nobody — it is a random twenty bytes
	// that happens to be shared with a phone — and it is declared nowhere because
	// an auditor sent to it would find a value with no meaning outside the
	// algorithm. pending_secret is the same ciphertext for a replacement not yet
	// proven (ADR 0264), added by ALTER TABLE and unread by this audit until
	// D187. What IS declared is confirmed_at, because "this person proved a
	// second factor, and when" is a fact about them.
	"auth_mfa_credential": {
		"user_id", "secret", "created_at", "pending_secret",
	},
	// id is a random identifier the token names and user_id a join key;
	// expires_at is created_at plus the token's lifetime and says nothing
	// created_at does not. What IS declared is created_at and revoked_at: when
	// a person signed in, and when they closed one sign-in by itself.
	"auth_session": {
		"id", "user_id", "expires_at",
	},
	"sales_channel": {
		"id", "is_disabled", "created_at", "updated_at", "deleted_at",
	},
	"api_key": {
		"id", "type", "token_hash", "redacted", "scopes", "created_by",
		"last_used_at", "revoked_at", "revoked_by", "created_at", "updated_at",
		"deleted_at",
	},
	"api_key_sales_channel": {
		"api_key_id", "sales_channel_id", "created_at",
	},
}

// TestPersonalDataCoversEveryPersonalColumn proves the declaration is
// EXHAUSTIVE against the schema it describes, in BOTH directions.
//
// The declaration is the only answer an embedder has to "where is this person
// in your database", and it is what that embedder publishes as its privacy
// notice. A missing row does not make the list shorter — it makes it FALSE, and
// the column it forgot is one no audit will ever look at. A row pointing at a
// column that does not exist is the mirror fault: it sends somebody answering a
// data-subject request hunting for data nobody holds.
func TestPersonalDataCoversEveryPersonalColumn(t *testing.T) {
	// internal/schemaaudit replays every migration, the ALTER TABLE this
	// audit's own reader once missed included (D187), and holds every
	// exemption to a column the schema has.
	module := auth.New(auth.Options{})
	schemaaudit.Cover(t, module.Migrations(), module.PersonalData(), notPersonalColumns)
}

// TestTheDeclarationReadsAsAnAnswerToAPerson checks the shape of the text
// rather than the list.
//
// Every holding lands in one report beside the other modules' — the invoice's,
// the customer's, the saga store's — and a controller reads that report out to
// a person. So a holding with no Kind says nothing about who wrote the column,
// a holding with no Why cannot be repeated to anybody, and a Why that opens
// with a capital letter is a sentence that was written for a Go file rather
// than for the answer it ends up inside (ADR 0033).
func TestTheDeclarationReadsAsAnAnswerToAPerson(t *testing.T) {
	for _, holding := range auth.New(auth.Options{}).PersonalData().Holdings {
		key := holding.Table + "." + holding.Column

		assert.Contains(t, []personaldata.Kind{personaldata.Named, personaldata.Open}, holding.Kind,
			"%s: a holding with no kind says nothing about whether gobit or the shop wrote it", key)

		require.NotEmpty(t, holding.Why,
			"%s: a column named without a reason cannot be repeated to a data subject", key)
		first, _ := utf8.DecodeRuneInString(holding.Why)
		assert.True(t, unicode.IsLower(first),
			"%s: the reason opens with %q; these clauses are read side by side inside "+
				"one report and start lower-case", key, first)
	}
}

// TestTheHolderIsLeftForTheCoordinator pins the empty Holder.
//
// The coordinator overwrites [personaldata.Declaration.Holder] with the name the
// module registry knows this module by, so writing "auth" here would be a
// second copy of that name with nothing keeping the two together. The failure
// it would eventually produce is quiet: a report naming a holder no registry
// entry matches, read by somebody trying to find out who still holds the data.
func TestTheHolderIsLeftForTheCoordinator(t *testing.T) {
	assert.Empty(t, auth.New(auth.Options{}).PersonalData().Holder)
}

// TestTheAuthModuleOffersNoErasure pins the decision, not the omission.
//
// This module's data subject is a STAFF MEMBER. A shopper asking to be
// forgotten must never reach an administrator's account, and an eraser here is
// all it would take for a colliding e-mail address to delete an operator — the
// customer and auth tables are joined nowhere that would catch it (ADR 0033).
// The sweep finds an eraser BY TYPE ASSERTION, so adding the method would be
// enough to wire that up with no other edit anywhere; this assertion is what
// makes that edit stop and read the argument first.
func TestTheAuthModuleOffersNoErasure(t *testing.T) {
	var module any = auth.New(auth.Options{})

	_, ok := module.(personaldata.Eraser)
	assert.False(t, ok,
		"the auth module has grown an Erase method. Its rows belong to staff, not "+
			"to shoppers, so a customer's erasure request would now delete an "+
			"administrator; declaring is how this module answers, and the coordinator "+
			"reports it as RETAINED on every sweep.")

	_, ok = module.(personaldata.Declarer)
	assert.True(t, ok, "the module has to declare, or its staff accounts are invisible in every report")
}
