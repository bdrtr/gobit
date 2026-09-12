package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/modules/auth"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
)

// mfaResetCommand takes an administrator's second factor off their account.
//
// # Why this is a command and not an endpoint
//
// Since ADR 0147 a login DEMANDS the factor, so the person who lost their phone
// cannot sign in to fix it themselves — and no endpoint lets one administrator
// remove another's, deliberately: an attacker holding one stolen admin session
// could then reach every other account with a password alone, which is the exact
// thing the factor exists to prevent.
//
// That leaves one honest place for the reset, and it is this one. Running it needs
// shell access to the installation, which is a privilege an attacker with a stolen
// session does not have and an operator answering a colleague's telephone call
// already does — and it is a step an installation can audit separately from the
// admin surface.
//
// # Why it needs -confirm, unlike refold-invoices
//
// It is not reversible in the sense that matters: the secret is gone, the person
// has to enroll again, and between the reset and their next enrollment their account
// is protected by a password alone. Repeating the address is the difference between
// "I meant that account" and a name completed by a shell history.
const mfaResetCommand = "mfa-reset"

// codeMFAResetFailed reports that the reset could not be carried out.
const codeMFAResetFailed = "app_mfa_reset_failed"

// factorRemover is the auth service as this command needs it.
//
// Two methods, declared here rather than taken as a whole service, for the reason
// [invoiceRefolder] gives: a root that resolved whole services would make every
// command look like it could do anything the module can, and this one may do
// exactly one thing.
type factorRemover interface {
	// GetUserByEmail finds the account the operator named.
	GetUserByEmail(ctx context.Context, email string) (authmodels.User, error)
	// RemoveMFA drops the account's second factor and reports whether there was
	// one to drop.
	RemoveMFA(ctx context.Context, userID string) (bool, error)
}

// runMFAReset brings the application up and removes the factor.
func runMFAReset(args []string, out io.Writer, opts Options) error {
	email, err := parseMFAResetFlags(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		// The flag set has already printed the usage; asking what a command does
		// is not a failure.
		return nil
	case err != nil:
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Log lines go to stderr so stdout carries only what the operator reads back;
	// the same split every other command in this binary makes.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	app, closeApp, err := openApplication(ctx, cfg, log, errorreport.NewSink(), opts, publishesOnly)
	if err != nil {
		return err
	}
	defer closeApp()

	return resetSecondFactor(ctx, app.container, out, email)
}

// resetSecondFactor resolves the service and removes the factor.
//
// Split from [runMFAReset] so the decision can be exercised without a process,
// which is the split [recoverExecution] and [refoldInvoices] make.
//
// The two outcomes are REPORTED APART. "There was none" is not a failure — the
// account ends up in the state the operator asked for — but it is the answer that
// says the telephone call was about something else, and an operator told only
// "done" would go on believing the phone was the problem.
func resetSecondFactor(
	ctx context.Context, c *container.Container, out io.Writer, email string,
) error {
	svc, err := container.Resolve[factorRemover](c, auth.ServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeMFAResetFailed,
			"the identity service %q could not be resolved; this command needs the auth module installed",
			auth.ServiceName)
	}

	user, err := svc.GetUserByEmail(ctx, email)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeMFAResetFailed,
			"no account could be read for %q", email)
	}

	removed, err := svc.RemoveMFA(ctx, user.ID)
	if err != nil {
		return err
	}

	if !removed {
		return writeReport(out, fmt.Sprintf(
			"%s (%s) held no second factor; nothing was changed\n", email, user.ID))
	}

	return writeReport(out, fmt.Sprintf(
		"%s (%s) no longer holds a second factor; they sign in with their password and "+
			"can enroll a new authenticator\n", email, user.ID))
}

// parseMFAResetFlags reads the address and the confirmation.
func parseMFAResetFlags(args []string) (string, error) {
	// The address is required to be the FIRST argument, exactly as the execution
	// id is for `recover`: the flag package stops at the first non-flag argument,
	// so an address written after the flags would be swallowed as a leftover.
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", errors.Invalid(codeMFAResetFailed,
			"%s needs the account's email address as its FIRST argument "+
				"(gobit %s <email> -%s <email>)",
			mfaResetCommand, mfaResetCommand, flagConfirm)
	}

	flags := flag.NewFlagSet("gobit "+mfaResetCommand, flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	confirm := flags.String(flagConfirm, "",
		"repeat the email address; nothing is removed without it")

	if err := flags.Parse(args[1:]); err != nil {
		return "", errors.Wrap(err, errors.KindInvalid, codeMFAResetFailed,
			"the flags of %s could not be parsed", mfaResetCommand)
	}
	if rest := flags.Args(); len(rest) > 0 {
		return "", errors.Invalid(codeMFAResetFailed,
			"unexpected argument %q after the flags of %s", rest[0], mfaResetCommand)
	}

	email := args[0]
	if *confirm != email {
		return "", errors.Invalid(codeMFAResetFailed,
			"the account will be left with a password alone until they enroll again: "+
				"run it again with -%s %s to confirm", flagConfirm, email)
	}

	return email, nil
}
