// First-administrator seeding: creating the one account that makes a fresh
// installation manageable, and refusing to open a shared installation where
// none can be created. It is its own file because it is the only startup step
// in this package that WRITES to the database, and because what it declines to
// do — touch an existing installation's administrator — matters as much as
// what it does. newAuditID is here as well, minting the identifiers of the
// other rows this package's wiring causes to be written: the audit log's.

package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/config"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
	authservice "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// codeBootstrapFailed reports that seeding the first administrator failed.
const codeBootstrapFailed = "admin_bootstrap_failed"

// codeAdminBootstrapRequired reports that a fresh installation is not
// manageable: there are no users and no seed has been configured.
const codeAdminBootstrapRequired = "admin_bootstrap_required"

// adminUsers is the NARROW surface the seeding step wants from the auth module.
//
// A two-method interface is used instead of the concrete *service.Service: the
// setup depends not on the whole of auth but only on the two calls listed here,
// and the seeding logic can be exercised with a fake implementation and no
// database. The service is resolved from the container BY NAME
// (auth.ServiceName).
//
// The signatures USE the input/output types from auth's service package and
// that is allowed: what is forbidden is the core knowing the modules (Principle
// 2.4) or the modules knowing each other; cmd/server is the composition root
// and already imports every module.
type adminUsers interface {
	ListUsers(ctx context.Context, in authservice.ListUsersInput) (authservice.Page[authmodels.User], error)
	CreateUser(ctx context.Context, in authservice.CreateUserInput, password string) (authmodels.User, error)
}

// seedAdmin creates the first admin user when there are no users at all.
//
// A server opened against an empty database has no administrator, and because
// the admin endpoints are protected there is no way to create the first one
// over HTTP either; without this step a fresh installation is unusable.
//
// # Only on an empty installation
//
// The step runs while the user count is ZERO, and is otherwise skipped with an
// info log. This is not merely "do not create it twice": the seed NEVER touches
// an existing installation's password or privileges. Had it done so, an
// ADMIN_BOOTSTRAP_PASSWORD forgotten in an env file would silently roll back
// the production administrator's password on every restart and restarting would
// stop being safe.
//
// # What is logged
//
// The password is not logged. NEITHER IS THE EMAIL: the auth module deliberately
// writes only the id when creating a user (see internal/modules/auth/service
// user.go) and it would make no sense for the setup to pierce that decision
// here — the log collector is open to a far wider audience than the admin
// surface, and the user id is enough to answer "which account was created".
//
// The error path is separate: there startup STOPS anyway, and the operator
// seeing the rejected value is necessary for diagnosis, because that is exactly
// what has to be fixed.
func seedAdmin(
	ctx context.Context,
	users adminUsers,
	cfg config.Config,
	log *slog.Logger,
) error {
	// The user count is read IN EVERY CASE. It answers two different questions:
	// if the seed is configured, "should I create it a second time"; if it is
	// not, "is this installation manageable at all". The second used never to
	// be asked, and an installation whose answer was "no" opened silently.
	//
	// The page size is 1: the only fact needed here is "are there any users",
	// not the list itself. Page.Count gives the TOTAL matching the filter, not
	// the number of records on the page.
	page, err := users.ListUsers(ctx, authservice.ListUsersInput{Limit: 1})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeBootstrapFailed,
			"the user count could not be read for the first-administrator seed")
	}

	// config.Validate forces the two to be given TOGETHER; only the "neither
	// given" state falls through to here. Both fields are checked anyway: a
	// hand-built Config that never went through validation must not be able to
	// run the seed with half an entry.
	if cfg.AdminBootstrapEmail == "" || cfg.AdminBootstrapPassword == "" {
		return reportUnmanageableInstallation(ctx, cfg, page.Count, log)
	}

	if page.Count > 0 {
		log.InfoContext(ctx, "the first-administrator seed was skipped: the installation has users",
			slog.Int64("user_count", page.Count))

		return nil
	}

	// The scope list is DELIBERATELY not given: in the auth module a nil slice
	// means "full privileges", and the first administrator must be fully
	// privileged — there is nobody else to grant them anything. Had an empty
	// slice been passed, an account unable to reach any admin endpoint would be
	// born and the system would still be unusable.
	user, err := users.CreateUser(ctx, authservice.CreateUserInput{
		Email: cfg.AdminBootstrapEmail,
	}, cfg.AdminBootstrapPassword)

	switch {
	// A conflict is not a FAILURE, it is a RACE. When several instances open
	// against an empty database at the same time they all see "no users" and
	// they all try to create one; email uniqueness rejects all but one.
	//
	// Treating that as an error and stopping startup would mean two of three
	// replicas entering a restart loop on the first deployment — a deployment
	// that repairs itself but looks broken. The desired end state ("there is an
	// administrator") holds for the losing instances too; the one right thing
	// to do is to carry on.
	//
	// Only a CONFLICT is swallowed: a connection error or an invalid password
	// still stops startup, because for those the desired end state does not
	// hold.
	case errors.IsConflict(err):
		log.InfoContext(ctx, "the first-administrator seed was skipped: another instance created it at the same time")

		return nil
	case err != nil:
		return errors.Wrap(err, errors.KindOf(err), codeBootstrapFailed,
			"the first administrator could not be created")
	}

	log.InfoContext(ctx, "the first administrator was created", slog.String("user_id", user.ID))

	return nil
}

