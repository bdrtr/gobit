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
	// SessionsValidFrom is the moment the person's sessions count from, or the
	// zero time when nothing ever ended them (ADR 0374).
	SessionsValidFrom time.Time
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
	tableCustomerCredentials    = "customer_credentials"
	tableCustomerRegistrations  = "customer_registrations"
	tableCustomerPasswordResets = "customer_password_resets"
	tableCustomerAddressChanges = "customer_address_changes"

	columnCustomerID   = "customer_id"
	columnEmail        = "email"
	columnPasswordHash = "password_hash"
	columnCreatedAt    = "created_at"
	columnUpdatedAt    = "updated_at"
	columnExpiresAt    = "expires_at"
	columnTokenHash    = "token_hash"

	columnSessionsValidFrom = "sessions_valid_from"
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
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerCredentials, Column: columnEmail,
				Kind: personaldata.Named,
				Why: "the address the person signs in with, folded to lower case; it is their " +
					"e-mail address and it is also the name they log in under",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerCredentials, Column: columnPasswordHash,
				Kind: personaldata.Named,
				Why: "an argon2id hash of the password this person chose; it cannot be read " +
					"back, but it is derived from a secret of theirs and people reuse " +
					"passwords, so it is pseudonymised personal data rather than none",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerCredentials, Column: columnCreatedAt,
				Kind: personaldata.Named,
				Why: "when this person first set a password here, which is a record of " +
					"something they did at a moment rather than a property of the account",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerCredentials, Column: columnUpdatedAt,
				Kind:      personaldata.Named,
				Why:       "when they last changed it, which says they were here and roughly when",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerCredentials, Column: columnSessionsValidFrom,
				Kind: personaldata.Named,
				Why: "when the person's password was last replaced or they ended their other " +
					"sessions, so that every session issued before it is refused (ADR 0374)",
				OnErasure: personaldata.Emptied,
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
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerRegistrations, Column: columnPasswordHash,
				Kind: personaldata.Named,
				Why: "an argon2id hash of the password chosen during a registration that was " +
					"never completed; hashed the moment it arrived, so the plaintext never " +
					"outlived that request",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerRegistrations, Column: columnTokenHash,
				Kind: personaldata.Named,
				Why: "the SHA-256 of the link that was sent to that address; it identifies " +
					"the registration rather than the person, and it is declared because a " +
					"controller asked to erase somebody has to know this row is here",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerRegistrations, Column: columnCreatedAt,
				Kind: personaldata.Named,
				Why: "when that registration was started, which is a record of something " +
					"the person did at a moment",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerRegistrations, Column: columnExpiresAt,
				Kind: personaldata.Named,
				Why: "when the link stops working; declared beside the moment above so that " +
					"a controller reading this list sees the whole row rather than part of it",
				OnErasure: personaldata.Emptied,
			},
			// A pending password reset (ADR 0373) goes with the credential it
			// would replace: its foreign key cascades, so the erasure above takes it
			// without a statement of its own that could be forgotten.
			{
				Table: tableCustomerPasswordResets, Column: columnCustomerID,
				Kind: personaldata.Named,
				Why: "the customer a password reset link was sent for, while that link is " +
					"unused; it goes with their credential",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerPasswordResets, Column: columnTokenHash,
				Kind: personaldata.Named,
				Why: "the SHA-256 of the reset link that was sent; it identifies the reset " +
					"rather than the person",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerPasswordResets, Column: columnCreatedAt,
				Kind: personaldata.Named,
				Why: "when the person asked for a new password, which is a record of " +
					"something they did at a moment",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerPasswordResets, Column: columnExpiresAt,
				Kind:      personaldata.Named,
				Why:       "when the reset link stops working; declared beside the moment above",
				OnErasure: personaldata.Emptied,
			},
			// A pending address change (ADR 0377) goes with the credential it
			// would move, by its cascade, and is also erased by the address it
			// would move to: that address may be somebody else's.
			{
				Table: tableCustomerAddressChanges, Column: columnCustomerID,
				Kind: personaldata.Named,
				Why: "the customer whose account a link would move to a new address, while " +
					"that link is unused",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerAddressChanges, Column: columnEmail,
				Kind: personaldata.Named,
				Why: "the address the account asked to move to, which has not been proven " +
					"yet and may be the address of somebody else",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerAddressChanges, Column: columnTokenHash,
				Kind: personaldata.Named,
				Why: "the SHA-256 of the link that was sent to the new address; it identifies " +
					"the change rather than the person",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerAddressChanges, Column: columnCreatedAt,
				Kind: personaldata.Named,
				Why: "when the person asked to move their account, which is a record of " +
					"something they did at a moment",
				OnErasure: personaldata.Emptied,
			},
			{
				Table: tableCustomerAddressChanges, Column: columnExpiresAt,
				Kind:      personaldata.Named,
				Why:       "when the link stops working; declared beside the moment above",
				OnErasure: personaldata.Emptied,
			},
		},
	}
}

