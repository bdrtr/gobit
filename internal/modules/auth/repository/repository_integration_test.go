//go:build integration

// The tests in this file require a real PostgreSQL instance (and therefore
// Docker); they are kept behind the `integration` tag so that `make test` stays
// fast. To run them: make test-integration
//
// The in-package unit test ([TestWrapDBSeparatesSQLSTATEClasses]) proves the
// DECISION of the error classification. The tests here prove the GROUND that
// decision stands on: that the uniqueness rules in the schema are really
// enforced, that no link can be made to a soft-deleted channel, that the key
// and its links are written in a single transaction, and that PostgreSQL really
// produces the expected SQLSTATE. None of these can be tested with a fake
// driver.
package repository_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

const postgresImage = "postgres:16-alpine"

// testPool is the pool shared by all of the tests.
var testPool *db.Pool

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container and runs all of the
// tests on it. It is a separate function because os.Exit skips defers.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_auth"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "could not stop the postgres container: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not start the postgres container: %v\n", err)
		return 1
	}

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not read the connection string: %v\n", err)
		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(dsn), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not open the connection pool: %v\n", err)
		return 1
	}
	defer testPool.Close()

	// The migration source is the module's own embedded file system: the schema
	// the test applies is the very one the server applies at startup.
	if err := db.Migrate(ctx, dsn, auth.New(auth.Options{}).Migrations(), auth.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "could not apply the migrations: %v\n", err)
		return 1
	}

	return m.Run()
}

// newRepo produces a repository working on the real pool.
func newRepo(t *testing.T) *repository.Repo {
	t.Helper()

	return repository.New(testPool.Pool())
}

// newUser writes an admin user with a unique email for the test.
func newUser(ctx context.Context, t *testing.T, repo *repository.Repo) models.User {
	t.Helper()

	now := time.Now().UTC()
	id := models.NewUserID(now)
	// The email must be LOWERCASE (auth_user_email_check); the body of the id
	// carries uppercase because it is Crockford Base32.
	user, err := repo.CreateUser(ctx, models.User{
		ID:        id,
		Email:     strings.ToLower("u" + id[len(models.UserIDPrefix):] + "@example.test"),
		Scopes:    []string{models.ScopeAdmin},
		CreatedAt: now,
	}, nil)
	require.NoError(t, err)

	return user
}

// newChannel writes a sales channel with a unique name for the test.
func newChannel(ctx context.Context, t *testing.T, repo *repository.Repo) models.SalesChannel {
	t.Helper()

	now := time.Now().UTC()
	id := models.NewSalesChannelID(now)
	channel, err := repo.CreateSalesChannel(ctx, models.SalesChannel{
		ID:        id,
		Name:      "channel " + id,
		CreatedAt: now,
	})
	require.NoError(t, err)

	return channel
}

// newKeyRecord produces a publishable API key record ready to be written.
//
// The plain text is not returned: none of these tests is interested in the key
// itself, only in whether the row was written.
func newKeyRecord(t *testing.T) models.APIKey {
	t.Helper()

	plaintext, err := models.NewToken(models.APIKeyPublishable)
	require.NoError(t, err)

	now := time.Now().UTC()
	return models.APIKey{
		ID:        models.NewAPIKeyID(now),
		Type:      models.APIKeyPublishable,
		Title:     "test key " + now.Format(time.RFC3339Nano),
		TokenHash: models.HashToken(plaintext),
		Redacted:  models.RedactToken(plaintext),
		Scopes:    []string{},
		CreatedAt: now,
	}
}

// countRows runs a single-column count query.
func countRows(ctx context.Context, t *testing.T, sql string, args ...any) int64 {
	t.Helper()

	var n int64
	require.NoError(t, testPool.Pool().QueryRow(ctx, sql, args...).Scan(&n))

	return n
}

