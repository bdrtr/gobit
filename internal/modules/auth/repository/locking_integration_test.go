//go:build integration

// This file holds the module's CONCURRENCY claims — the ones that can only be
// shown against a real server, with a real competing transaction and real row
// locks.
//
// Every test here has the same shape, and it is the shape the module's other
// integration tests cannot produce: a competing transaction takes the row a
// write is about to depend on, the write is observed to STOP, and only then
// does the competitor delete the row and commit. Sequential tests
// (TestKeyCannotBeLinkedToADeletedChannel and its neighbors) prove that an
// ALREADY deleted row is refused; they say nothing about a row that is deleted
// WHILE the write is deciding, and that is the case that produced a real defect.
//
// One of the claims here was MEASURED false (2026-09-06) before it was fixed:
// [service.Service.SetPassword] read the user and wrote the identity in two
// separate autocommit statements, a delete driven into the gap left a LIVE
// identity under a deleted user, and creating a new administrator at that
// address then failed with a conflict forever. The account of the defect and of
// the fix is in repository/identity.go, SetPasswordHash.
package repository_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// racePassword is the password used by the tests in this file.
//
// Its content carries no meaning; only its length does (service.MinPasswordLen).
const racePassword = "correct-horse-battery-staple"

// newRaceService produces a service running on the real repository.
//
// The bcrypt cost is pulled down to the minimum: these tests are about locking,
// not about hashing cost, and at the default cost (12) every call would spend
// ~250 ms producing a hash nobody verifies.
func newRaceService(repo service.Repository) *service.Service {
	return service.New(repo, service.Options{
		JWTSecret:  "a-secret-used-only-for-testing",
		BcryptCost: bcrypt.MinCost,
	})
}

// blockedRequestCount returns how many requests are waiting on a lock HELD BY
// the given backend.
//
// Narrowing it to a known blocker with pg_blocking_pids is what makes the
// waiting assertion mean something. "Somebody in this database is waiting on a
// lock" is also satisfied by another test's session, and under that condition
// the assertion below would pass before the request under test had run a single
// statement — the test would be green while measuring nothing.
func blockedRequestCount(ctx context.Context, t *testing.T, blockerPID int32) int64 {
	t.Helper()

	return countRows(ctx, t,
		`SELECT count(*) FROM pg_stat_activity
         WHERE datname = current_database()
           AND wait_event_type = 'Lock'
           AND $1 = ANY(pg_blocking_pids(pid))`, blockerPID)
}

// requireBlockedRequest waits until the given backend really holds somebody up.
//
// The wait state is polled rather than slept through: a fixed sleep would
// either wake early on a loaded machine and make the test flaky, or add dead
// time to every run.
func requireBlockedRequest(ctx context.Context, t *testing.T, blockerPID int32) {
	t.Helper()

	require.Eventually(t, func() bool {
		return blockedRequestCount(ctx, t, blockerPID) > 0
	}, 10*time.Second, 10*time.Millisecond,
		"the write under test had to wait on this session's lock")
}

