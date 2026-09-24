package balancetender_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/balancetender"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// The machine's contract with its two adapters (ADR 0165).
//
// The verbs' ordinary path — hold at authorize, no spend at capture, release at
// cancel, refund at refund, decline on a short balance — is proven twice over
// through the adapters' own fakes (storecredit, loyaltypoints). What this file
// pins is what those tests reach only indirectly or not at all: the ENTRY the
// machine hands the store, its sign and its reference, because the schema of
// both ledgers pairs the sign with the kind and refuses the row otherwise; and
// the refusals and repeats, which the service's own transition table keeps out
// of every caller's reach but this file's.

// codes are a fixture identity's codes.
var codes = balancetender.Codes{
	InvalidInput: "fixture_invalid_input",
	NoCustomer:   "fixture_no_customer",
	Insufficient: "fixture_insufficient",
	InvalidState: "fixture_invalid_state",
}

// memStore is the smallest balancetender.Store: sessions in a map, the ledger
// as the list of entries the machine asked for, the balance as their sum plus a
// seed.
type memStore struct {
	seed     int64
	sessions map[string]models.TenderSession
	entries  []balancetender.Entry
	inTx     bool
}

func newMemStore(seed int64) *memStore {
	return &memStore{seed: seed, sessions: map[string]models.TenderSession{}}
}

var _ balancetender.Store = (*memStore)(nil)

func (m *memStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	m.inTx = true
	defer func() { m.inTx = false }()

	return fn(ctx)
}

func (m *memStore) InsertSessionIfAbsent(
	_ context.Context, session models.TenderSession,
) (models.TenderSession, bool, error) {
	for id := range m.sessions {
		if m.sessions[id].IdempotencyKey == session.IdempotencyKey {
			return models.TenderSession{}, false, nil
		}
	}
	m.sessions[session.ID] = session

	return session, true, nil
}

func (m *memStore) SessionByIdempotencyKey(_ context.Context, key string) (models.TenderSession, error) {
	for id := range m.sessions {
		if m.sessions[id].IdempotencyKey == key {
			return m.sessions[id], nil
		}
	}

	return models.TenderSession{}, coreerrors.NotFound("fixture_not_found", "no session: %s", key)
}

func (m *memStore) Session(_ context.Context, id string) (models.TenderSession, error) {
	session, ok := m.sessions[id]
	if !ok {
		return models.TenderSession{}, coreerrors.NotFound("fixture_not_found", "no session: %s", id)
	}

	return session, nil
}

func (m *memStore) LockSession(ctx context.Context, id string) (models.TenderSession, error) {
	if !m.inTx {
		return models.TenderSession{}, coreerrors.Internal("fixture_no_tx", "lock outside a transaction")
	}

	return m.Session(ctx, id)
}

func (m *memStore) UpdateSessionState(
	_ context.Context, id string, status models.SessionStatus,
	authorized, captured, refunded int64, declineReason string,
) (models.TenderSession, error) {
	session := m.sessions[id]
	session.Status, session.DeclineReason = status, declineReason
	session.AuthorizedAmount, session.CapturedAmount, session.RefundedAmount = authorized, captured, refunded
	m.sessions[id] = session

	return session, nil
}

func (m *memStore) Move(_ context.Context, entry balancetender.Entry) error {
	m.entries = append(m.entries, entry)

	return nil
}

func (m *memStore) Balance(_ context.Context, _, _ string) (int64, error) {
	balance := m.seed
	for i := range m.entries {
		balance += m.entries[i].Amount
	}

	return balance, nil
}

func (m *memStore) LockBalance(_ context.Context, _, _ string) error {
	if !m.inTx {
		return coreerrors.Internal("fixture_no_tx", "lock outside a transaction")
	}

	return nil
}

// newMachine builds a machine on a store seeded with the given balance.
func newMachine(seed int64) (*balancetender.Machine, *memStore) {
	store := newMemStore(seed)
	ids := 0

	return balancetender.New(store, balancetender.Identity{
		Unit:  "fixture units",
		Codes: codes,
		NewSessionID: func() string {
			ids++

			return "fix_" + string(rune('0'+ids))
		},
	}, nil), store
}

