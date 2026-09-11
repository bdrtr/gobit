package identitysession

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
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
const ModuleName = "identity_session"

// Default session shape.
//
// The TTL is thirty days because a shopper's session is not an operator's: the
// admin token lives twelve hours because a stolen one buys the whole shop
// (ADR 0031), and a stolen shopper session buys one person's order history. A
// storefront that logs people out every twelve hours is a storefront people stop
// using, and the way to shorten this is to set it.
const (
	DefaultTTL        = 30 * 24 * time.Hour
	DefaultCookieName = "gobit_session"
)

// Options are the installation's choices.
type Options struct {
	// Secret signs the session cookie; it is REQUIRED and at least 32 bytes.
	//
	// There is no default and there must not be: a generated-per-startup secret
	// would log every shopper out on each deploy, and a constant one shipped in
	// source would let anybody mint a session for any customer of every
	// installation that never changed it.
	Secret []byte
	// RetiredSecrets are keys a session cookie may still carry and that nothing
	// signs with.
	//
	// # What a rotation is
	//
	// Move the current secret here, put a new one in [Options.Secret], restart.
	// From that moment every cookie is signed with the new key and every cookie
	// already in a browser keeps working until it expires — which is what makes
	// rotating a key something other than logging every shopper out (ADR 0129).
	//
	// A retired key stays useful for as long as a session lasts, so it can be
	// dropped from this list one TTL after the rotation. Nothing here enforces
	// that: a list that grew forever would be a slowly widening set of keys that
	// can mint a session, and only the operator knows when the last cookie signed
	// with one expired.
	//
	// # A LEAKED key does not belong here
	//
	// Retiring it keeps it able to mint sessions. A key that got out is dropped
	// outright — which does log everybody out, and is the correct price.
	RetiredSecrets [][]byte
	// TTL is how long a session lasts; zero means [DefaultTTL].
	TTL time.Duration
	// CookieName is the cookie's name; empty means [DefaultCookieName].
	CookieName string
	// Insecure drops the cookie's Secure attribute.
	//
	// It exists for local development over plain HTTP and is named so that
	// switching it on reads as what it is. A production installation that sets it
	// is sending session cookies over the wire in the clear.
	Insecure bool
	// Credentials replaces this module's own table with another store.
	//
	// Nil means the table: an installation keeping passwords in a directory it
	// already runs binds that here and the migration still runs, which is the one
	// wart of this shape and is cheaper than a second module.
	Credentials Credentials
	// Logger is optional.
	Logger *slog.Logger

	// Accounts is the shop's own notion of a customer, and binding it is what
	// TURNS ON storefront self-registration.
	//
	// Nil leaves the two registration endpoints unmounted. That is the default
	// because opening an account needs a write this module may not make: see
	// [Accounts] for why it is a seam rather than a call.
	Accounts Accounts
	// Verification carries the proof to the address, and binding it is the other
	// half of turning self-registration on.
	//
	// Nil leaves the endpoints unmounted. A shop sends mail with its own client,
	// its own templates and its own sending domain, and this module chooses none
	// of those.
	Verification Verification
	// RegistrationTTL is how long a sign-up link works for.
	//
	// Zero means [DefaultRegistrationTTL].
	RegistrationTTL time.Duration
	// Limiter bounds how often the registration endpoints may be called.
	//
	// A registration endpoint sends mail to an address a stranger typed, so an
	// unlimited one is a shop that can be pointed at anybody. Nil therefore does
	// NOT mean no limit here: it means [DefaultRegistrationLimit] requests per
	// [DefaultRegistrationWindow] per client address, kept in memory.
	//
	// In memory means PER PROCESS, so an installation behind several instances
	// gets that many times the limit — and one that wants a real bound binds a
	// shared limiter (core/http/redisguard). Said here because the default is
	// the kind that looks like a limit and is a fraction of one.
	Limiter corehttp.RateLimiter
}

// Module is the gobit module this package installs.
type Module struct {
	opts     Options
	sessions *Sessions
	store    Credentials
	log      *slog.Logger
}

// That the published contract is satisfied is pinned down at compile time, and
// so is the interface this whole module exists to fill.
var (
	_ module.Module     = (*Module)(nil)
	_ corehttp.Identity = (*Sessions)(nil)
	// The schema capability is OPTIONAL and the composition root looks for it
	// with a type assertion, so a drifted method name would cost nothing at
	// compile time and three endpoints in the published document. This line
	// closes that silence — the same one gobit's own modules close for
	// themselves.
	_ openapi.Describer = (*Module)(nil)
)

// New builds the module.
//
// It validates nothing here: [Module.Register] is where a missing secret has
// somewhere to fail, and failing at construction would mean an embedder's main
// panicking before the lifecycle can say which module refused.
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
		// The directory is embedded at build time; a failure here is a broken
		// binary rather than a runtime condition.
		panic(fmt.Sprintf("identity-session: the migrations could not be opened: %v", err))
	}

	return sub
}

