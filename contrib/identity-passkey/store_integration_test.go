//go:build integration

package identitypasskey_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"
	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	identitypasskey "github.com/bdrtr/gobit/contrib/identity-passkey"
	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
)

// The tests in this file run the SQL and the CHECKs.
//
// Everything beside them runs on a store in a map, which proves the ceremonies
// and nothing about storage. What shipped here is four statements, two CHECKs, a
// JSONB column and an index; a mistyped column compiles, and a credential that
// round-trips through the library but not through the database is invisible
// until somebody's key stops working.
//
// The JSONB column is the case worth naming. The whole credential is stored as
// the library serialises it, which is the decision that keeps a library upgrade
// from being a migration — and it is only true if a credential really does come
// back equal to the one that went in, through a real column of a real type.

var testPool *pgxpool.Pool

var testDSN string

// TestMain raises one Postgres and applies this module's migrations.
func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres is separate because os.Exit skips defers.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("identity_passkey_test"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "the postgres container could not be stopped: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "the postgres container could not be started: %v\n", err)

		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection string could not be read: %v\n", err)

		return 1
	}

	pool, err := db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the pool could not be opened: %v\n", err)

		return 1
	}
	defer pool.Close()
	testPool = pool.Pool()

	if err := db.Migrate(ctx, testDSN,
		identitypasskey.New(identitypasskey.Options{}).Migrations(),
		identitypasskey.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)

		return 1
	}

	return m.Run()
}

// newRealHarness is [newHarness] with the module's own Postgres store.
func newRealHarness(t *testing.T) *harness {
	t.Helper()

	session := identitysession.New(identitysession.Options{
		Secret:      []byte(testSecret),
		Insecure:    true,
		Credentials: noCredentials{},
	})
	c := container.New(nil)
	require.NoError(t, session.Register(t.Context(), c))

	pool, err := db.New(t.Context(), db.DefaultConfig(testDSN), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, c.Provide("core.db", pool))

	m := identitypasskey.New(identitypasskey.Options{
		Session: session, RPID: testRPID, RPOrigins: []string{testOrigin},
		DisplayName: "Example Shop",
	})
	require.NoError(t, m.Register(t.Context(), c))

	r := chi.NewRouter()
	m.Routes(r)

	return &harness{
		module: m, router: r, sessions: session.Sessions(), store: nil,
		rp: virtualwebauthn.RelyingParty{
			ID: testRPID, Name: "Example Shop", Origin: testOrigin,
		},
		auth: virtualwebauthn.NewAuthenticatorWithOptions(virtualwebauthn.AuthenticatorOptions{
			UserHandle: []byte(testCustomer),
		}),
	}
}

// realStore is the module's own store on the container.
func realStore(t *testing.T) identitypasskey.Credentials {
	t.Helper()

	return newRealHarness(t).module.Store()
}

// aCredential is a credential shaped like one the library would hand over.
func aCredential(id string) webauthn.Credential {
	return webauthn.Credential{
		ID:              []byte(id),
		PublicKey:       []byte("a public key"),
		AttestationType: "none",
	}
}