// open opens a session for a named customer.
func open(t *testing.T, machine *balancetender.Machine, key string, amount int64) coreprovider.Session {
	t.Helper()

	session, err := machine.CreateSession(context.Background(), coreprovider.CreateSessionInput{
		Amount: amount, CurrencyCode: "TRY", Reference: "paycol_1", IdempotencyKey: key, CustomerID: "cus_1",
	})
	require.NoError(t, err)

	return session
}

// TestEveryMovementCarriesItsSignAndTheSessionsName is the contract both
// adapters build on: a hold is negative, a release and a refund are positive,
// and every entry references the session the machine minted — never the
// collection the module handed in as Reference.
func TestEveryMovementCarriesItsSignAndTheSessionsName(t *testing.T) {
	t.Parallel()

	machine, store := newMachine(12_000)

	canceled := open(t, machine, "k1", 5_000)
	_, err := machine.Authorize(context.Background(), canceled.ID)
	require.NoError(t, err)
	require.NoError(t, machine.Cancel(context.Background(), canceled.ID))

	partial := open(t, machine, "k2", 5_000)
	_, err = machine.Authorize(context.Background(), partial.ID)
	require.NoError(t, err)
	require.NoError(t, machine.Capture(context.Background(), partial.ID, 2_000))
	require.NoError(t, machine.Refund(context.Background(), partial.ID, 0))

	want := []balancetender.Entry{
		{CustomerID: "cus_1", CurrencyCode: "TRY", Movement: balancetender.Hold, Amount: -5_000, SessionID: canceled.ID},
		{CustomerID: "cus_1", CurrencyCode: "TRY", Movement: balancetender.Release, Amount: 5_000, SessionID: canceled.ID},
		{CustomerID: "cus_1", CurrencyCode: "TRY", Movement: balancetender.Hold, Amount: -5_000, SessionID: partial.ID},
		{CustomerID: "cus_1", CurrencyCode: "TRY", Movement: balancetender.Release, Amount: 3_000, SessionID: partial.ID},
		{CustomerID: "cus_1", CurrencyCode: "TRY", Movement: balancetender.Refund, Amount: 2_000, SessionID: partial.ID},
	}
	assert.Equal(t, want, store.entries)

	for i := range store.entries {
		assert.NotEqual(t, "paycol_1", store.entries[i].SessionID,
			"an entry must never reference the collection: the earn target is summed by it")
	}
}

// TestTheCodesAreTheTendersOwn pins that every refusal carries the identity's
// codes, so the two tenders sharing one machine still refuse under their own
// names — an operator reading a log can tell which balance was short.
func TestTheCodesAreTheTendersOwn(t *testing.T) {
	t.Parallel()

	machine, _ := newMachine(1)

	_, err := machine.CreateSession(context.Background(), coreprovider.CreateSessionInput{
		Amount: 5, CurrencyCode: "TRY", Reference: "paycol_1", IdempotencyKey: "k3",
	})
	assert.Equal(t, codes.NoCustomer, coreerrors.CodeOf(err))
	assert.True(t, coreerrors.IsConflict(err))

	_, err = machine.CreateSession(context.Background(), coreprovider.CreateSessionInput{
		Amount: 0, CurrencyCode: "TRY", Reference: "paycol_1", IdempotencyKey: "k4", CustomerID: "cus_1",
	})
	assert.Equal(t, codes.InvalidInput, coreerrors.CodeOf(err))

	short := open(t, machine, "k5", 5)
	result, err := machine.Authorize(context.Background(), short.ID)
	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionFailed, result.Status)
	assert.Contains(t, result.DeclineReason, codes.Insufficient)

	err = machine.Capture(context.Background(), short.ID, 0)
	assert.Equal(t, codes.InvalidState, coreerrors.CodeOf(err))
}

// refused asserts that a verb was refused as a transition the session's status
// does not allow, and that it wrote nothing on the way.
func refused(t *testing.T, store *memStore, entriesBefore int, err error, what string) {
	t.Helper()

	require.Errorf(t, err, "%s has to be refused", what)
	assert.Equalf(t, codes.InvalidState, coreerrors.CodeOf(err), "%s: %v", what, err)
	assert.Truef(t, coreerrors.IsConflict(err),
		"%s is a request the session's STATE refuses, which is a conflict: %v", what, err)
	assert.Lenf(t, store.entries, entriesBefore, "%s wrote to the ledger while refusing", what)
}

