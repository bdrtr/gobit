// Package identitypasskey adds passkeys to gobit's session identity.
//
// # What it is
//
// A gobit module an embedder adds beside contrib/identity-session:
//
//	sessions := identitysession.New(identitysession.Options{Secret: secret})
//	shop := gobit.New().
//		Add(sessions).
//		Add(identitypasskey.New(identitypasskey.Options{
//			Session:     sessions,
//			RPID:        "example.test",
//			RPOrigins:   []string{"https://example.test"},
//			DisplayName: "Example Shop",
//		}))
//
// It brings four storefront endpoints — register begin/finish and sign-in
// begin/finish — a table of its own, and nothing else. Signing in with a passkey
// issues the SAME session cookie a password does, because a session is a session
// however the person proved they own it.
//
// # Why it is a module of its own rather than part of identity-session
//
// Measured before it was written: importing go-webauthn adds nine modules that
// gobit's graph does not already carry, go-tpm and go-tpm-tools among them. An
// installation that wanted a cookie and a password should not pay for
// attestation formats it will never see, and a separate go.mod is the only thing
// that keeps it from paying (ADR 0128).
//
// # What it does NOT do
//
// It does not let a person register their FIRST credential without already being
// signed in. A passkey is added to an account, and deciding who may create an
// account is the question identity-session left alone for the same reason.
//
// It removes nothing. Listing and deleting a person's keys is a real need and a
// separate one: a delete endpoint that can remove a person's last key turns a
// convenience into a lockout, and the rule for that is not written yet.
package identitypasskey

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/webauthn"

	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/openapi"
)

// migrationsRoot holds this module's schema.
//
//go:embed migrations/*.sql
var migrationsRoot embed.FS

// ModuleName prefixes this module's container services and names its migration
// version table.
const ModuleName = "identity_passkey"

// CeremonyTTL is how long a begun ceremony stays valid.
//
// Two minutes is a person picking up their phone and touching a sensor, with
// room for them to look for it. Long enough is the whole requirement: the
// challenge is single-use by construction, so the window is about a person's
// patience rather than an attacker's budget.
const CeremonyTTL = 2 * time.Minute

// Options are the installation's choices.
type Options struct {
	// Session is the session module this one signs people into; REQUIRED.
	//
	// It is the module and not its *Sessions because [identitysession.Module.Sessions]
	// answers nil until that module has registered, and the composition root
	// registers in the order an embedder added. Taking the module lets this one
	// ask at ITS registration, by which time the answer exists — and say so when
	// it does not.
	Session *identitysession.Module
	// RPID is the relying party id: the registrable domain the passkey belongs
	// to, without a scheme or a port. REQUIRED.
	//
	// There is no default and there must not be. A credential is bound to this
	// value forever; getting it wrong does not fail loudly, it produces keys
	// that cannot be used from the site that created them.
	RPID string
	// RPOrigins are the origins a ceremony may come from, scheme and port
	// included. REQUIRED.
	RPOrigins []string
	// DisplayName is what a person's authenticator shows in its picker.
	DisplayName string
	// Credentials replaces this module's own table with another store.
	Credentials Credentials
	// OtherSignIn states whether an account has a way in that is not a passkey.
	//
	// It decides one thing: whether a person's LAST passkey may be removed. Nil
	// means [PasswordSignIn] over [Options.Session] — the ordinary arrangement,
	// where a password in the session module is a way in — and [Module.Register]
	// REFUSES that default when the identity this installation bound is not that
	// session module's, because in such an installation a password there is a row
	// nothing reads and the default would be confidently wrong (ADR 0130).
	//
	// A shop with no passwords wires [NoOtherSignIn], and the consequence is the
	// honest one: the last passkey is never removable.
	OtherSignIn OtherSignIn
	// Logger is optional.
	Logger *slog.Logger
}

// Module is the gobit module this package installs.
type Module struct {
	opts        Options
	web         *webauthn.WebAuthn
	sessions    *identitysession.Sessions
	store       Credentials
	identity    corehttp.Identity
	otherSignIn OtherSignIn
	// displayName is what a person's authenticator shows; it falls back to the
	// relying party id, which is at least true.
	displayName string
	log         *slog.Logger
}

// The contracts are pinned at compile time; the describer is OPTIONAL and the
// composition root finds it with a type assertion, so a drifted method name
// would cost four endpoints in the published document and nothing in the build.
var (
	_ module.Module     = (*Module)(nil)
	_ openapi.Describer = (*Module)(nil)
)

// New builds the module.
func New(opts Options) *Module {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Module{opts: opts, log: log}
}

// Name is the module's unique name.
func (m *Module) Name() string { return ModuleName }

// Migrations returns this module's schema.
func (m *Module) Migrations() fs.FS {
	sub, err := fs.Sub(migrationsRoot, "migrations")
	if err != nil {
		panic(fmt.Sprintf("identity-passkey: the migrations could not be opened: %v", err))
	}

	return sub
}

