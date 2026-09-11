//go:build integration

package identitysession_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/personaldata"
)

// The tests in this file run the SQL.
//
// Everything beside them runs on a store the test wrote, which proves the
// session half and nothing about the storage half: two statements, three CHECKs
// and a unique index shipped in ADR 0127 with nothing ever executing them. A
// mistyped column compiles, and a CHECK that refuses a row the module writes is
// invisible until the first sign-in in production.
//
// The e-mail folding is the case worth naming. The store folds on the way in and
// the column CHECKs that it is folded, which is two rules for one fact — the
// kind of pair that agrees in a unit test because the same function wrote both
// sides. Only a real database can say the CHECK is the floor the store thinks it
// is.

// testHash is a stored password hash of the shape the CHECK insists on.
//
// It is a constant rather than a real derivation because nothing here is testing
// argon2: what these scenarios exercise is the SQL, and a derivation would add a
// second of CPU per row for no assertion.
const testHash = "$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$" +
	"aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g"

// testPool is the pool the scenarios share.
var testPool *pgxpool.Pool

// testDSN is the connection string of the container.
var testDSN string

// TestMain raises one Postgres and applies this module's migrations to it.
func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres is separate because os.Exit skips defers.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("identity_session_test"),
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

	// The module's own migrations, applied the way the lifecycle applies them.
	// That this call works at all is half the test: an embedder's module reaches
	// core/db from outside, or it cannot install its own schema.
	if err := db.Migrate(ctx, testDSN,
		identitysession.New(identitysession.Options{}).Migrations(),
		identitysession.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)

		return 1
	}

	return m.Run()
}

// registered builds a module on the REAL store.
func registered(t *testing.T) *identitysession.Module {
	t.Helper()

	c := container.New(nil)
	pool, err := db.New(t.Context(), db.DefaultConfig(testDSN), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, c.Provide("core.db", pool))

	m := identitysession.New(identitysession.Options{
		Secret: []byte("an integration signing secret of thirty-two"),
	})
	require.NoError(t, m.Register(t.Context(), c))

	return m
}

// TestTheSchemaIsWhatTheModuleWrites is the witness under every other test here.
func TestTheSchemaIsWhatTheModuleWrites(t *testing.T) {
	var columns int
	require.NoError(t, testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM information_schema.columns WHERE table_name = 'customer_credentials'`,
	).Scan(&columns))
	assert.Equal(t, 5, columns,
		"customer_id, email, password_hash, created_at, updated_at")

	var checks int
	require.NoError(t, testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM pg_constraint
		 WHERE conrelid = 'customer_credentials'::regclass AND contype = 'c'`,
	).Scan(&checks))
	assert.Equal(t, 3, checks,
		"the hash shape, the folded address and the non-blank address")
}

// TestACredentialRoundTrips runs both statements against the real table.
func TestACredentialRoundTrips(t *testing.T) {
	m := registered(t)
	hash, err := identitysession.HashPassword("a password")
	require.NoError(t, err)

	customerID := "cust_" + t.Name()
	require.NoError(t, m.Credentials().Put(t.Context(), customerID, "roundtrip@example.test", hash))

	gotID, gotHash, err := m.Credentials().Credential(t.Context(), "roundtrip@example.test")
	require.NoError(t, err)
	assert.Equal(t, customerID, gotID)
	require.NoError(t, identitysession.VerifyPassword(gotHash, "a password"),
		"what came back has to be the hash that went in")
}

// TestAnUnknownAddressIsAMismatchAndNotANotFound holds the rule that keeps the
// sign-in endpoint from being an address enumerator, at the layer that could
// break it.
func TestAnUnknownAddressIsAMismatchAndNotANotFound(t *testing.T) {
	m := registered(t)

	_, _, err := m.Credentials().Credential(t.Context(), "nobody@example.test")

	assert.ErrorIs(t, err, identitysession.ErrPasswordMismatch,
		"a not-found error here would reach the endpoint as a different status")
}

// TestTheAddressIsFoldedOnBothSides proves the store and the CHECK agree with a
// real database rather than with each other.
func TestTheAddressIsFoldedOnBothSides(t *testing.T) {
	m := registered(t)
	hash, err := identitysession.HashPassword("a password")
	require.NoError(t, err)

	customerID := "cust_" + t.Name()
	require.NoError(t, m.Credentials().Put(t.Context(), customerID, "  MiXeD@Example.TEST  ", hash))

	for _, spelling := range []string{
		"mixed@example.test", "MIXED@EXAMPLE.TEST", " MiXeD@Example.Test ",
	} {
		gotID, _, credErr := m.Credentials().Credential(t.Context(), spelling)
		require.NoError(t, credErr, "%q must find the same account", spelling)
		assert.Equal(t, customerID, gotID)
	}
}