// Register builds the session verifier and PROVIDES it under the core's name.
//
// # Why it registers into a core-owned slot
//
// [corehttp.IdentityName] is the slot gobit's own modules resolve. Providing it
// is the whole installation: the address book, the b2b storefront and the cart
// find a verifier where they already look, and nothing in gobit had to learn
// this module's name (ADR 0043).
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	if len(m.opts.Secret) < minSecretLen {
		return fmt.Errorf(
			"identity-session: Options.Secret is %d bytes and at least %d are required; "+
				"a session cookie is signed with it, so a short or absent secret lets "+
				"anybody mint a session for any customer",
			len(m.opts.Secret), minSecretLen)
	}
	for i, retired := range m.opts.RetiredSecrets {
		// A retired key still MINTS nothing and still ACCEPTS everything it
		// signed, so a short one is the same hole as a short current one — and
		// the likeliest way in is an operator padding the list with a
		// placeholder while they work out the rotation.
		if len(retired) < minSecretLen {
			return fmt.Errorf(
				"identity-session: Options.RetiredSecrets[%d] is %d bytes and at least %d "+
					"are required; a retired key still accepts every session it signed",
				i, len(retired), minSecretLen)
		}
		if bytes.Equal(retired, m.opts.Secret) {
			return fmt.Errorf(
				"identity-session: Options.RetiredSecrets[%d] is the CURRENT secret; a "+
					"rotation moves the old key here and puts a NEW one in Options.Secret, "+
					"and listing the same key twice means the rotation did not happen",
				i)
		}
	}

	m.store = m.opts.Credentials
	if m.store == nil {
		pool, err := container.Resolve[*db.Pool](c, dbServiceName)
		if err != nil {
			return fmt.Errorf("identity-session: %q could not be resolved: %w", dbServiceName, err)
		}
		m.store = pgCredentials{pool: pool.Pool()}
	}

	ttl := m.opts.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	name := m.opts.CookieName
	if name == "" {
		name = DefaultCookieName
	}

	m.sessions = &Sessions{
		secret:     m.opts.Secret,
		retired:    m.opts.RetiredSecrets,
		ttl:        ttl,
		cookieName: name,
		secure:     !m.opts.Insecure,
		now:        time.Now,
	}

	if err := c.Provide(corehttp.IdentityName, m.sessions); err != nil {
		return fmt.Errorf("identity-session: the identity slot could not be filled: %w", err)
	}

	m.log.InfoContext(ctx, "identity-session registered",
		"slot", corehttp.IdentityName, "cookie", name, "ttl", ttl, "secure", !m.opts.Insecure)

	return nil
}

// dbServiceName is the core pool's container name.
const dbServiceName = "core.db"

// minSecretLen is the shortest signing secret this module accepts.
//
// Thirty-two bytes is the output width of the hash the MAC is built on, so a
// shorter key adds nothing an attacker has to guess past that width; it is the
// same floor gobit's own JWT secret takes in production.
const minSecretLen = 32

// Credentials returns the store, for an embedder seeding accounts from its own
// code rather than over the admin endpoint.
//
// It answers nil until [Module.Register] has run, for [Module.Sessions]'s
// reason: before that there is no pool to read.
func (m *Module) Credentials() Credentials { return m.store }

// Sessions returns the verifier, for an embedder that signs people in itself.
//
// It answers nil until [Module.Register] has run, which is the honest answer:
// before that there is no secret to sign with.
func (m *Module) Sessions() *Sessions { return m.sessions }

// Routes mounts the two storefront endpoints and the operator one.
//
// If Register did not run nothing is mounted, which is gobit's own modules'
// stance: an endpoint that exists and panics is worse than one that does not
// exist.
func (m *Module) Routes(r chi.Router) {
	if m.sessions == nil || m.store == nil {
		m.log.Warn("identity-session: Routes ran without Register, no endpoint was mounted")

		return
	}

	r.Post("/store/v1/auth/sign-in", m.signIn)
	r.Post("/store/v1/auth/sign-out", m.signOut)
	r.Put("/admin/v1/customer-credentials", m.putCredential)

	if !m.selfRegistrationMounted() {
		// Said once, at INFO rather than WARN: an installation that binds no
		// Accounts has not misconfigured anything, it has chosen to open accounts
		// its own way. What would be a fault is mounting an endpoint that takes a
		// password and can never finish.
		m.log.Info("identity-session: self-registration is not mounted",
			"accounts_bound", !isNil(m.opts.Accounts),
			"verification_bound", !isNil(m.opts.Verification),
			"store_holds_registrations", m.storeHoldsRegistrations())

		return
	}

	// The limit wraps only these two. Signing in is already bounded by not
	// knowing the password, and gobit's own guard stack limits the whole API;
	// what is different here is that ONE request makes this shop send mail to an
	// address a stranger chose.
	limited := r.With(corehttp.RateLimit(m.registrationLimiter(), corehttp.ClientIPKey))
	limited.Post("/store/v1/auth/register", m.register)
	limited.Post("/store/v1/auth/register/verify", m.verifyRegistration)
}