// TestTheSchemaIsWhatTheModuleWrites is the witness under the rest.
func TestTheSchemaIsWhatTheModuleWrites(t *testing.T) {
	var columns int
	require.NoError(t, testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM information_schema.columns WHERE table_name = 'passkey_credentials'`,
	).Scan(&columns))
	assert.Equal(t, 5, columns,
		"credential_id, customer_id, credential, created_at, last_used_at")

	var kind string
	require.NoError(t, testPool.QueryRow(t.Context(),
		`SELECT data_type FROM information_schema.columns
		 WHERE table_name = 'passkey_credentials' AND column_name = 'credential'`,
	).Scan(&kind))
	assert.Equal(t, "jsonb", kind,
		"the credential is JSONB, which is what keeps a library upgrade from being "+
			"a migration")
}

// TestAKeyStoredInPostgresStillSignsIn is the storage decision, executed.
//
// # Why the assertion is a SIGN-IN and not a field comparison
//
// The first version of this test compared fields, and it failed on
// `attestationType` — which the library writes into its JSON and, in one shape
// of credential, does not read back. Chasing that was chasing the wrong thing
// twice over: the field is registration metadata that no assertion consults, and
// a list of fields to compare is a copy of the library's own schema that goes
// stale the first time it grows one.
//
// What the storage decision actually promises is narrower and testable: a key
// registered, WRITTEN TO POSTGRES, and read back out of it still signs its
// owner in. So the test performs both ceremonies with the real store between
// them, and nothing here knows which fields the library needed.
func TestAKeyStoredInPostgresStillSignsIn(t *testing.T) {
	h := newRealHarness(t)

	begun := h.post(t, "/store/v1/auth/passkey/register/begin", "", h.signedInAs(t, testCustomer))
	require.Equal(t, http.StatusOK, begun.Code, "body: %s", begun.Body.String())

	options, err := virtualwebauthn.ParseAttestationOptions(begun.Body.String())
	require.NoError(t, err)
	credential := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	attestation := virtualwebauthn.CreateAttestationResponse(h.rp, h.auth, credential, *options)

	stored := h.post(t, "/store/v1/auth/passkey/register/finish", attestation,
		h.signedInAs(t, testCustomer), ceremonyCookieOf(t, begun))
	require.Equal(t, http.StatusNoContent, stored.Code, "body: %s", stored.Body.String())

	// It really is in the table, not in a map this test could have kept.
	var rows int
	require.NoError(t, testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM passkey_credentials WHERE customer_id = $1`, testCustomer,
	).Scan(&rows))
	require.Positive(t, rows)

	h.auth.AddCredential(credential)

	// And a sign-in that reads it back out of Postgres completes.
	signingIn := h.post(t, "/store/v1/auth/passkey/sign-in/begin", "")
	require.Equal(t, http.StatusOK, signingIn.Code, "body: %s", signingIn.Body.String())

	assertionOptions, err := virtualwebauthn.ParseAssertionOptions(signingIn.Body.String())
	require.NoError(t, err)
	assertion := virtualwebauthn.CreateAssertionResponse(h.rp, h.auth, credential, *assertionOptions)

	in := h.post(t, "/store/v1/auth/passkey/sign-in/finish", assertion,
		ceremonyCookieOf(t, signingIn))
	require.Equal(t, http.StatusNoContent, in.Code,
		"a key that went through a JSONB column has to still sign in; body: %s",
		in.Body.String())
}

// TestOneKeyStaysOneRow is the conflict target: a key registered twice is the
// same key.
func TestOneKeyStaysOneRow(t *testing.T) {
	store := realStore(t)
	credential := aCredential("cred_" + t.Name())

	require.NoError(t, store.Put(t.Context(), testCustomer, credential))
	require.NoError(t, store.Put(t.Context(), testCustomer, credential))

	var rows int
	require.NoError(t, testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM passkey_credentials WHERE customer_id = $1`, testCustomer,
	).Scan(&rows))
	assert.GreaterOrEqual(t, rows, 1)

	credentials, err := store.ForCustomer(t.Context(), testCustomer)
	require.NoError(t, err)

	seen := 0
	for _, c := range credentials {
		if string(c.ID) == string(credential.ID) {
			seen++
		}
	}
	assert.Equal(t, 1, seen, "one key is ONE row, not two signing the same challenge")
}

// TestAPersonKeepsEveryKey is most of why passkeys are worth having.
func TestAPersonKeepsEveryKey(t *testing.T) {
	store := realStore(t)
	customerID := "cust_" + t.Name()

	require.NoError(t, store.Put(t.Context(), customerID, aCredential("phone_"+t.Name())))
	require.NoError(t, store.Put(t.Context(), customerID, aCredential("laptop_"+t.Name())))

	credentials, err := store.ForCustomer(t.Context(), customerID)
	require.NoError(t, err)
	assert.Len(t, credentials, 2,
		"losing one device must not cost the other; a row per CUSTOMER would")
}

// TestAnUnknownCredentialIsNotFoundAndSaysNothingElse keeps the sign-in from
// telling a caller which credential ids exist.
func TestAnUnknownCredentialIsNotFoundAndSaysNothingElse(t *testing.T) {
	store := realStore(t)

	_, _, err := store.ByCredentialID(t.Context(), []byte("nobody's key"))

	assert.ErrorIs(t, err, identitypasskey.ErrNoCredential)
}

// TestUsingAKeyStampsIt is the column a person reads when they decide which key
// to remove.
func TestUsingAKeyStampsIt(t *testing.T) {
	store := realStore(t)
	credential := aCredential("cred_" + t.Name())
	require.NoError(t, store.Put(t.Context(), testCustomer, credential))

	var before *string
	require.NoError(t, testPool.QueryRow(t.Context(),
		`SELECT last_used_at::text FROM passkey_credentials WHERE credential_id = $1`,
		encodedID(credential.ID)).Scan(&before))
	require.Nil(t, before, "a key that has never signed in carries no stamp")

	require.NoError(t, store.Used(t.Context(), credential.ID))

	var after *string
	require.NoError(t, testPool.QueryRow(t.Context(),
		`SELECT last_used_at::text FROM passkey_credentials WHERE credential_id = $1`,
		encodedID(credential.ID)).Scan(&after))
	assert.NotNil(t, after, "using it stamps it")
}

// TestTheChecksRefuseWhatTheStoreWouldNeverWrite runs them with raw SQL.
//
// Going around the store is the point: a floor tested only through the thing
// standing on it is not known to be there.
func TestTheChecksRefuseWhatTheStoreWouldNeverWrite(t *testing.T) {
	for name, row := range map[string][3]string{
		"a blank customer":              {"cred_check_1", "   ", `{"id":"x"}`},
		"a credential that is an array": {"cred_check_2", "cust_check", `["not an object"]`},
		"a credential that is a string": {"cred_check_3", "cust_check", `"not an object"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := testPool.Exec(t.Context(),
				`INSERT INTO passkey_credentials (credential_id, customer_id, credential)
				 VALUES ($1, $2, $3::jsonb)`, row[0], row[1], row[2])

			require.Error(t, err, "the database has to refuse %s", name)
			assert.Contains(t, err.Error(), "passkey_credentials_",
				"the refusal names the constraint, which is what an operator reads")
		})
	}
}