// reportUnmanageableInstallation checks that the installation is manageable
// when no seed has been configured.
//
// # Which failure
//
// A fresh database plus an empty ADMIN_BOOTSTRAP_* pair PASSES config.Validate,
// because leaving both empty is a legitimate choice for an INSTALLED system
// (see [config.Config.AdminBootstrapEmail]) and validation cannot see the
// "installed?" question. If the database is empty too, the result is an
// unmanageable installation: there are no users, /admin/v1 is fully protected
// except for the login endpoint, and there is NO WAY to create the first user
// over HTTP. The storefront surface is closed as well, because the publishable
// key is also minted by an admin endpoint.
//
// The server still opens without a hitch: /health and /ready return green,
// every route is mounted, no log line says anything is missing. The failure
// shows on the first sign-in attempt.
//
// # Why it STOPS in a shared environment
//
// There is NO ambiguity here, and that is the criterion:
// [config.Config.LocalFileRootIsDurable] settles for a warning because it is
// not certain the configuration is wrong; that an installation with zero users
// is unmanageable IS certain. The same certainty stops startup in main.go when
// the authenticator cannot be resolved, and the rationale is identical:
// carrying on with a surface that looks protected but can accept no admin
// request hides the failure until the first sign-in attempt — often days after
// the installation. At that point the way to fix it is not configuration but
// hand-written SQL against the production database.
//
// # Why it does NOT stop in development
//
// The repository's promise is "make up && make run works even without a .env",
// and a developer opening for the first time against a fresh database lands
// exactly in this state. There the cost is next to nothing: the person reading
// the warning is sitting at the terminal that printed it and can reopen within
// seconds using two environment variables. The distinction is the same as
// JWT_SECRET's — a warning in development, a refusal in a shared environment.
func reportUnmanageableInstallation(
	ctx context.Context,
	cfg config.Config,
	userCount int64,
	log *slog.Logger,
) error {
	if userCount > 0 {
		return nil
	}

	if cfg.IsShared() {
		return errors.Invalid(codeAdminBootstrapRequired,
			"the installation has no users and ADMIN_BOOTSTRAP_EMAIL/ADMIN_BOOTSTRAP_PASSWORD were "+
				"not given: because the admin surface is fully protected apart from the login "+
				"endpoint, there is no way to create the first administrator over HTTP (APP_ENV=%s)",
			cfg.AppEnv)
	}

	log.WarnContext(ctx, "the installation has no users",
		"warning", "the admin surface is fully protected apart from the login endpoint and there is "+
			"no way to create the first administrator over HTTP; the storefront surface is closed "+
			"too, because the publishable key is minted by an admin endpoint",
		"remedy", "provide ADMIN_BOOTSTRAP_EMAIL and ADMIN_BOOTSTRAP_PASSWORD and restart")

	return nil
}

// newAuditID produces an audit row's identifier.
//
// It is a plain random id rather than something derived from the request: two
// writes on the same path by the same actor in the same second are two
// different facts, and a derived id would collapse them.
func newAuditID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand failing is a broken machine rather than a condition to
		// handle; an id that repeats would silently drop audit rows on the
		// primary key, which is the one outcome this table must not have.
		panic("audit id could not be generated: " + err.Error())
	}

	return "audit_" + hex.EncodeToString(raw[:])
}