// dbServiceName is the core pool's container name.
const dbServiceName = "core.db"

// Register builds the ceremony engine and finds what it signs people into.
//
// # Why it does not fill the identity slot
//
// A passkey PROVES a person at a ceremony and the proof is then carried by the
// session cookie the other module issues. Two modules filling
// corehttp.IdentityName would be two answers to "who is this request", and the
// second Provide would fail anyway — which is the container saying the same
// thing.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	if m.opts.Session == nil {
		return errors.New(
			"identity-passkey: Options.Session is required; this module signs people " +
				"into contrib/identity-session's cookie and has no session of its own")
	}
	if m.sessions = m.opts.Session.Sessions(); m.sessions == nil {
		return errors.New(
			"identity-passkey: the session module has not registered yet; add it to the " +
				"application BEFORE this one, because the composition root registers in " +
				"the order modules were added")
	}
	if m.opts.RPID == "" || len(m.opts.RPOrigins) == 0 {
		return fmt.Errorf(
			"identity-passkey: RPID and RPOrigins are required (RPID %q, %d origins); a "+
				"credential is bound to them forever and a wrong value does not fail "+
				"loudly, it mints keys the site that made them cannot use",
			m.opts.RPID, len(m.opts.RPOrigins))
	}

	displayName := m.opts.DisplayName
	if displayName == "" {
		displayName = m.opts.RPID
	}

	web, err := webauthn.New(&webauthn.Config{
		RPID:          m.opts.RPID,
		RPDisplayName: displayName,
		RPOrigins:     m.opts.RPOrigins,
	})
	if err != nil {
		return fmt.Errorf("identity-passkey: the ceremony engine could not be built: %w", err)
	}
	m.web = web
	m.displayName = displayName

	m.store = m.opts.Credentials
	if m.store == nil {
		pool, resolveErr := container.Resolve[*db.Pool](c, dbServiceName)
		if resolveErr != nil {
			return fmt.Errorf("identity-passkey: %q could not be resolved: %w",
				dbServiceName, resolveErr)
		}
		m.store = pgCredentials{pool: pool.Pool()}
	}

	// The verifier this installation bound, which is how a REGISTRATION knows
	// whose account the new key is being added to. It is resolved rather than
	// taken from Options because the slot is the core's and an installation may
	// have bound something else in front of the session module.
	identity, err := container.Resolve[corehttp.Identity](c, corehttp.IdentityName)
	if err != nil {
		return fmt.Errorf(
			"identity-passkey: %q could not be resolved: %w; registering a passkey adds "+
				"it to an account, so this module needs to know whose",
			corehttp.IdentityName, err)
	}
	m.identity = identity

	m.otherSignIn = m.opts.OtherSignIn
	if m.otherSignIn == nil {
		// The default is only correct when the verifier this installation bound
		// IS the session module's, and that is checkable here because the slot
		// was just resolved. Where it is not, refusing beats defaulting: a WARN
		// at startup is not a choice (ADR 0125), and the wrong answer removes
		// somebody's last way in.
		if identity != any(m.sessions) {
			return errors.New(
				"identity-passkey: Options.OtherSignIn is required when the bound " +
					corehttp.IdentityName + " is not this session module's verifier; a " +
					"password in that module is not a way in for an installation that " +
					"proves its customers some other way. Wire identitypasskey." +
					"PasswordSignIn(session) if it is, or NoOtherSignIn() if a passkey " +
					"is the only way in")
		}
		m.otherSignIn = PasswordSignIn(m.opts.Session)
	}

	m.log.InfoContext(ctx, "identity-passkey registered",
		"rp_id", m.opts.RPID, "origins", len(m.opts.RPOrigins))

	return nil
}

// Store returns the credential store, for an embedder listing or removing a
// person's keys from its own code.
//
// This module ships neither: a delete endpoint that can remove somebody's LAST
// key turns a convenience into a lockout, and the rule for that is not written
// here. What it can do is not stand in the way of an embedder who has decided.
//
// It answers nil until [Module.Register] has run.
func (m *Module) Store() Credentials { return m.store }

// Routes mounts the four ceremony endpoints.
func (m *Module) Routes(r chi.Router) {
	if m.web == nil || m.store == nil || m.sessions == nil || m.otherSignIn == nil {
		m.log.Warn("identity-passkey: Routes ran without Register, no endpoint was mounted")

		return
	}

	r.Post("/store/v1/auth/passkey/register/begin", m.beginRegistration)
	r.Post("/store/v1/auth/passkey/register/finish", m.finishRegistration)
	r.Post("/store/v1/auth/passkey/sign-in/begin", m.beginSignIn)
	r.Post("/store/v1/auth/passkey/sign-in/finish", m.finishSignIn)
	r.Get("/store/v1/auth/passkey/keys", m.listKeys)
	r.Delete("/store/v1/auth/passkey/keys/{credential_id}", m.removeKey)
}
