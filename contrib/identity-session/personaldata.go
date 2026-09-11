package identitysession

import (
	"context"
	"strings"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
)

// The module answers the three data-subject capabilities.
//
// They are OPTIONAL and found by type assertion on the registered module (ADR
// 0029), so these pins are the only thing that fails when a signature drifts: a
// module that silently stopped implementing [personaldata.Eraser] would be
// skipped by the sweep, with a green build and a report that looks complete.
var (
	_ personaldata.Eraser    = (*Module)(nil)
	_ personaldata.Declarer  = (*Module)(nil)
	_ personaldata.Discloser = (*Module)(nil)
)

// ErasureHolder is the name this module answers a data-subject request under.
const ErasureHolder = ModuleName

// CodeSubjectEmpty is a data-subject request that names nobody.
const CodeSubjectEmpty = "identity_session_subject_empty"

// PersonalRecords is the OPTIONAL capability a store offers to answer a
// data-subject request.
//
// Separate from [Credentials] for that interface's own reason: it exists so an
// installation can bind LDAP or an existing users table, and such a store must
// not be asked to delete rows out of a table it does not own. A store that does
// not implement this leaves the module answering [personaldata.Retained] with the
// reason.
type PersonalRecords interface {
	// EraseCredentialsOf deletes the credential row of a customer or of an
	// e-mail address, and answers how many rows went.
	EraseCredentialsOf(ctx context.Context, customerID, email string) (int, error)
	// CredentialRecordsOf reads what is held about a customer or an address.
	CredentialRecordsOf(ctx context.Context, customerID, email string) ([]StoredCredential, error)
	// ErasePendingRegistrationOf deletes an unfinished sign-up for an address and
	// answers how many rows went.
	//
	// By ADDRESS only, because a pending registration has no customer: there is no
	// customer yet. A blank address erases nothing rather than everything.
	ErasePendingRegistrationOf(ctx context.Context, email string) (int, error)
	// PendingRegistrationOf reads an unfinished sign-up for a dossier.
	//
	// It answers the moments and never the token or the hash: the token is a live
	// credential for whoever holds it, and the hash is the person's own secret.
	PendingRegistrationOf(ctx context.Context, email string) (*StoredRegistration, error)
}

// StoredCredential is one row as a dossier reports it.
//
// It carries no hash. See [Module.PersonalDataOf] for why the column is declared
// and its value is not reproduced.
type StoredCredential struct {
	// CustomerID is the row's key.
	CustomerID string
	// Email is the address the person signs in with.
	Email string
	// CreatedAt is when the password was first set.
	CreatedAt time.Time
	// UpdatedAt is when it was last changed.
	UpdatedAt time.Time
}

// StoredRegistration is an unfinished sign-up as a dossier reports it.
//
// It carries neither the token nor the password hash. See
// [Module.PersonalDataOf] for the hash's reason; the token's is sharper — it is
// not a record OF something, it is a working link, and a dossier that reproduced
// it would hand somebody's half-open account to whoever carries the answer.
type StoredRegistration struct {
	// Email is the address being proven.
	Email string
	// CreatedAt is when the registration was started.
	CreatedAt time.Time
	// ExpiresAt is when the link stops working.
	ExpiresAt time.Time
}

// The table and columns this module declares.
const (
	tableCustomerCredentials   = "customer_credentials"
	tableCustomerRegistrations = "customer_registrations"

	columnCustomerID   = "customer_id"
	columnEmail        = "email"
	columnPasswordHash = "password_hash"
	columnCreatedAt    = "created_at"
	columnUpdatedAt    = "updated_at"
	columnExpiresAt    = "expires_at"
	columnTokenHash    = "token_hash"
)

// passwordHashNotReproduced is what a dossier says in place of the hash.
//
// The field is PRESENT rather than dropped: a dossier that omitted a column would
// be false about what is held, and one that printed it would hand somebody's
// password hash to whoever carries the answer.
const passwordHashNotReproduced = "a password is set; the stored value is an argon2id hash " +
	"derived from the person's own secret and is deliberately not reproduced here"