// TestThereIsOneIdentityPerUserPerProvider proves that the
// auth_identity_user_provider_uniq index is really enforced.
//
// Why the rule is required: the identity is read as a SINGLE row by (user_id,
// provider), and the password, the attempt counter and the lock are always
// written to that row. Had a second row been possible, which of them gets read
// would become ambiguous and the lock counter would be split in two. Because a
// second row cannot be opened through the repository surface, it is attempted
// with raw SQL: what is tested is not the guarantee given by the CODE but the
// one given by the SCHEMA.
func TestThereIsOneIdentityPerUserPerProvider(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	user := newUser(ctx, t, repo)
	now := time.Now().UTC()

	_, err := repo.SetPasswordHash(ctx, user.ID, models.ProviderEmailPass, user.Email, "hash-1", now)
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO auth_identity (id, user_id, provider, provider_identity)
		 VALUES ($1, $2, $3, $4)`,
		models.NewAuthIdentityID(now), user.ID, models.ProviderEmailPass, "second-"+user.Email)
	require.Error(t, err, "a second identity could be written for the same user and provider")

	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr))
	assert.Equal(t, "23505", pgErr.Code)
	assert.Equal(t, "auth_identity_user_provider_uniq", pgErr.ConstraintName)
}

// TestSettingPasswordDoesNotOpenASecondIdentity proves that
// [repository.Repo.SetPasswordHash] UPDATES the existing identity instead of
// opening a new row.
//
// That is what the uniqueness constraint amounts to in practice: when the "set
// password" call is repeated, the number of identities must stay the same.
func TestSettingPasswordDoesNotOpenASecondIdentity(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	user := newUser(ctx, t, repo)
	now := time.Now().UTC()

	first, err := repo.SetPasswordHash(ctx, user.ID, models.ProviderEmailPass, user.Email, "hash-1", now)
	require.NoError(t, err)

	second, err := repo.SetPasswordHash(ctx, user.ID, models.ProviderEmailPass, user.Email, "hash-2", now.Add(time.Second))
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID, "the second call must not open a new identity")
	assert.Equal(t, "hash-2", second.PasswordHash)
	assert.Equal(t, int64(1), countRows(ctx,
		t, `SELECT count(*) FROM auth_identity WHERE user_id = $1 AND deleted_at IS NULL`, user.ID))
}

// TestLogoutAdvancesTheAnchorAndLeavesCredentialsUntouched proves the CONTRACT
// of the [repository.Repo.RevokeSessions] query on a real database.
//
// Both halves matter equally:
//
//   - updated_at MUST ADVANCE — it is the anchor session revocation rests on;
//     had it stayed put, the logout endpoint would return 200 and drop no token
//     at all.
//   - password_hash and the lock counters MUST STAY PUT — had the password
//     changed, the user could never log in again; had the counter been reset,
//     the logout endpoint would become the way to clear the login lock.
//
// The contract can only be tested with real SQL: a fake repository cannot see
// what the query writes in its SET list.
func TestLogoutAdvancesTheAnchorAndLeavesCredentialsUntouched(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	user := newUser(ctx, t, repo)
	now := time.Now().UTC()

	before, err := repo.SetPasswordHash(ctx, user.ID, models.ProviderEmailPass, user.Email, "hash-1", now)
	require.NoError(t, err)

	// The lock is set up BEFORE the logout; whether it is preserved can only be
	// seen on a record that is really locked. The threshold is 1: a single
	// failed attempt locks it.
	locked, err := repo.RegisterLoginFailure(ctx, before.ID, 1, now.Add(time.Minute), now)
	require.NoError(t, err)
	require.Equal(t, 1, locked.FailedAttempts)
	require.True(t, locked.IsLocked(now), "test ground: the record must be locked before the logout")

	logout := now.Add(2 * time.Second)
	advanced, err := repo.RevokeSessions(ctx, user.ID, logout)
	require.NoError(t, err)
	require.Len(t, advanced, 1, "the user has a single identity, a single row must come back")

	after := advanced[0]
	assert.Equal(t, before.ID, after.ID, "the logout must not open a new identity row")
	assert.True(t, after.UpdatedAt.After(before.UpdatedAt),
		"the session anchor must advance: before %s, after %s", before.UpdatedAt, after.UpdatedAt)
	assert.Equal(t, "hash-1", after.PasswordHash, "the logout must not change the password")
	assert.Equal(t, 1, after.FailedAttempts, "the logout must not reset the failed attempt counter")
	assert.True(t, after.IsLocked(logout),
		"the logout must not lift the login lock; had it done so, it would be the way to bypass the lock")
}

// TestLogoutAdvancesTheAnchorOfEveryProvider proves on a real database that the
// logout query SELECTS NO provider.
//
// # What the test proves
//
// Two things together:
//
//   - the anchor of the emailpass identity advances as before — today's
//     behavior is preserved, there is NO observable change (today there is a
//     single live provider),
//   - the anchor of a SECOND provider row set up by hand advances as well.
//
// The second one appears in no user flow today; what it locks down is the
// behavior on the day OAuth is added. Had the query selected a single provider,
// the logout would not drop that provider's tokens and it would do so SILENTLY.
//
// The second row is written with raw SQL: the repository surface has no "open
// an OAuth identity" endpoint and must not have one. What is tested is not the
// CODE but that the query touches every row IN THE SCHEMA.
func TestLogoutAdvancesTheAnchorOfEveryProvider(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	user := newUser(ctx, t, repo)
	now := time.Now().UTC()

	emailpass, err := repo.SetPasswordHash(ctx, user.ID, models.ProviderEmailPass, user.Email, "hash-1", now)
	require.NoError(t, err)

	// The provider name is a raw string: the models package holds only the
	// emailpass constant, and adding a constant there for a provider that is
	// not implemented would make it look as if there were a login path the code
	// does not support.
	const secondProvider = "google"
	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO auth_identity (id, user_id, provider, provider_identity, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $5)`,
		models.NewAuthIdentityID(now), user.ID, secondProvider, "oauth-sub-"+user.ID, now)
	require.NoError(t, err, "the schema must allow a second identity per provider")

	logout := now.Add(2 * time.Second)
	advanced, err := repo.RevokeSessions(ctx, user.ID, logout)
	require.NoError(t, err)

	require.Len(t, advanced, 2, "the logout must touch both identities")
	anchors := map[string]time.Time{}
	for _, identity := range advanced {
		anchors[identity.Provider] = identity.UpdatedAt
	}
	assert.True(t, anchors[models.ProviderEmailPass].After(emailpass.UpdatedAt),
		"the anchor of the emailpass identity must advance; today's behavior must be preserved")
	assert.True(t, anchors[secondProvider].After(now),
		"the anchor of the second provider must advance too — had it not, tokens issued by "+
			"that provider would still be accepted after the logout")

	// The read side must see the same write; had they diverged, the extra
	// anchor written would never be read and the logout would stay ineffective
	// for that provider.
	anchor, err := repo.SessionAnchor(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, anchors[secondProvider], anchor,
		"the anchor that is read must be the most recent anchor written")
}