// encodedID spells a credential id the way the column holds it.
//
// It spells the encoding out rather than calling the store's helper: a test
// reading the column through the same function that wrote it would agree with
// that function whatever either of them did, and what these tests are for is the
// COLUMN.
func encodedID(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

// TestARegistrationCannotTakeAnotherCustomersKey is gap D64, in the store.
//
// A credential id comes from the client: the authenticator mints it and a
// hostile one presents whatever it likes. Until the conflict update was scoped
// to the same owner, registering an id that was already somebody else's MOVED
// their row onto the registering account and answered success.
func TestARegistrationCannotTakeAnotherCustomersKey(t *testing.T) {
	store := realStore(t)
	victim := "cust_" + t.Name() + "_victim"
	thief := "cust_" + t.Name() + "_thief"
	credential := aCredential("cred_" + t.Name())

	require.NoError(t, store.Put(t.Context(), victim, credential))

	err := store.Put(t.Context(), thief, credential)

	require.ErrorIs(t, err, identitypasskey.ErrCredentialBelongsToAnother,
		"a foreign credential id must be refused and NAMED, not written and not a 500")

	owner, _, readErr := store.ByCredentialID(t.Context(), credential.ID)
	require.NoError(t, readErr)
	assert.Equal(t, victim, owner, "the row stays with the customer who registered it")

	left, err := store.ForCustomer(t.Context(), victim)
	require.NoError(t, err)
	assert.Len(t, left, 1, "and the victim keeps their way in")
}

// TestRegisteringYourOwnKeyAgainStillReplacesIt keeps the scope from closing the
// case it was not about.
//
// The conflict target is the credential because a key registered twice is the
// same key; scoping it to the owner must not turn a person re-registering their
// own device into a refusal.
func TestRegisteringYourOwnKeyAgainStillReplacesIt(t *testing.T) {
	store := realStore(t)
	customerID := "cust_" + t.Name()
	credential := aCredential("cred_" + t.Name())

	require.NoError(t, store.Put(t.Context(), customerID, credential))
	credential.PublicKey = []byte("a rotated public key")
	require.NoError(t, store.Put(t.Context(), customerID, credential),
		"re-registering your own key is not a takeover")

	_, got, err := store.ByCredentialID(t.Context(), credential.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("a rotated public key"), got.PublicKey)

	credentials, err := store.ForCustomer(t.Context(), customerID)
	require.NoError(t, err)
	assert.Len(t, credentials, 1, "one key is still ONE row")
}

// TestTheRealStoreRefusesTheLastKey runs the guard against the real CHECKs.
//
// The memory store imitates this rule, which is exactly why it cannot prove it:
// the imitation and the SQL can disagree and the unit tests would stay green.
func TestTheRealStoreRefusesTheLastKey(t *testing.T) {
	store := realStore(t)
	customer := "cust_06G8LASTKEYREFUSED000000"
	require.NoError(t, store.Put(t.Context(), customer, aCredential("only-"+customer)))

	surviving, err := store.Remove(t.Context(), customer, []byte("only-"+customer), false)

	require.ErrorIs(t, err, identitypasskey.ErrLastWayIn)
	assert.Equal(t, 1, surviving, "the count is what the caller reports to the person")

	keys, err := store.ListForCustomer(t.Context(), customer)
	require.NoError(t, err)
	assert.Len(t, keys, 1, "the row is still there")
}

// TestTheRealStoreRemovesTheLastKeyWhenItIsAllowed is the other side of the same
// guard: the store does not decide policy, it enforces what it is told.
func TestTheRealStoreRemovesTheLastKeyWhenItIsAllowed(t *testing.T) {
	store := realStore(t)
	customer := "cust_06G8LASTKEYALLOWED00000"
	require.NoError(t, store.Put(t.Context(), customer, aCredential("only-"+customer)))

	surviving, err := store.Remove(t.Context(), customer, []byte("only-"+customer), true)

	require.NoError(t, err)
	assert.Equal(t, 0, surviving)

	keys, err := store.ListForCustomer(t.Context(), customer)
	require.NoError(t, err)
	assert.Empty(t, keys, "an account with a password may hold no passkeys at all")
}

// TestARemovalCannotReachAnotherPersonsKey is the ownership half.
//
// The removal names a credential id and nothing else, and credential ids are not
// secret — they are handed to every relying party the authenticator talks to. So
// the store has to refuse an id that exists and belongs to somebody else, and it
// has to refuse it the SAME way it refuses one that never existed.
//
// # The fixture is the test
//
// The first version of this gave the caller TWO keys and permission to remove
// their last, and it passed with the ownership check deleted — because the
// DELETE is itself scoped by customer_id, so it affected no rows and answered
// "not found" anyway. Two rules, one answer, nothing isolated.
//
// The caller here holds exactly ONE key and may not remove it. That separates
// them: without the ownership check the row count decides first and the store
// answers "that is the only way into this account" — about a key that is not
// theirs. It is the wrong answer and it is also a disclosure, since it is an
// answer ABOUT somebody else's credential id.
func TestARemovalCannotReachAnotherPersonsKey(t *testing.T) {
	store := realStore(t)
	owner := "cust_06G8REMOVEOWNER00000000"
	stranger := "cust_06G8REMOVESTRANGER0000"
	require.NoError(t, store.Put(t.Context(), owner, aCredential("key-of-"+owner)))
	require.NoError(t, store.Put(t.Context(), stranger, aCredential("key-of-"+stranger)))

	_, err := store.Remove(t.Context(), stranger, []byte("key-of-"+owner), false)

	require.ErrorIs(t, err, identitypasskey.ErrNoCredential,
		"somebody else's key is not found, which is the same answer as never existed")
	require.NotErrorIs(t, err, identitypasskey.ErrLastWayIn,
		"and it is decidedly NOT a sentence about how many ways into an account there are")

	keys, err := store.ListForCustomer(t.Context(), owner)
	require.NoError(t, err)
	assert.Len(t, keys, 1, "and the owner still has it")

	strangersKeys, err := store.ListForCustomer(t.Context(), stranger)
	require.NoError(t, err)
	assert.Len(t, strangersKeys, 1, "the caller's own key is untouched as well")
}

// TestTheListingAnswersOnePersonsRows checks the columns a client renders.
func TestTheListingAnswersOnePersonsRows(t *testing.T) {
	store := realStore(t)
	customer := "cust_06G8LISTINGROWS0000000"
	other := "cust_06G8LISTINGOTHER000000"
	require.NoError(t, store.Put(t.Context(), customer, aCredential("first-"+customer)))
	require.NoError(t, store.Put(t.Context(), customer, aCredential("second-"+customer)))
	require.NoError(t, store.Put(t.Context(), other, aCredential("first-"+other)))

	keys, err := store.ListForCustomer(t.Context(), customer)
	require.NoError(t, err)

	require.Len(t, keys, 2, "one person's rows and nobody else's")
	for _, key := range keys {
		assert.NotEmpty(t, key.ID)
		assert.False(t, key.CreatedAt.IsZero(), "the column the client sorts on")
		assert.Nil(t, key.LastUsedAt,
			"a key that has never signed anybody in has no last use, and that has to "+
				"arrive as null rather than as the zero time — a client formatting "+
				"0001-01-01 shows a date, and a date is a claim")
	}
	assert.NotEqual(t, keys[0].ID, keys[1].ID)
}

// raceRounds is how many times the overlap is forced.
//
// One forced round is already deterministic — the two removals are PROVEN to be
// in flight together before either is let go — so the rounds are not there to
// make a rare thing happen. They are there because an ordering that happens to
// be favourable is worth ruling out cheaply, and because this repository has
// been burned once by a one-round concurrency test that was green half the time
// (gap D46).
const raceRounds = 20

// TestTwoRemovalsCannotBOTHTakeTheLastKey is the lockout, executed.
//
// # What is being defended
//
// Two of a person's devices, each removing the OTHER's key at the same moment.
// Both read "there are two, so removing one is fine", both remove one, and the
// account has no way in. Nothing about the two requests is invalid on its own.
//
// # Why the store's guard has to be a LOCK and not a condition
//
// Measured in this repository: under READ COMMITTED, the guard written inside the
// DELETE as a subquery counting the customer's rows produces ZERO keys left,
// every run. Both statements see a snapshot taken before the other's delete, and
// both are correct about a world that is already gone. `SELECT … WHERE
// customer_id = $1 FOR UPDATE` in the same transaction as the DELETE serialises
// them, and the second one then counts one row and refuses.
//
// # Why a third transaction holds the gate
//
// The overlap has to be FORCED, not hoped for. The store opens its own
// transaction, so the test cannot step inside it — and the first probe written
// for this, which started both goroutines from one channel, reported "no race"
// five runs out of five because each goroutine opened its transaction after the
// barrier. A test that does not prove the overlap happened is a test of the
// scheduler.
//
// So a third transaction locks the customer's rows first. Both removals then
// queue behind it, and the test waits for PostgreSQL itself to report two
// backends waiting on a lock before releasing the gate. That condition is the
// same in both worlds, which is what makes it a fair barrier: with the row lock
// the two waiters are the SELECTs, and without it they are the DELETEs.
func TestTwoRemovalsCannotBOTHTakeTheLastKey(t *testing.T) {
	store := realStore(t)

	for round := range raceRounds {
		customer := fmt.Sprintf("cust_06G8RACE%013d", round)
		first := []byte(fmt.Sprintf("first-%s", customer))
		second := []byte(fmt.Sprintf("second-%s", customer))
		require.NoError(t, store.Put(t.Context(), customer, aCredential(string(first))))
		require.NoError(t, store.Put(t.Context(), customer, aCredential(string(second))))

		gate, err := testPool.Begin(t.Context())
		require.NoError(t, err)
		_, err = gate.Exec(t.Context(),
			`SELECT credential_id FROM passkey_credentials WHERE customer_id = $1 FOR UPDATE`,
			customer)
		require.NoError(t, err)

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, target := range [][]byte{first, second} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = store.Remove(context.Background(), customer, target, false)
			}()
		}

		waitForTwoBlockedBackends(t)
		require.NoError(t, gate.Commit(t.Context()))
		wg.Wait()

		succeeded := 0
		for _, err := range errs {
			if err == nil {
				succeeded++

				continue
			}
			require.ErrorIs(t, err, identitypasskey.ErrLastWayIn,
				"the loser is refused BY THE RULE, not by a deadlock or a serialisation "+
					"failure the caller would have to retry")
		}
		assert.Equal(t, 1, succeeded, "round %d: exactly one removal may win", round)

		keys, err := store.ListForCustomer(t.Context(), customer)
		require.NoError(t, err)
		assert.Len(t, keys, 1,
			"round %d: an account must not be left with no way in by two requests that "+
				"were each individually reasonable", round)
	}
}

// waitForTwoBlockedBackends blocks until PostgreSQL reports two backends waiting
// on a lock.
//
// It polls a CONDITION rather than sleeping a duration, which is the difference
// between a barrier and a guess. The deadline exists so a shape that can never
// reach the condition fails as a failure instead of hanging the suite.
func waitForTwoBlockedBackends(t *testing.T) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		require.NoError(t, testPool.QueryRow(t.Context(),
			`SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database()
			   AND state = 'active'
			   AND wait_event_type = 'Lock'`,
		).Scan(&waiting))
		if waiting >= 2 {
			return
		}
		require.False(t, time.Now().After(deadline),
			"two removals never queued behind the gate (%d waiting), so the overlap this "+
				"test exists to force did not happen and nothing was proved", waiting)
		time.Sleep(5 * time.Millisecond)
	}
}
