package paymentpaytr

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/module"
)

// dbServiceName is the core pool's name in the container.
const dbServiceName = "core.db"

// CallbackPath is where PayTR posts the result of a payment.
//
// # Why it is not under /store/v1 or /admin/v1
//
// PayTR is neither a shopper nor an operator. It carries no publishable key and
// no bearer token, so a path under either prefix would be refused by the guard
// stack before the handler ever ran — and the symptom would be PayTR retrying
// forever while every payment stayed pending.
//
// Its authentication is the SIGNATURE on the body, which is stronger than a
// shared bearer token would be: it covers the amount and the outcome, not just
// the caller's identity.
const CallbackPath = "/paytr/callback"

// pendingGrace is how old a payment must be before it is listed as stuck.
//
// A customer typing their card details is a pending payment and is not a
// problem; one that has been pending for an hour is.
const pendingGrace = time.Hour

// ScopeRead is the scope the operator's stuck-payment list requires.
const ScopeRead = "paytr:read"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with the "migrations/" prefix stripped.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// paytrModule is the module the plugin brings.
type paytrModule struct {
	cfg   config
	log   *slog.Logger
	store *store
	prov  *provider
}

// The core's module contract is satisfied at compile time.
var _ module.Module = (*paytrModule)(nil)

// newModule builds the module. Dependencies are resolved in Register.
func newModule(cfg config, log *slog.Logger) *paytrModule {
	if log == nil {
		log = slogDiscard()
	}

	m := &paytrModule{cfg: cfg, log: log}
	// The provider is built now and given its store in Register. It is handed
	// to the plugin host at Setup, before any module is up, so it cannot be
	// constructed later.
	m.prov = &provider{cfg: cfg, client: &http.Client{}, log: log}

	return m
}

// provider returns the payment provider this module backs.
func (m *paytrModule) provider() *provider { return m.prov }

// Name returns the module's unique name.
func (m *paytrModule) Name() string { return ModuleName }

// Migrations returns the module's migration files.
func (m *paytrModule) Migrations() fs.FS { return migrationsRoot }

// Register resolves the pool and reports what is stuck.
func (m *paytrModule) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, dbServiceName)
	}

	m.store = newStore(pool.Pool())
	m.prov.store = m.store

	m.reportStuck(ctx)

	return nil
}

// reportStuck logs how many payments PayTR never reported on.
//
// It is the startup counterpart of `gobit stuck`: a payment left pending is
// money that may have been taken with nothing following it, and nothing else in
// the system will ever mention it. A failure to count is not a startup failure
// — refusing to boot over a diagnostic query would trade a visible problem for
// a bigger one.
func (m *paytrModule) reportStuck(ctx context.Context) {
	stuck, err := m.store.pending(ctx, pendingGrace, 100)
	if err != nil {
		m.log.WarnContext(ctx, "the pending PayTR payments could not be counted at startup",
			"error", err)

		return
	}
	if len(stuck) == 0 {
		return
	}

	m.log.WarnContext(ctx, "PayTR payments are still pending; PayTR never reported on them "+
		"and any money taken has no order behind it",
		"pending", len(stuck),
		"older_than", pendingGrace.String(),
		"list", CallbackPath+"/../pending")
}

// Routes mounts the callback and the operator's list.
func (m *paytrModule) Routes(r chi.Router) {
	if m.store == nil {
		return
	}

	r.Post(CallbackPath, m.handleCallback)
	r.With(corehttp.RequireScope(ScopeRead)).Get("/admin/v1/paytr/pending", m.handlePending)
}