// TestSessionAnchorIsReadFromTheNewestProvider proves that the read query
// SELECTS NO provider and takes the furthest of the rows.
//
// The asymmetry is set up on purpose: only the anchor of the second provider is
// advanced, the emailpass row is left where it is. Had the query looked at a
// fixed provider (or taken the OLDEST anchor), this advance would become
// invisible and the revocation on that provider would drop no token at all.
func TestSessionAnchorIsReadFromTheNewestProvider(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	user := newUser(ctx, t, repo)
	now := time.Now().UTC()

	_, err := repo.SetPasswordHash(ctx, user.ID, models.ProviderEmailPass, user.Email, "hash-1", now)
	require.NoError(t, err)

	ahead := now.Add(time.Hour)
	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO auth_identity (id, user_id, provider, provider_identity, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		models.NewAuthIdentityID(now), user.ID, "google", "oauth-sub-"+user.ID, now, ahead)
	require.NoError(t, err)

	anchor, err := repo.SessionAnchor(ctx, user.ID)
	require.NoError(t, err)

	// The expected value is truncated to microseconds: the timestamptz column
	// has microsecond resolution and Go's nanoseconds are dropped on write. Had
	// it not been truncated, the assertion would break for a reason unrelated
	// to the rule it tests.
	assert.Equal(t, ahead.Truncate(time.Microsecond), anchor,
		"the anchor must be the MOST RECENT value across the providers")
}

// TestUserWithoutIdentityCannotLogOut proves that the logout does not succeed
// SILENTLY for a user that has no login identity at all.
//
// If there is no row to write, there is no anchor written either; returning
// success would present a logout that dropped nothing as a success. The check
// is done by hand: a multi-row UPDATE does NOT raise a "no rows" error, it
// returns an empty set.
func TestUserWithoutIdentityCannotLogOut(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	user := newUser(ctx, t, repo)

	_, err := repo.RevokeSessions(ctx, user.ID, time.Now().UTC())

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "expected kind NotFound, got: %s", errors.KindOf(err))
	assert.Equal(t, repository.CodeIdentityNotFound, errors.CodeOf(err))
}