// TestSetPasswordWaitsForAnInFlightDeleteAndThenRefuses drives a delete into the
// exact moment a password is being assigned.
//
// The interleaving is produced rather than hoped for. A competing transaction
// takes the user row EXCLUSIVELY — which is what the delete's own UPDATE takes —
// and holds it; the password write then has to reach that row and stop. Only
// after the wait is observed does the competing transaction perform the soft
// delete and commit, so the write can wake up into no world but the one where
// the user is already gone.
//
// # What the WAIT proves, and what it does not
//
// The wait alone proves nothing about the fix, and saying otherwise would be
// the mistake this whole exercise exists to stop making. It was MEASURED: with
// FOR SHARE stripped out of LockLiveUser the write STILL waits here, because
// InsertIdentity's foreign key takes a KEY SHARE lock on the parent row and
// that conflicts with FOR UPDATE just as well. The foreign key reaches the row,
// waits for it, and then does not object to it — it looks at PHYSICAL existence
// and a soft delete leaves the row in place. Under that mutation this test
// fails on the two assertions below, which are the ones carrying the proof:
// the call returns NOT FOUND, and the user is left with no live identity.
//
// requireBlockedRequest is therefore not the claim; it is what makes the
// interleaving deterministic instead of hoped-for.
func TestSetPasswordWaitsForAnInFlightDeleteAndThenRefuses(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	svc := newRaceService(repo)
	user := newUser(ctx, t, repo)

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var blockerPID int32
	require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID))

	var locked string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT id FROM auth_user WHERE id = $1 FOR UPDATE`, user.ID).Scan(&locked))

	result := make(chan error, 1)
	go func() { result <- svc.SetPassword(ctx, user.ID, racePassword) }()

	requireBlockedRequest(ctx, t, blockerPID)

	// The delete is performed by the blocking transaction itself, so the
	// password write can only wake up into a world where the user is already
	// gone. Doing it from a third session would have to queue behind the same
	// lock and the order of the two would be the scheduler's business.
	_, err = tx.Exec(ctx,
		`UPDATE auth_user SET deleted_at = $2, updated_at = $2 WHERE id = $1`,
		user.ID, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	var setErr error
	select {
	case setErr = <-result:
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting password write did not finish in time")
	}

	// assert, not require: when this breaks, the row assertion below says WHAT
	// was left behind, and that is the half of the failure a reader needs.
	if assert.Error(t, setErr,
		"assigning a password to a user deleted in the meantime must not succeed") {
		assert.True(t, errors.IsNotFound(setErr), "kind: %s", errors.KindOf(setErr))
	}

	assert.Equal(t, int64(0), countRows(ctx, t,
		`SELECT count(*) FROM auth_identity WHERE user_id = $1 AND deleted_at IS NULL`, user.ID),
		"a deleted user must not be left with a live login identity")
}

// TestTheAddressOfADeletedAdministratorStaysUsable states the consequence the
// test above prevents, in the form an operator meets it.
//
// auth_identity_provider_uniq covers the LIVE rows of (provider,
// provider_identity) and provider_identity is the user's address. An identity
// left alive under a deleted user therefore holds that address forever: the
// user list shows it as free — every read filters deleted users out — but a new
// administrator cannot be opened at it, and there is no repair path either,
// because DeleteUser on an already deleted user returns NotFound. The row can
// only be removed by hand, with SQL, by somebody who first works out why the
// conflict is being reported at all.
func TestTheAddressOfADeletedAdministratorStaysUsable(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	svc := newRaceService(repo)
	user := newUser(ctx, t, repo)

	require.NoError(t, svc.SetPassword(ctx, user.ID, racePassword))
	require.NoError(t, svc.DeleteUser(ctx, user.ID))

	created, err := svc.CreateUser(ctx, service.CreateUserInput{
		Email:  user.Email,
		Scopes: []string{models.ScopeAdmin},
	}, racePassword)
	require.NoError(t, err, "the address of a deleted administrator has to stay usable")
	assert.Equal(t, user.Email, created.Email)
}

// TestSetPasswordWritesTheAddressOfTheRowItLocked proves that the login address
// comes from the write's own read and not from a value handed down.
//
// The two columns say the same thing — the user's login address — and the
// module keeps them in step (see queries/users.sql,
// SyncIdentityProviderIdentity). The way they used to be able to diverge was an
// address read before the write: the service read the user, an e-mail change
// landed, and the identity was created carrying the OLD address. That left the
// old address occupied in auth_identity_provider_uniq by a row nobody logs in
// with, and it was measured (2026-09-06) exactly like this.
func TestSetPasswordWritesTheAddressOfTheRowItLocked(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	svc := newRaceService(repo)
	user := newUser(ctx, t, repo)

	changed := "changed" + user.Email
	_, err := svc.UpdateUser(ctx, user.ID, service.UpdateUserInput{Email: &changed})
	require.NoError(t, err)

	require.NoError(t, svc.SetPassword(ctx, user.ID, racePassword))

	var email, providerIdentity string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT u.email, i.provider_identity
           FROM auth_user u JOIN auth_identity i ON i.user_id = u.id
          WHERE u.id = $1`, user.ID).Scan(&email, &providerIdentity))

	assert.Equal(t, changed, email)
	assert.Equal(t, email, providerIdentity,
		"the identity must carry the address of the user row the write locked")
}

// TestARevivedIdentityCannotLogIn measures the BOUNDARY of the defect above: a
// live identity under a deleted user is a consistency fault, not a way in.
//
// The distinction decides how urgent the fix is and it must be measured rather
// than reasoned about. Both roads into the admin surface read the USER first —
// [service.Service.Login] through GetUserByEmail and token verification through
// GetUser — so the claim rests on those two reads and on nothing else. Were
// either of them ever to start resolving a caller from the identity row alone,
// this test is the one that would fail.
//
// The row is manufactured with raw SQL on purpose: what is under test is the
// row, not the road that leads to it, and going through the old defect would
// make this test depend on the defect still existing.
func TestARevivedIdentityCannotLogIn(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	svc := newRaceService(repo)
	user := newUser(ctx, t, repo)

	require.NoError(t, svc.SetPassword(ctx, user.ID, racePassword))
	require.NoError(t, svc.DeleteUser(ctx, user.ID))

	_, err := testPool.Pool().Exec(ctx,
		`UPDATE auth_identity SET deleted_at = NULL, updated_at = $2 WHERE user_id = $1`,
		user.ID, time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, int64(1), countRows(ctx, t,
		`SELECT count(*) FROM auth_identity WHERE user_id = $1 AND deleted_at IS NULL`, user.ID),
		"the orphan the test needs has to exist, otherwise it measures nothing")

	_, _, loginErr := svc.Login(ctx, user.Email, racePassword)
	require.Error(t, loginErr, "a deleted user must not be able to log in")
	assert.True(t, errors.IsUnauthorized(loginErr), "kind: %s", errors.KindOf(loginErr))
}

