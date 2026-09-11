package identitypasskey

import (
	"context"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
)

// The module answers the three data-subject capabilities.
//
// They are OPTIONAL and found by type assertion on the registered module (ADR
// 0029), so these pins are the only thing that fails when a signature drifts —
// a module that silently stopped implementing [personaldata.Eraser] would be
// skipped by the sweep with a green build and a complete-looking report, which
// is the exact failure ADR 0035 measured on a different capability.
//
// This module is reached by the sweep with no wiring of its own: an installation
// adds it to the registry like any other module, and the coordinator walks what
// the registry holds.
var (
	_ personaldata.Eraser    = (*Module)(nil)
	_ personaldata.Declarer  = (*Module)(nil)
	_ personaldata.Discloser = (*Module)(nil)
)

// ErasureHolder is the name this module answers a data-subject request under.
const ErasureHolder = ModuleName

// CodeSubjectEmpty is a data-subject request that names nobody.
const CodeSubjectEmpty = "identity_passkey_subject_empty"

// PersonalRecords is the OPTIONAL capability a store offers to answer a
// data-subject request.
//
// It is separate from [Credentials] for that interface's own reason: an
// installation may bind LDAP or a table of its own, and such a store neither can
// nor should be asked to erase rows out of gobit's table. A store that does not
// implement this leaves the module answering [personaldata.Retained] with the
// reason, which is true and is what a controller needs to hear.
//
// # Neither method is scoped by the relying party, and that is the decision
//
// Every other read in this module answers "which keys can sign this person in",
// and an abandoned relying party's rows are not those (ADR 0131). A data-subject
// request asks something else: what is HELD about her. An abandoned row is held
// about her, so scoping these would leave a person who asked to be forgotten
// with rows still on disk, and a dossier that is true about everything it
// mentions and silent about the rest.
type PersonalRecords interface {
	// ErasePasskeysOf deletes every credential of a customer, whatever relying
	// party it belongs to, and answers how many rows went.
	ErasePasskeysOf(ctx context.Context, customerID string) (int, error)
	// PasskeyRecordsOf reads every credential of a customer for disclosure.
	PasskeyRecordsOf(ctx context.Context, customerID string) ([]StoredKey, error)
}

// StoredKey is one row as a dossier reports it.
//
// It carries more than [Key] does, and the difference is the point. [Key] feeds a
// person choosing which passkey to remove, so it withholds their hardware — the
// authenticator model, the public key, the counter. A disclosure answers "what do
// you hold about me", and withholding a column from THAT answer would make the
// answer false.
type StoredKey struct {
	// CredentialID is the row's identifier, base64url.
	CredentialID string
	// RelyingPartyID is the relying party the key was registered under, empty for
	// a row written before the column existed.
	RelyingPartyID string
	// CreatedAt is when it was registered.
	CreatedAt time.Time
	// LastUsedAt is the last sign-in this module managed to record.
	LastUsedAt *time.Time
	// Credential is the whole stored credential, as JSON.
	Credential string
}

// The tables and columns this module declares.
//
// Two of these names are annotated for gosec, which reads "credential" in an
// identifier as a hardcoded secret. They are a TABLE name and a COLUMN name that a
// declaration and a dossier print. The stored credential is not a secret either —
// it is a public key and the identifier the authenticator minted, and the private
// half never leaves the person's device.
const (
	tablePasskeyCredentials = "passkey_credentials" //nolint:gosec // a table name

	columnCredentialID = "credential_id" //nolint:gosec // a column name, printed in a declaration
	columnCustomerID   = "customer_id"
	columnCredential   = "credential"
	columnCreatedAt    = "created_at"
	columnLastUsedAt   = "last_used_at"
)

// PersonalData says what this module keeps about a person.
//
// `rp_id` is NOT declared and the omission is deliberate: it is the
// installation's own configuration copied onto the row, identical for every
// person registered under it, and declaring it would tell a controller to look
// for somebody in a column that describes the shop.
func (m *Module) PersonalData() personaldata.Declaration {
	return personaldata.Declaration{
		Holder: ErasureHolder,
		Holdings: []personaldata.Holding{
			{
				Table: tablePasskeyCredentials, Column: columnCustomerID,
				Kind: personaldata.Named,
				Why: "the shop's own identifier for the person this passkey signs in; it is " +
					"the only handle this module keys on, and it is what makes every other " +
					"column in the row a fact about somebody",
			},
			{
				Table: tablePasskeyCredentials, Column: columnCredentialID,
				Kind: personaldata.Named,
				Why: "the identifier the person's authenticator minted for this shop; it is " +
					"unique to them and to this site, so it identifies the device as well as " +
					"the account and is pseudonymised personal data rather than none",
			},
			{
				Table: tablePasskeyCredentials, Column: columnCredential,
				Kind: personaldata.Named,
				Why: "the whole credential as the WebAuthn library stores it: the public key " +
					"derived from a secret held on the person's own device, the AAGUID naming " +
					"the model of authenticator they use, the signature counter and the " +
					"transports it can be reached over",
			},
			{
				Table: tablePasskeyCredentials, Column: columnCreatedAt,
				Kind: personaldata.Named,
				Why: "when this person registered this device, which is a record of something " +
					"they did at a moment rather than a property of the account",
			},
			{
				Table: tablePasskeyCredentials, Column: columnLastUsedAt,
				Kind: personaldata.Named,
				Why: "the last sign-in with this device that this module managed to record; it " +
					"says roughly when the person was last here and from which of their devices",
			},
		},
	}
}