// Erase deletes a person's password credentials.
//
// # It erases the row, not the sessions
//
// A signed cookie is not stored anywhere — that is the whole point of signing it
// (ADR 0127) — so there is no row to delete and no list to walk. The moment a
// person's sessions count from (ADR 0374) is a column of the row this deletes,
// so it goes with it rather than ending anything. The cookie a deleted person
// still holds stops working the moment anything looks the account up, and it
// expires on its own. Reporting the row as deleted and the sessions as
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

	// A pending password reset needs no statement here: its row points at the
	// credential just deleted and the foreign key took it (ADR 0373). So does
	// the person's own pending address change; one that would move SOMEBODY
	// ELSE's account to this person's address holds the address and goes by it
	// (ADR 0377).
	moving := 0
	if changes, ok := records.(AddressChangeRecords); ok {
		if moving, err = changes.EraseAddressChangesTo(ctx, email); err != nil {
			return personaldata.Result{}, err
		}
	}

	m.log.InfoContext(ctx, "identity-session erased a person's credentials",
		"rows", rows+pending+moving, "pending_registrations", pending,
		"address_changes_to_the_address", moving,
		"by_customer_id", customerID != "", "by_email", email != "")

	return personaldata.Result{
		Holder: ErasureHolder, Outcome: personaldata.Deleted, Rows: rows + pending + moving,
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
	var reset *StoredPasswordReset
	if resets, ok := records.(PasswordResetRecords); ok {
		if reset, err = resets.PendingPasswordResetOf(ctx, customerID, email); err != nil {
			return personaldata.Disclosure{}, err
		}
	}
	var changes []StoredAddressChange
	if moving, ok := records.(AddressChangeRecords); ok {
		if changes, err = moving.PendingAddressChangesOf(ctx, customerID, email); err != nil {
			return personaldata.Disclosure{}, err
		}
	}
	if len(found) == 0 && pending == nil && reset == nil && len(changes) == 0 {
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
				{Column: columnSessionsValidFrom, Kind: personaldata.Named, Value: momentOrNothing(row.SessionsValidFrom)},
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

	// A pending password reset is its own record too, its token reported as the
	// sign-up link's is: by what it is, never by its value.
	if reset != nil {
		out = append(out, personaldata.Record{
			Table: tableCustomerPasswordResets,
			ID:    reset.CustomerID,
			Fields: []personaldata.Field{
				{Column: columnCustomerID, Kind: personaldata.Named, Value: reset.CustomerID},
				{Column: columnCreatedAt, Kind: personaldata.Named, Value: reset.CreatedAt},
				{Column: columnExpiresAt, Kind: personaldata.Named, Value: reset.ExpiresAt},
				{
					Column: columnTokenHash, Kind: personaldata.Named,
					Value: "a password reset link was sent; the stored value is its SHA-256 and " +
						"is deliberately not reproduced here, because the link itself would " +
						"replace the password",
				},
			},
		})
	}

	// A pending address change is its own record, its token reported by what
	// it is (ADR 0377).
	for _, change := range changes {
		out = append(out, personaldata.Record{
			Table: tableCustomerAddressChanges,
			ID:    change.CustomerID,
			Fields: []personaldata.Field{
				{Column: columnCustomerID, Kind: personaldata.Named, Value: change.CustomerID},
				{Column: columnEmail, Kind: personaldata.Named, Value: change.Email},
				{Column: columnCreatedAt, Kind: personaldata.Named, Value: change.CreatedAt},
				{Column: columnExpiresAt, Kind: personaldata.Named, Value: change.ExpiresAt},
				{
					Column: columnTokenHash, Kind: personaldata.Named,
					Value: "a link was sent to the new address; the stored value is its SHA-256 " +
						"and is deliberately not reproduced here, because the link itself would " +
						"move the account",
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

// momentOrNothing reports a moment that may not have happened: nil, rather than
// the year one, for a column that holds nothing.
func momentOrNothing(moment time.Time) any {
	if moment.IsZero() {
		return nil
	}

	return moment
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
	return personaldata.Result{
		Holder: ErasureHolder, Outcome: personaldata.Retained,
		Kept: m.PersonalData().Paths(), Why: why,
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

// PasswordResetRecords is the OPTIONAL capability a store offers to show a
// pending password reset in a dossier (ADR 0373). It is not a method of
// [PersonalRecords], which is published, for [PasswordReset]'s reason.
type PasswordResetRecords interface {
	// PendingPasswordResetOf reads the unused reset of a customer or of the
	// address their credential signs in with, or nil when there is none.
	PendingPasswordResetOf(ctx context.Context, customerID, email string) (*StoredPasswordReset, error)
}

// StoredPasswordReset is a pending reset as a dossier reports it: never its
// token or the token's hash.
type StoredPasswordReset struct {
	// CustomerID is the customer the link was sent for.
	CustomerID string
	// CreatedAt is when the reset was asked for.
	CreatedAt time.Time
	// ExpiresAt is when the link stops working.
	ExpiresAt time.Time
}

// The store shows pending resets in a dossier.
var _ PasswordResetRecords = pgCredentials{}

// AddressChangeRecords is the OPTIONAL capability a store offers to answer a
// data-subject request about pending address changes (ADR 0377), for
// [PasswordResetRecords]' reason.
type AddressChangeRecords interface {
	// EraseAddressChangesTo deletes every pending change that would move an
	// account to the address, and answers how many went. A blank address
	// erases nothing.
	EraseAddressChangesTo(ctx context.Context, email string) (int, error)
	// PendingAddressChangesOf reads the pending change of a customer, of the
	// address their credential signs in with, and every one that would move an
	// account to the address.
	PendingAddressChangesOf(ctx context.Context, customerID, email string) ([]StoredAddressChange, error)
}

// StoredAddressChange is a pending address change as a dossier reports it:
// never its token or the token's hash.
type StoredAddressChange struct {
	// CustomerID is the customer the link would move.
	CustomerID string
	// Email is the address it would move them to.
	Email string
	// CreatedAt is when the change was asked for.
	CreatedAt time.Time
	// ExpiresAt is when the link stops working.
	ExpiresAt time.Time
}
