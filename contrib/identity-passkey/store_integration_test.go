//go:build integration

package identitypasskey_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"testing"

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