// TestOneCustomerKeepsOneRow proves the conflict target is the customer.
//
// A person changing their address keeps their account; a second row would be a
// second account they cannot tell apart from the first.
func TestOneCustomerKeepsOneRow(t *testing.T) {
	m := registered(t)
	first, err := identitysession.HashPassword("the first password")
	require.NoError(t, err)
	second, err := identitysession.HashPassword("the second password")
	require.NoError(t, err)

	customerID := "cust_" + t.Name()
	require.NoError(t, m.Credentials().Put(t.Context(), customerID, "before@example.test", first))
	require.NoError(t, m.Credentials().Put(t.Context(), customerID, "after@example.test", second))

	var rows int
	require.NoError(t, testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM customer_credentials WHERE customer_id = $1`, customerID,
	).Scan(&rows))
	assert.Equal(t, 1, rows, "the customer keeps ONE credential")

	_, _, err = m.Credentials().Credential(t.Context(), "before@example.test")
	assert.ErrorIs(t, err, identitysession.ErrPasswordMismatch, "the old address is gone")

	_, gotHash, err := m.Credentials().Credential(t.Context(), "after@example.test")
	require.NoError(t, err)
	assert.NoError(t, identitysession.VerifyPassword(gotHash, "the second password"))
}

// TestOneAddressBelongsToOneCustomer is the unique index doing the work the
// store cannot.
func TestOneAddressBelongsToOneCustomer(t *testing.T) {
	m := registered(t)
	hash, err := identitysession.HashPassword("a password")
	require.NoError(t, err)

	require.NoError(t, m.Credentials().Put(t.Context(), "cust_"+t.Name()+"_first", "shared@example.test", hash))
	err = m.Credentials().Put(t.Context(), "cust_"+t.Name()+"_second", "shared@example.test", hash)

	require.Error(t, err,
		"two customers holding one address would make a sign-in answer whichever row "+
			"the planner reached first")
}

// TestTheChecksRefuseWhatTheStoreWouldNeverWrite runs them with raw SQL.
//
// Going around the store is the point: the CHECKs are the FLOOR under it, and a
// floor only tested through the thing standing on it is not known to be there.
func TestTheChecksRefuseWhatTheStoreWouldNeverWrite(t *testing.T) {
	hash, err := identitysession.HashPassword("a password")
	require.NoError(t, err)

	for name, row := range map[string][3]string{
		"a hash that is not argon2id": {"cust_check_1", "check1@example.test", "$2y$10$notargon"},
		"an unfolded address":         {"cust_check_2", "Check2@Example.TEST", hash},
		"a blank address":             {"cust_check_3", "   ", hash},
	} {
		t.Run(name, func(t *testing.T) {
			_, execErr := testPool.Exec(t.Context(),
				`INSERT INTO customer_credentials (customer_id, email, password_hash)
				 VALUES ($1, $2, $3)`, row[0], row[1], row[2])

			require.Error(t, execErr, "the database has to refuse %s", name)
			assert.Contains(t, execErr.Error(), "customer_credentials_",
				"the refusal names the constraint, which is what an operator reads")
		})
	}
}

// TestSigningInAgainstTheRealStore is the whole chain in one request.
//
// Every other test here takes one half. This one walks what an operator and a
// shopper really do: the credential is written over the admin endpoint, the
// shopper signs in over the storefront one, and the cookie that comes back
// proves the customer the operator named.
func TestSigningInAgainstTheRealStore(t *testing.T) {
	m := registered(t)
	r := chi.NewRouter()
	m.Routes(r)

	customerID := "cust_" + t.Name()
	written := send(t, r, http.MethodPut, "/admin/v1/customer-credentials",
		fmt.Sprintf(`{"customer_id":%q,"email":"chain@example.test","password":"a real password"}`,
			customerID))
	require.Equal(t, http.StatusNoContent, written.Code, "body: %s", written.Body.String())

	wrong := send(t, r, http.MethodPost, "/store/v1/auth/sign-in",
		`{"email":"chain@example.test","password":"not it"}`)
	require.Equal(t, http.StatusUnauthorized, wrong.Code)
	assert.Empty(t, wrong.Result().Cookies(), "no cookie for a wrong password")

	in := send(t, r, http.MethodPost, "/store/v1/auth/sign-in",
		`{"email":"CHAIN@example.test","password":"a real password"}`)
	require.Equal(t, http.StatusNoContent, in.Code, "body: %s", in.Body.String())

	cookies := in.Result().Cookies()
	require.Len(t, cookies, 1, "the sign-in sets exactly the session cookie")

	proved := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/store/v1/customers/"+customerID, http.NoBody)
	proved.Header.Set("Cookie", cookies[0].Name+"="+cookies[0].Value)

	id, err := m.Sessions().CustomerID(proved)
	require.NoError(t, err)
	assert.Equal(t, customerID, id,
		"the cookie proves the customer the credential named, folding and all")
}

// send runs a JSON request against the router.
func send(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// TestAnErasureTakesThePersonsCredentialRow is the data-subject sweep's half.
func TestAnErasureTakesThePersonsCredentialRow(t *testing.T) {
	m := registered(t)
	store := m.Credentials()
	customer := "cust_06G8SESSIONERASED00000"
	stranger := "cust_06G8SESSIONKEPT0000000"

	require.NoError(t, store.Put(t.Context(), customer, "erased@example.test", testHash))
	require.NoError(t, store.Put(t.Context(), stranger, "kept@example.test", testHash))

	result, err := m.Erase(t.Context(), personaldata.Subject{CustomerID: customer})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Deleted, result.Outcome)
	assert.Equal(t, 1, result.Rows)

	_, _, err = store.Credential(t.Context(), "erased@example.test")
	require.Error(t, err, "the row is gone")

	_, _, err = store.Credential(t.Context(), "kept@example.test")
	require.NoError(t, err, "and nobody else was touched")
}

// TestAnErasureByADDRESSAlsoFindsThePerson is the handle the passkey module does
// not have.
//
// This module stores the address, so a subject that names only one is resolvable
// here — and the fold matters: a request carrying "Person@Example.test" has to
// reach the row written as "person@example.test", or an erasure quietly deletes
// nothing and reports success.
func TestAnErasureByADDRESSAlsoFindsThePerson(t *testing.T) {
	m := registered(t)
	require.NoError(t, m.Credentials().Put(t.Context(),
		"cust_06G8SESSIONBYADDRESS00", "by-address@example.test", testHash))

	result, err := m.Erase(t.Context(),
		personaldata.Subject{Email: "  By-Address@Example.test  "})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Deleted, result.Outcome)
	assert.Equal(t, 1, result.Rows, "the address is folded and trimmed before it is used")
}

// TestADossierNamesTheHashAndDoesNotReproduceIt is the one field that is reported
// by description.
//
// Dropping the column would make the dossier false about what is held; printing it
// would put somebody's password hash into whatever carries the answer — an e-mail,
// a ticket, a support tool. So the field is present and its value is a sentence.
func TestADossierNamesTheHashAndDoesNotReproduceIt(t *testing.T) {
	m := registered(t)
	customer := "cust_06G8SESSIONDOSSIER0000"
	require.NoError(t, m.Credentials().Put(t.Context(), customer, "dossier@example.test", testHash))

	disclosure, err := m.PersonalDataOf(t.Context(), personaldata.Subject{CustomerID: customer})
	require.NoError(t, err)

	require.Equal(t, personaldata.Disclosed, disclosure.State)
	require.Len(t, disclosure.Records, 1)

	fields := map[string]any{}
	for _, field := range disclosure.Records[0].Fields {
		fields[field.Column] = field.Value
	}

	assert.Equal(t, "dossier@example.test", fields["email"])
	assert.Equal(t, customer, fields["customer_id"])

	hash, ok := fields["password_hash"]
	require.True(t, ok, "the column is DECLARED, so leaving it out of the dossier would "+
		"make the answer false about what is held")
	assert.NotContains(t, hash, "$argon2id$",
		"and the stored value must not travel: it is derived from the person's own secret")
	assert.Contains(t, hash, "not reproduced")

	// The hash is not read out of the database either, which is the same decision
	// one layer down: a secret that is never going to be reported should not be in
	// this process's memory to begin with.
	for _, record := range disclosure.Records {
		for _, field := range record.Fields {
			text, isText := field.Value.(string)
			if isText {
				assert.NotContains(t, text, testHash,
					"no field carries the stored hash")
			}
		}
	}
}

// TestADossierForSomebodyWithNoPasswordSaysNOTHING keeps the two kinds of empty
// apart.
func TestADossierForSomebodyWithNoPasswordSaysNOTHING(t *testing.T) {
	m := registered(t)

	disclosure, err := m.PersonalDataOf(t.Context(),
		personaldata.Subject{CustomerID: "cust_06G8SESSIONNOBODY0000"})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Nothing, disclosure.State,
		"this store CAN search and found nothing, which is a different answer from "+
			"a store that cannot search")
	assert.Empty(t, disclosure.Records)
}