// Erase deletes every passkey of a person.
//
// # It does NOT apply the last-way-in rule, and that is not an oversight
//
// [Module.removeKey] refuses a removal that would leave an account with no way
// in (ADR 0130), and routing an erasure through it would refuse an erasure for
// exactly the person it is meant to serve. The rule defends somebody who is
// KEEPING their account; an erasure is that person asking for the account to stop
// existing. A guard written for one path and applied to another is a defect this
// repository has recorded more than once, so the erasure writes its own
// statement and says so here.
func (m *Module) Erase(ctx context.Context, s personaldata.Subject) (personaldata.Result, error) {
	if s.CustomerID == "" && s.Email == "" {
		return personaldata.Result{}, coreerrors.Invalid(CodeSubjectEmpty,
			"an erasure has to name somebody: give a customer id, an e-mail address, or both")
	}

	records, ok := m.personalRecords()
	if !ok {
		return m.retained("the bound credential store does not offer erasure, so this " +
			"module's passkeys are still there"), nil
	}

	// An e-mail alone cannot be resolved here. This module stores no address —
	// the account is whoever the bound identity proves — so an address is not a
	// subject it stores nothing under, it is a subject it cannot LOOK for.
	// Answering "deleted, 0 rows" would report a search that never happened.
	if s.CustomerID == "" {
		return m.retained("this module keys on the shop's customer id and stores no " +
			"e-mail address, so a subject named only by address cannot be resolved here"), nil
	}

	rows, err := records.ErasePasskeysOf(ctx, s.CustomerID)
	if err != nil {
		return personaldata.Result{}, err
	}

	m.log.InfoContext(ctx, "identity-passkey erased a person's passkeys",
		"rows", rows)

	return personaldata.Result{
		Holder: ErasureHolder, Outcome: personaldata.Deleted, Rows: rows,
	}, nil
}

// PersonalDataOf assembles what this module holds about a person.
func (m *Module) PersonalDataOf(
	ctx context.Context, s personaldata.Subject,
) (personaldata.Disclosure, error) {
	if s.CustomerID == "" && s.Email == "" {
		return personaldata.Disclosure{}, coreerrors.Invalid(CodeSubjectEmpty,
			"a disclosure has to name somebody: give a customer id, an e-mail address, or both")
	}

	records, ok := m.personalRecords()
	if !ok {
		return m.cannotLook("the bound credential store does not offer disclosure"), nil
	}
	if s.CustomerID == "" {
		return m.cannotLook("this module keys on the shop's customer id and stores no " +
			"e-mail address, so a subject named only by address cannot be resolved here"), nil
	}

	keys, err := records.PasskeyRecordsOf(ctx, s.CustomerID)
	if err != nil {
		return personaldata.Disclosure{}, err
	}
	if len(keys) == 0 {
		return personaldata.Disclosure{
			Holder: ErasureHolder, State: personaldata.Nothing,
		}, nil
	}

	out := make([]personaldata.Record, 0, len(keys))
	for _, key := range keys {
		out = append(out, personaldata.Record{
			Table: tablePasskeyCredentials,
			ID:    key.CredentialID,
			Fields: []personaldata.Field{
				{Column: columnCustomerID, Kind: personaldata.Named, Value: s.CustomerID},
				{Column: columnCredentialID, Kind: personaldata.Named, Value: key.CredentialID},
				{Column: columnCredential, Kind: personaldata.Named, Value: key.Credential},
				{Column: columnCreatedAt, Kind: personaldata.Named, Value: key.CreatedAt},
				{Column: columnLastUsedAt, Kind: personaldata.Named, Value: key.LastUsedAt},
			},
		})
	}

	// The most concentrated personal data this module can produce, so the fact
	// that somebody assembled it is recorded — an audit asking afterwards who
	// read what finds nothing if the read left no trace. The customer id is not
	// logged: it is the subject of the request, and a log line naming both the
	// person and the sweep is the dossier in miniature.
	m.log.InfoContext(ctx, "identity-passkey disclosed a person's passkeys",
		"records", len(out))

	return personaldata.Disclosure{
		Holder: ErasureHolder, State: personaldata.Disclosed, Records: out,
	}, nil
}

// retained is the answer of a holder that kept what it holds, with the reason.
//
// It lists what is kept from the DECLARATION rather than from a second list, so
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

// cannotLook is the disclosure of a holder that could not search at all.
//
// [personaldata.Unresolvable] rather than [personaldata.Nothing]: a holder that
// searched and found nothing has told the controller something true, and one that
// could not search has not. Reporting the second as the first lets it hide inside
// the first, which is the distinction the three states exist for.
func (m *Module) cannotLook(why string) personaldata.Disclosure {
	return personaldata.Disclosure{
		Holder: ErasureHolder, State: personaldata.Unresolvable, Why: why,
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

// The store's own implementation is pinned here rather than beside it, so that a
// drift in [PersonalRecords] fails in the file that declares the contract.
var _ PersonalRecords = pgCredentials{}