// PersonalData says what this module keeps about a person.
func (m *Module) PersonalData() personaldata.Declaration {
	return personaldata.Declaration{
		Holder: ErasureHolder,
		Holdings: []personaldata.Holding{
			{
				Table: tableCustomerCredentials, Column: columnCustomerID,
				Kind: personaldata.Named,
				Why: "the shop's own identifier for the person these credentials sign in, and " +
					"the handle every session cookie this module issues carries",
			},
			{
				Table: tableCustomerCredentials, Column: columnEmail,
				Kind: personaldata.Named,
				Why: "the address the person signs in with, folded to lower case; it is their " +
					"e-mail address and it is also the name they log in under",
			},
			{
				Table: tableCustomerCredentials, Column: columnPasswordHash,
				Kind: personaldata.Named,
				Why: "an argon2id hash of the password this person chose; it cannot be read " +
					"back, but it is derived from a secret of theirs and people reuse " +
					"passwords, so it is pseudonymised personal data rather than none",
			},
			{
				Table: tableCustomerCredentials, Column: columnCreatedAt,
				Kind: personaldata.Named,
				Why: "when this person first set a password here, which is a record of " +
					"something they did at a moment rather than a property of the account",
			},
			{
				Table: tableCustomerCredentials, Column: columnUpdatedAt,
				Kind: personaldata.Named,
				Why:  "when they last changed it, which says they were here and roughly when",
			},
			// The pending-registration table is declared too, and it was NOT until
			// the personal-data audit failed on it. A row there is not an account —
			// nothing about the person exists in the shop yet — but it holds their
			// address and a hash of the password they chose, which is personal data
			// whatever it is a step towards.
			{
				Table: tableCustomerRegistrations, Column: columnEmail,
				Kind: personaldata.Named,
				Why: "an address somebody typed into the sign-up form and has not yet " +
					"proven; it is a claim rather than an account, and it is still their " +
					"address sitting in this shop's database",
			},
			{
				Table: tableCustomerRegistrations, Column: columnPasswordHash,
				Kind: personaldata.Named,
				Why: "an argon2id hash of the password chosen during a registration that was " +
					"never completed; hashed the moment it arrived, so the plaintext never " +
					"outlived that request",
			},
			{
				Table: tableCustomerRegistrations, Column: columnTokenHash,
				Kind: personaldata.Named,
				Why: "the SHA-256 of the link that was sent to that address; it identifies " +
					"the registration rather than the person, and it is declared because a " +
					"controller asked to erase somebody has to know this row is here",
			},
			{
				Table: tableCustomerRegistrations, Column: columnCreatedAt,
				Kind: personaldata.Named,
				Why: "when that registration was started, which is a record of something " +
					"the person did at a moment",
			},
			{
				Table: tableCustomerRegistrations, Column: columnExpiresAt,
				Kind: personaldata.Named,
				Why: "when the link stops working; declared beside the moment above so that " +
					"a controller reading this list sees the whole row rather than part of it",
			},
		},
	}
}

// Erase deletes a person's password credentials.
//
// # It erases the row, not the sessions
//
// A signed cookie is not stored anywhere — that is the whole point of signing it
// (ADR 0127) — so there is no row to delete and no list to walk. What ends a
// session in flight is the key it was signed with, and dropping that logs
// EVERYBODY out, which is not an erasure of one person. The cookie a deleted
// person still holds stops working the moment anything looks the account up, and
// it expires on its own. Reporting the row as deleted and the sessions as
// untouched is the honest answer; claiming a session sweep that cannot exist
// would not be.
func (m *Module) Erase(ctx context.Context, s personaldata.Subject) (personaldata.Result, error) {
	customerID, email, err := subjectHandles(s)
	if err != nil {
		return personaldata.Result{}, err
	}

	records, ok := m.personalRecords()
	if !ok {
		return m.retained("the bound credential store does not offer erasure, so this " +
			"module's credentials are still there"), nil
	}

	rows, err := records.EraseCredentialsOf(ctx, customerID, email)
	if err != nil {
		return personaldata.Result{}, err
	}

	// A PENDING registration is erased too, and forgetting it was a real hole in
	// the slice that added the table: an unfinished sign-up holds the person's
	// address and a hash of their password, and an erasure that took the account
	// and left the claim would have reported a complete deletion. Found by the
	// personal-data audit, which reads the migrations rather than a list somebody
	// kept.
	//
	// It is erased by ADDRESS only. The table has no customer id — there is no
	// customer yet, which is the whole point of it — so a subject named only by a
	// customer id cannot reach a pending row, and the count below says so by not
	// growing.
	pending, err := records.ErasePendingRegistrationOf(ctx, email)
	if err != nil {
		return personaldata.Result{}, err
	}

	m.log.InfoContext(ctx, "identity-session erased a person's credentials",
		"rows", rows+pending, "pending_registrations", pending,
		"by_customer_id", customerID != "", "by_email", email != "")

	return personaldata.Result{
		Holder: ErasureHolder, Outcome: personaldata.Deleted, Rows: rows + pending,
	}, nil
}