// TestAnchorOfUserWithoutIdentityCannotBeRead proves that reading the anchor of
// a user with no identity does not silently return the zero time.
//
// Had the zero time been returned, EVERY token would count as minted after it
// and the token of a user whose identity was deleted would be accepted until it
// expired; that is, the check could be bypassed by deleting the identity row.
func TestAnchorOfUserWithoutIdentityCannotBeRead(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	user := newUser(ctx, t, repo)

	_, err := repo.SessionAnchor(ctx, user.ID)

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "expected kind NotFound, got: %s", errors.KindOf(err))
	assert.Equal(t, repository.CodeIdentityNotFound, errors.CodeOf(err))
}

// TestKeyCannotBeLinkedToADeletedChannel proves that no link is made to a
// soft-deleted channel.
//
// The foreign key does not catch this case: the deleted row stays in place and
// passes the FK. Had the link been possible, the key would be BORN DEAD — it
// would look "linked to a channel" on the admin surface, find no channel at all
// on a store request and be rejected.
func TestKeyCannotBeLinkedToADeletedChannel(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	channel := newChannel(ctx, t, repo)
	now := time.Now().UTC()

	key, err := repo.CreateAPIKey(ctx, newKeyRecord(t))
	require.NoError(t, err)
	require.NoError(t, repo.DeleteSalesChannel(ctx, channel.ID, now))

	err = repo.LinkSalesChannel(ctx, key.ID, channel.ID, now)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a deleted channel must be 'not found', kind: %s", errors.KindOf(err))
	assert.Equal(t, repository.CodeSalesChannelNotFound, errors.CodeOf(err))
	assert.Equal(t, int64(0), countRows(ctx,
		t, `SELECT count(*) FROM api_key_sales_channel WHERE api_key_id = $1`, key.ID))
}

// TestKeyCanBeLinkedToALiveChannel proves that the check closes the right door
// and does not close the door altogether.
//
// A disabled (is_disabled) channel can be linked as well: being disabled is not
// the same as being deleted, and the admin surface must be able to set up the
// link first and enable the channel afterwards.
func TestKeyCanBeLinkedToALiveChannel(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	channel := newChannel(ctx, t, repo)
	now := time.Now().UTC()

	key, err := repo.CreateAPIKey(ctx, newKeyRecord(t))
	require.NoError(t, err)

	require.NoError(t, repo.LinkSalesChannel(ctx, key.ID, channel.ID, now))
	// Repeating the same link is not an error: a link is a set, it carries no
	// multiplicity.
	require.NoError(t, repo.LinkSalesChannel(ctx, key.ID, channel.ID, now))

	ids, err := repo.ChannelIDsOfKey(ctx, key.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{channel.ID}, ids)

	disabled := true
	_, err = repo.UpdateSalesChannel(ctx, channel.ID, models.SalesChannelPatch{IsDisabled: &disabled}, now)
	require.NoError(t, err)

	secondKey, err := repo.CreateAPIKey(ctx, newKeyRecord(t))
	require.NoError(t, err)
	assert.NoError(t, repo.LinkSalesChannel(ctx, secondKey.ID, channel.ID, now),
		"a disabled channel must not count as deleted")
}

// TestKeyAndItsLinksAreWrittenInOneTransaction proves that when a link cannot
// be made the key row DOES NOT REMAIN either.
//
// Had the write been split into two transactions, what would be left behind is
// a key whose plain text never reached the caller: unusable because nobody
// knows it, uncompletable because its plain text can never be produced again —
// merely a garbage row to be deleted by hand.
func TestKeyAndItsLinksAreWrittenInOneTransaction(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	live := newChannel(ctx, t, repo)
	dead := newChannel(ctx, t, repo)
	require.NoError(t, repo.DeleteSalesChannel(ctx, dead.ID, time.Now().UTC()))

	record := newKeyRecord(t)
	_, err := repo.CreateAPIKeyWithChannels(ctx, record, []string{live.ID, dead.ID})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))

	// Not even a tombstone remains: once the transaction is rolled back the row
	// has never existed.
	assert.Equal(t, int64(0), countRows(ctx,
		t, `SELECT count(*) FROM api_key WHERE id = $1`, record.ID))
	assert.Equal(t, int64(0), countRows(ctx,
		t, `SELECT count(*) FROM api_key_sales_channel WHERE api_key_id = $1`, record.ID),
		"the first link must be rolled back as well")
	_, err = repo.GetAPIKeyByHash(ctx, record.TokenHash)
	assert.True(t, errors.IsNotFound(err), "a rolled-back key must not be readable from anywhere")
}