// TestTheForbiddenTransitionsAreConflictsAndWriteNothing walks every refusal the
// machine makes on a session's status.
//
// None of them is reachable through the module on an ordinary day — the
// service's own transition table stops the call first — which is exactly why
// they need a witness of their own: a guard nothing exercises can be deleted
// and every lane stays green, and the day a second caller skips the service's
// table, a capture above the hold or a refund above the capture writes a row
// the ledger cannot take back.
func TestTheForbiddenTransitionsAreConflictsAndWriteNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	machine, store := newMachine(20_000)

	session := open(t, machine, "k-forbidden", 5_000)
	_, err := machine.Authorize(ctx, session.ID)
	require.NoError(t, err)
	held := len(store.entries)

	refused(t, store, held, machine.Capture(ctx, session.ID, 5_001),
		"a capture above the held amount")
	refused(t, store, held, machine.Refund(ctx, session.ID, 0),
		"a refund of a session that captured nothing")

	require.NoError(t, machine.Capture(ctx, session.ID, 5_000))
	captured := len(store.entries)

	refused(t, store, captured, machine.Cancel(ctx, session.ID),
		"a cancel of a captured session")
	refused(t, store, captured, machine.Refund(ctx, session.ID, 5_001),
		"a refund above the captured amount")

	require.NoError(t, machine.Refund(ctx, session.ID, 4_000))
	refused(t, store, captured+1, machine.Refund(ctx, session.ID, 1_001),
		"a refund above what is left of the capture")

	canceled := open(t, machine, "k-canceled", 1_000)
	_, err = machine.Authorize(ctx, canceled.ID)
	require.NoError(t, err)
	require.NoError(t, machine.Cancel(ctx, canceled.ID))
	refused(t, store, len(store.entries), machine.Capture(ctx, canceled.ID, 0),
		"a capture of a canceled session")
}

// TestARepeatedVerbAnswersWithTheCurrentStateAndWritesNothing is the
// idempotency the package godoc promises, measured on the LEDGER.
//
// The saga retries a step it cannot tell succeeded, and every verb here may
// therefore arrive twice. A repeat that returned an error would fail a
// checkout that had in fact paid; a repeat that wrote would take the balance
// twice, or give it back twice. The schema would refuse only one of those — a
// zero-amount row — and as a server error rather than as the answer the
// contract asks for.
func TestARepeatedVerbAnswersWithTheCurrentStateAndWritesNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	machine, store := newMachine(20_000)

	session := open(t, machine, "k-repeat", 5_000)
	first, err := machine.Authorize(ctx, session.ID)
	require.NoError(t, err)
	again, err := machine.Authorize(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, first, again, "a second authorize answers with the hold it already made")
	assert.Len(t, store.entries, 1, "a second authorize holds nothing more")

	require.NoError(t, machine.Capture(ctx, session.ID, 0))
	require.NoError(t, machine.Capture(ctx, session.ID, 0),
		"a second capture answers with the current state rather than an error")
	assert.Len(t, store.entries, 1, "a capture of the whole hold writes nothing, twice")
	assert.Equal(t, int64(5_000), store.sessions[session.ID].CapturedAmount)

	require.NoError(t, machine.Refund(ctx, session.ID, 0))
	require.NoError(t, machine.Refund(ctx, session.ID, 0),
		"a refund of a session with nothing left to give back is not an error")
	assert.Len(t, store.entries, 2, "the second refund wrote a row, and a zero row at that")
	assert.Equal(t, int64(5_000), store.sessions[session.ID].RefundedAmount)

	canceled := open(t, machine, "k-cancel-twice", 1_000)
	_, err = machine.Authorize(ctx, canceled.ID)
	require.NoError(t, err)
	require.NoError(t, machine.Cancel(ctx, canceled.ID))
	require.NoError(t, machine.Cancel(ctx, canceled.ID), "the saga's compensation is idempotent")
	assert.Len(t, store.entries, 4, "one hold and one release, however many cancels")
	assert.Equal(t, int64(20_000), mustBalance(t, store),
		"everything captured was refunded and everything held was released")
}

// mustBalance reads the fixture's balance.
func mustBalance(t *testing.T, store *memStore) int64 {
	t.Helper()

	balance, err := store.Balance(context.Background(), "cus_1", "TRY")
	require.NoError(t, err)

	return balance
}