// PersonalDataOf assembles what this module holds about a person.
//
// # The hash is declared and its value is not reproduced
//
// Nothing else in this repository puts a password hash in a dossier, and the
// reason is not squeamishness: the value tells the person nothing they do not
// already know — they chose the password — and it is the one field whose escape
// hurts THEM, because a dossier is carried by e-mail, ticket systems and support
// tools. So the column appears with a sentence in place of its contents, which
// keeps the answer complete about what is held without handing it over.
func (m *Module) PersonalDataOf(
	ctx context.Context, s personaldata.Subject,
) (personaldata.Disclosure, error) {
	customerID, email, err := subjectHandles(s)
	if err != nil {
		return personaldata.Disclosure{}, err
	}

	records, ok := m.personalRecords()
	if !ok {
		// Unresolvable rather than Nothing: a holder that searched and found
		// nothing has told the controller something true, and one that could not
		// search has not.
		return personaldata.Disclosure{
			Holder: ErasureHolder, State: personaldata.Unresolvable,
			Why: "the bound credential store does not offer disclosure",
		}, nil
	}

	found, err := records.CredentialRecordsOf(ctx, customerID, email)
	if err != nil {
		return personaldata.Disclosure{}, err
	}
	pending, err := records.PendingRegistrationOf(ctx, email)
	if err != nil {
		return personaldata.Disclosure{}, err
	}
	if len(found) == 0 && pending == nil {
		return personaldata.Disclosure{
			Holder: ErasureHolder, State: personaldata.Nothing,
		}, nil
	}

	out := make([]personaldata.Record, 0, len(found)+1)
	for _, row := range found {
		out = append(out, personaldata.Record{
			Table: tableCustomerCredentials,
			ID:    row.CustomerID,
			Fields: []personaldata.Field{
				{Column: columnCustomerID, Kind: personaldata.Named, Value: row.CustomerID},
				{Column: columnEmail, Kind: personaldata.Named, Value: row.Email},
				{
					Column: columnPasswordHash, Kind: personaldata.Named,
					Value: passwordHashNotReproduced,
				},
				{Column: columnCreatedAt, Kind: personaldata.Named, Value: row.CreatedAt},
				{Column: columnUpdatedAt, Kind: personaldata.Named, Value: row.UpdatedAt},
			},
		})
	}

	// An unfinished sign-up is its own record. Somebody who typed their address
	// into the form and never clicked the link has a row in this shop, and a
	// dossier that showed only completed accounts would be silent about it.
	if pending != nil {
		out = append(out, personaldata.Record{
			Table: tableCustomerRegistrations,
			ID:    pending.Email,
			Fields: []personaldata.Field{
				{Column: columnEmail, Kind: personaldata.Named, Value: pending.Email},
				{Column: columnCreatedAt, Kind: personaldata.Named, Value: pending.CreatedAt},
				{Column: columnExpiresAt, Kind: personaldata.Named, Value: pending.ExpiresAt},
				{
					Column: columnPasswordHash, Kind: personaldata.Named,
					Value: passwordHashNotReproduced,
				},
				{
					Column: columnTokenHash, Kind: personaldata.Named,
					Value: "a sign-up link was sent to this address; the stored value is its " +
						"SHA-256 and is deliberately not reproduced here, because the link " +
						"itself would open the account",
				},
			},
		})
	}

	// The read is recorded and the handles are not: an audit asking who read what
	// finds nothing if the read left no trace, and a log line naming the person
	// beside the sweep is the dossier in miniature.
	m.log.InfoContext(ctx, "identity-session disclosed a person's credentials",
		"records", len(out), "by_customer_id", customerID != "", "by_email", email != "")

	return personaldata.Disclosure{
		Holder: ErasureHolder, State: personaldata.Disclosed, Records: out,
	}, nil
}

// subjectHandles reads the two handles this module can look somebody up by.
//
// The address is folded rather than validated, which is the in-tree modules'
// stance: a malformed address is not a bad request here, it is an address this
// module stores nothing under, and refusing it would turn "nothing to show" into
// an error in the middle of somebody else's dossier.
func subjectHandles(s personaldata.Subject) (customerID, email string, err error) {
	customerID = strings.TrimSpace(s.CustomerID)
	email = strings.ToLower(strings.TrimSpace(s.Email))

	if customerID == "" && email == "" {
		return "", "", coreerrors.Invalid(CodeSubjectEmpty,
			"a data-subject request has to name somebody: give a customer id, an e-mail "+
				"address, or both")
	}

	return customerID, email, nil
}

// retained is the answer of a holder that kept what it holds, with the reason.
//
// What is kept is listed from the DECLARATION rather than from a second list, so
// a column added to one is never missing from the other.
func (m *Module) retained(why string) personaldata.Result {
	declared := m.PersonalData().Holdings
	kept := make([]string, 0, len(declared))
	for _, holding := range declared {
		kept = append(kept, holding.Table+"."+holding.Column)
	}

	return personaldata.Result{
		Holder: ErasureHolder, Outcome: personaldata.Retained, Kept: kept, Why: why,
	}
}

// personalRecords answers the store's optional data-subject capability.
func (m *Module) personalRecords() (PersonalRecords, bool) {
	if m.store == nil {
		return nil, false
	}

	records, ok := m.store.(PersonalRecords)

	return records, ok
}

// The store's own implementation is pinned where the contract is declared.
var _ PersonalRecords = pgCredentials{}