// TestKeyAndItsLinksAreWrittenTogetherOnTheSuccessPath proves that on the
// success path the write leaves behind both the key and the links.
func TestKeyAndItsLinksAreWrittenTogetherOnTheSuccessPath(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	first := newChannel(ctx, t, repo)
	second := newChannel(ctx, t, repo)

	key, err := repo.CreateAPIKeyWithChannels(ctx, newKeyRecord(t), []string{first.ID, second.ID})
	require.NoError(t, err)

	ids, err := repo.ChannelIDsOfKey(ctx, key.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{first.ID, second.ID}, ids)
}

// plainRepo is a wrapper that exposes only the [service.Repository] surface.
//
// The embedded interface carries only ITS OWN methods:
// CreateAPIKeyWithChannels stays outside and the service's type assertion
// fails. That makes the COMPENSATION path — the one taken with a repository
// that cannot open a transaction — testable; without that path, every service
// test built on fake repositories would leave a garbage key behind it.
type plainRepo struct{ service.Repository }

// TestKeyIsRolledBackWhenTheLinkFailsOnANonTransactionalRepo proves that the
// compensation path really removes the key.
//
// On an atomic repository the row never comes into existence; here it does come
// into existence and is soft-deleted — what is left behind is a tombstone that
// can be read from nowhere. Both of them keep the contract "a key that reached
// nobody's hands cannot be used".
func TestKeyIsRolledBackWhenTheLinkFailsOnANonTransactionalRepo(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	dead := newChannel(ctx, t, repo)
	require.NoError(t, repo.DeleteSalesChannel(ctx, dead.ID, time.Now().UTC()))

	svc := service.New(plainRepo{repo}, service.Options{JWTSecret: "a-secret-used-only-for-testing"})
	title := "compensation " + time.Now().UTC().Format(time.RFC3339Nano)

	_, plaintext, err := svc.CreateAPIKey(ctx, service.CreateAPIKeyInput{
		Type:            models.APIKeyPublishable,
		Title:           title,
		SalesChannelIDs: []string{dead.ID},
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.Empty(t, plaintext, "a failed call must not leak the plain text")

	assert.Equal(t, int64(0), countRows(ctx,
		t, `SELECT count(*) FROM api_key WHERE title = $1 AND deleted_at IS NULL`, title),
		"a key whose link could not be made must not stay live")
}

// TestDataExceptionProducesAClientError proves on a REAL server that the 22xxx
// class returns a 422 and not a 500.
//
// jsonb cannot turn the JSON escape of a NUL byte inside text into text and
// produces 22P05. That value comes entirely FROM THE CLIENT: the caller chooses
// what goes into the metadata field. Had the class not been recognized, a
// character written by the client would be reported as a server error and the
// caller would retry instead of fixing the request.
func TestDataExceptionProducesAClientError(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	now := time.Now().UTC()

	_, err := repo.CreateSalesChannel(ctx, models.SalesChannel{
		ID:        models.NewSalesChannelID(now),
		Name:      "channel " + now.Format(time.RFC3339Nano),
		Metadata:  map[string]any{"note": "\x00"},
		CreatedAt: now,
	})
	require.Error(t, err)

	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr))
	require.Equal(t, "22", pgErr.Code[:2], "expected the data exception class (22xxx), got: %s", pgErr.Code)
	assert.True(t, errors.IsInvalid(err),
		"a data exception must be a client error, kind: %s", errors.KindOf(err))
	assert.Equal(t, repository.CodeConstraintViolation, errors.CodeOf(err))
	// The suffix is searched for and not the bare word "constraint": the error
	// CODE (auth_constraint_violation) is part of Error().
	assert.NotContains(t, err.Error(), "(constraint:",
		"no half suffix must be added when there is no constraint name")
}