// TestLinkWaitsForAnInFlightChannelDeleteAndThenRefuses verifies the claim
// queries/sales_channels.sql makes for LockLiveSalesChannel.
//
// That comment says the shared lock "makes the UPDATE that performs the
// deletion wait until the transaction ends", and until now nothing tested it:
// [TestKeyCannotBeLinkedToADeletedChannel] deletes the channel FIRST and then
// links, which any plain read would also refuse. The interesting case is the
// delete arriving while the link is being decided, and only a competing
// transaction can produce it.
//
// As in the password test above, the wait itself is not the proof — the link's
// own foreign key would wait for this row too. The proof is that the link is
// REFUSED afterwards: the foreign key sees a row that is physically still
// there and would have accepted it.
func TestLinkWaitsForAnInFlightChannelDeleteAndThenRefuses(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	channel := newChannel(ctx, t, repo)

	key, err := repo.CreateAPIKey(ctx, newKeyRecord(t))
	require.NoError(t, err)

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var blockerPID int32
	require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID))

	var locked string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT id FROM sales_channel WHERE id = $1 FOR UPDATE`, channel.ID).Scan(&locked))

	result := make(chan error, 1)
	go func() { result <- repo.LinkSalesChannel(ctx, key.ID, channel.ID, time.Now().UTC()) }()

	requireBlockedRequest(ctx, t, blockerPID)

	_, err = tx.Exec(ctx,
		`UPDATE sales_channel SET deleted_at = $2, updated_at = $2 WHERE id = $1`,
		channel.ID, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	var linkErr error
	select {
	case linkErr = <-result:
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting link did not finish in time")
	}

	if assert.Error(t, linkErr, "a channel deleted in the meantime must not be linked") {
		assert.True(t, errors.IsNotFound(linkErr), "kind: %s", errors.KindOf(linkErr))
	}
	assert.Equal(t, int64(0), countRows(ctx, t,
		`SELECT count(*) FROM api_key_sales_channel WHERE api_key_id = $1`, key.ID),
		"a publishable key must not be left linked to a deleted channel")
}

// TestTheFailedAttemptCounterLosesNoIncrement verifies the claim
// queries/identities.sql makes for RegisterLoginFailure.
//
// That comment says the increment is done in SQL because "were the number read
// and written back on the Go side, hundreds of requests sent at the same time
// would all read 0 and all write 1, and the lock would never engage". The
// mechanism it names is the FOR UPDATE row inside the CTE, and until now the
// claim rested on reading that word rather than on a count.
//
// The consequence of the claim being false is the one thing in this module that
// concurrency alone could take away: the login lock is what stands between a
// known administrator address and an offline-speed guessing run, and a counter
// that loses increments never reaches its threshold.
//
// The threshold is deliberately put out of reach. Whether the lock ENGAGES is a
// different claim with its own test; if it engaged here it would stop the count
// and this test would be measuring the lock instead of the counter.
func TestTheFailedAttemptCounterLosesNoIncrement(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	user := newUser(ctx, t, repo)

	now := time.Now().UTC()
	identity, err := repo.SetPasswordHash(ctx, user.ID, models.ProviderEmailPass, "hash", now)
	require.NoError(t, err)

	const attempts = 12
	const unreachableThreshold = attempts + 1

	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, failErr := repo.RegisterLoginFailure(
				ctx, identity.ID, unreachableThreshold, now.Add(time.Hour), now,
			); failErr != nil {
				t.Errorf("counting a failed attempt returned an error: %v", failErr)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(attempts), countRows(ctx, t,
		`SELECT failed_attempts FROM auth_identity WHERE id = $1`, identity.ID),
		"every concurrent attempt has to be counted; a lost increment is a lock that never engages")
	assert.Equal(t, int64(0), countRows(ctx, t,
		`SELECT count(*) FROM auth_identity WHERE id = $1 AND locked_until IS NOT NULL`, identity.ID),
		"the threshold was put out of reach on purpose")
}