// storeHoldsRegistrations says whether the bound store can keep a pending row.
func (m *Module) storeHoldsRegistrations() bool {
	if m.store == nil {
		return false
	}
	_, ok := m.store.(Registrations)

	return ok
}

// The default bound on the registration endpoints.
//
// Five in ten minutes per client address. It is generous for a person — who asks
// once, then maybe once more when the message is slow — and it costs an abuser
// an address of their own per five messages.
const (
	// DefaultRegistrationLimit is how many registration requests one client
	// address may make per window.
	DefaultRegistrationLimit = 5
	// DefaultRegistrationWindow is that window.
	DefaultRegistrationWindow = 10 * time.Minute
)

// registrationLimiter answers the bound these endpoints run behind.
//
// The default is built HERE and kept by the middleware, because [Module.Routes]
// is the only caller and runs once at startup. An earlier version memoised it in
// a field; a mutation showed the memo could not matter — a field nothing reads
// twice is a field that says the wrong thing about when it is written. What WOULD
// matter is building one per request, and the shape that could do that is a call
// from a handler, which there is none of.
func (m *Module) registrationLimiter() corehttp.RateLimiter {
	if m.opts.Limiter != nil {
		return m.opts.Limiter
	}

	return corehttp.NewMemoryLimiter(DefaultRegistrationLimit, DefaultRegistrationWindow)
}

// signInRequest is the body of POST /store/v1/auth/sign-in.
type signInRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// credentialRequest is the body of PUT /admin/v1/customer-credentials.
type credentialRequest struct {
	CustomerID string `json:"customer_id"`
	Email      string `json:"email"`
	Password   string `json:"password"`
}

// signIn checks the password and issues the cookie.
//
// # Why the answer carries no body
//
// What the caller needs is the cookie, and anything else this could return —
// the customer's id, their e-mail — is something the storefront can read from
// the routes that now work. A sign-in that echoed the identifier would put it
// in every browser history that logged a URL.
func (m *Module) signIn(w http.ResponseWriter, r *http.Request) {
	var body signInRequest
	if !decode(w, r, &body) {
		return
	}

	customerID, hash, err := m.store.Credential(r.Context(), body.Email)
	if err == nil {
		err = VerifyPassword(hash, body.Password)
	}
	if err != nil {
		// One answer for an unknown address, a wrong password and a corrupt
		// stored hash. Telling them apart is the oracle this module refuses to be.
		if errors.Is(err, ErrPasswordMismatch) {
			corehttp.WriteError(r.Context(), w, coreerrors.Unauthorized(CodeRejected,
				"the e-mail address and the password do not match"))

			return
		}
		m.log.ErrorContext(r.Context(), "identity-session: a sign-in could not be answered",
			"error", err)
		corehttp.WriteError(r.Context(), w, coreerrors.Internal(CodeUnavailable,
			"the sign-in could not be answered"))

		return
	}

	m.sessions.Issue(w, customerID)
	corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)
}

// signOut clears the cookie.
//
// It answers 204 whether a session was there or not: a caller signing out twice
// has got what they asked for both times, and reporting "you were not signed in"
// tells an unauthenticated caller something about the cookie they sent.
func (m *Module) signOut(w http.ResponseWriter, r *http.Request) {
	m.sessions.Clear(w)
	corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)
}

// putCredential writes a customer's credential.
//
// It is on the ADMIN prefix, so it is behind gobit's own operator
// authentication, and that is what the endpoint IS rather than where it ended up:
// it writes a credential for any customer the caller names, which is an
// operator's power.
//
// Storefront self-registration is [Module.register] and [Module.verifyRegistration]
// — a different pair, mounted only when the installation binds [Accounts] and
// [Verification] (ADR 0133). The difference between them is whose word is taken
// for who somebody is: here an operator's, there a proven address.
func (m *Module) putCredential(w http.ResponseWriter, r *http.Request) {
	var body credentialRequest
	if !decode(w, r, &body) {
		return
	}
	if body.CustomerID == "" || body.Email == "" {
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodeInvalid,
			"customer_id and email are required"))

		return
	}

	hash, err := HashPassword(body.Password)
	if err != nil {
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodeInvalid, "%s", err.Error()))

		return
	}
	if err := m.store.Put(r.Context(), body.CustomerID, body.Email, hash); err != nil {
		m.log.ErrorContext(r.Context(), "identity-session: a credential could not be written",
			"error", err)
		corehttp.WriteError(r.Context(), w, coreerrors.Conflict(CodeNotWritten,
			"the credential could not be written; the e-mail may belong to another customer"))

		return
	}

	corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)
}