// handleCallback records what PayTR reported.
//
// # The answer body must be exactly "OK"
//
// PayTR reads the body, not the status code. Anything else means "not
// acknowledged" and PayTR retries — so a handler that returns a JSON envelope
// on success produces an endless retry loop while every payment looks fine from
// the inside.
//
// That is also why the failure paths answer with a short token rather than the
// core's error envelope: this endpoint's protocol is PayTR's, not gobit's.
func (m *paytrModule) handleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		m.log.WarnContext(ctx, "a PayTR callback could not be parsed", "error", err)
		answer(w, http.StatusBadRequest, "BAD_REQUEST")

		return
	}

	oid := r.PostForm.Get("merchant_oid")
	status := r.PostForm.Get("status")
	totalAmount := r.PostForm.Get("total_amount")
	received := r.PostForm.Get("hash")

	if oid == "" || received == "" {
		m.log.WarnContext(ctx, "a PayTR callback arrived without an order id or a signature")
		answer(w, http.StatusBadRequest, "BAD_REQUEST")

		return
	}

	expected := callbackSignature(
		callbackInput{MerchantOID: oid, Status: status, TotalAmount: totalAmount},
		m.cfg.MerchantKey, m.cfg.MerchantSalt)

	if !signaturesMatch(expected, received) {
		// This is the message that tells the system a payment succeeded, so it
		// is the one worth forging. A mismatch is logged at ERROR and the order
		// id is included: a genuine configuration fault and an attack look
		// identical here, and the id is what tells them apart afterwards.
		m.log.ErrorContext(ctx, "a PayTR callback failed signature verification",
			"merchant_oid", oid)
		answer(w, http.StatusForbidden, "BAD_HASH")

		return
	}

	paid, err := strconv.ParseInt(totalAmount, 10, 64)
	if err != nil {
		m.log.ErrorContext(ctx, "a verified PayTR callback carried an unreadable amount",
			"merchant_oid", oid, "total_amount", totalAmount)
		answer(w, http.StatusBadRequest, "BAD_AMOUNT")

		return
	}

	outcome := statusFailed
	if status == payTRSuccess {
		outcome = statusSuccess
	}

	applied, err := m.store.recordCallback(ctx, oid, outcome, paid,
		r.PostForm.Get("failed_reason_msg"))
	if err != nil {
		// The write failed, so PayTR must NOT be acknowledged: a retry is
		// exactly what is wanted here, and it is the only thing that will save
		// this payment.
		m.log.ErrorContext(ctx, "a PayTR callback could not be recorded; PayTR will retry",
			"merchant_oid", oid, "error", err)
		answer(w, http.StatusInternalServerError, "RETRY")

		return
	}

	if !applied {
		// Either the payment was already reported — PayTR retries a callback it
		// believes was not acknowledged, so this is routine — or the order id
		// is unknown. Both are acknowledged: retrying would not change either.
		m.log.InfoContext(ctx, "a PayTR callback changed nothing; it was already recorded or the id is unknown",
			"merchant_oid", oid, "status", outcome)
		answer(w, http.StatusOK, "OK")

		return
	}

	m.log.InfoContext(ctx, "a PayTR payment was reported",
		"merchant_oid", oid, "status", outcome, "amount", paid)

	answer(w, http.StatusOK, "OK")
}

// handlePending lists payments PayTR never reported on.
//
// This is the operator's view of the one gap this plugin cannot close by
// itself: a customer who paid and closed the browser. The rows here are where
// money may have moved with nothing following it.
func (m *paytrModule) handlePending(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	stuck, err := m.store.pending(ctx, pendingGrace, 200)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	items := make([]map[string]any, 0, len(stuck))
	for _, p := range stuck {
		items = append(items, map[string]any{
			"merchant_oid":  p.MerchantOID,
			"amount":        p.Amount,
			"currency_code": p.CurrencyCode,
			"status":        p.Status,
		})
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, map[string]any{"pending": items})
}

// answer writes PayTR's protocol: a bare token, no JSON envelope.
func answer(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// mustSub opens the subdirectory; it panics if it cannot be opened.
//
// The panic is safe: the directory name is constant at compile time and the
// embed directive has already verified the files exist. Returning nil silently
// would mean the module coming up without its table.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("paytr: could not open the embedded migrations directory: " + err.Error())
	}

	return sub
}
