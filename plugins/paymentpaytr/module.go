package paymentpaytr

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/module"
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

// The error codes the callback's verification can refuse with. They never reach
// PayTR — the ring answers in PayTR's own protocol — but they are what the log
// line carries, and a forged callback is the one event worth being able to grep
// for by code.
const (
	// codeCallbackUnreadable is a body that is not a PayTR form.
	codeCallbackUnreadable = "paytr_callback_unreadable"
	// codeCallbackBadHash is a signature that does not match.
	codeCallbackBadHash = "paytr_callback_bad_hash"
)

// pendingGrace is how old a payment must be before it is listed as stuck.
//
// A customer typing their card details is a pending payment and is not a
// problem; one that has been pending for an hour is.
const pendingGrace = time.Hour

// ScopeRead is the scope the operator's stuck-payment list requires.
const ScopeRead = "paytr:read"

// PendingPath is the operator's stuck-payment list.
//
// It is a constant because two places name it and they had drifted: the route
// was bound from a literal while the startup warning pointed at
// CallbackPath + "/../pending", which is "/paytr/pending" — a path this plugin
// has never served. An operator who followed that line got a 404 and had no way
// to tell whether the endpoint or the payment was missing.
const PendingPath = "/admin/v1/paytr/pending"

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

// reportStuck makes the pending report once, at startup.
//
// It is the startup counterpart of `gobit stuck`: a payment left pending is
// money that may have been taken with nothing following it, and nothing else in
// the system will ever mention it. A failure to count is not a startup failure
// — refusing to boot over a diagnostic query would trade a visible problem for
// a bigger one.
//
// The report itself is [paytrModule.watchPending], which the plugin also
// registers as an hourly job (job.go). The two callers share one query and one
// message and differ only in what they do with an error, which is the whole
// difference between them: a diagnostic that fails at boot must not stop the
// boot, while a scheduled pass that fails must land in `gobit jobs` as FAILED
// rather than as a silent "ok".
func (m *paytrModule) reportStuck(ctx context.Context) {
	if err := m.watchPending(ctx); err != nil {
		m.log.WarnContext(ctx, "the pending PayTR payments could not be counted at startup",
			"error", err)
	}
}

// Routes mounts the operator's list.
//
// The CALLBACK is not here any more. It is registered with the core's inbound
// callback registry (ADR 0028), which binds the path itself — so this plugin
// cannot end up with an endpoint the guards do not know about, which is exactly
// what binding it here produced: no quota, no body limit and no replay window,
// with the signature check inside the handler as the only protection.
func (m *paytrModule) Routes(r chi.Router) {
	if m.store == nil {
		return
	}

	r.With(corehttp.RequireScope(ScopeRead)).Get(PendingPath, m.handlePending)
}

// callbackRoute is what this plugin registers with the core's callback ring.
//
// # The answers are PayTR's protocol, not gobit's
//
// PayTR reads the BODY, not the status: anything but the exact token "OK" means
// "not acknowledged" and it retries. That is why a duplicate is answered with OK
// too — a contradicting retry is a real signal, and the ring reports it at
// ERROR, but refusing it would produce an endless retry loop while every
// payment looked fine from the inside.
func (m *paytrModule) callbackRoute() corehttp.CallbackRoute {
	return corehttp.CallbackRoute{
		Source:  ProviderID,
		Path:    CallbackPath,
		Verify:  m.verifyCallback,
		Key:     callbackIdentity,
		Handler: m.handleCallback,
		Ack: corehttp.CallbackAck{
			Accepted:    corehttp.CallbackResponse{Status: http.StatusOK, Body: "OK"},
			Duplicate:   corehttp.CallbackResponse{Status: http.StatusOK, Body: "OK"},
			Rejected:    corehttp.CallbackResponse{Status: http.StatusForbidden, Body: "BAD_HASH"},
			Malformed:   corehttp.CallbackResponse{Status: http.StatusBadRequest, Body: "BAD_REQUEST"},
			Unavailable: corehttp.CallbackResponse{Status: http.StatusInternalServerError, Body: "RETRY"},
		},
	}
}

// verifyCallback is PayTR's signature check, moved out of the handler.
//
// The body arrives already buffered, so the form is parsed from the bytes the
// signature was computed over rather than from a stream something else may have
// consumed.
func (m *paytrModule) verifyCallback(_ context.Context, _ *http.Request, body []byte) error {
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return coreerrors.Invalid(codeCallbackUnreadable, "the callback body could not be parsed")
	}

	oid, received := form.Get("merchant_oid"), form.Get("hash")
	if oid == "" || received == "" {
		return coreerrors.Invalid(codeCallbackUnreadable,
			"the callback carried no order id or no signature")
	}

	expected := callbackSignature(callbackInput{
		MerchantOID: oid,
		Status:      form.Get("status"),
		TotalAmount: form.Get("total_amount"),
	}, m.cfg.MerchantKey, m.cfg.MerchantSalt)

	if !signaturesMatch(expected, received) {
		// This is the message that says a payment succeeded, so it is the one
		// worth forging. The order id is included because a genuine
		// misconfiguration and an attack look identical here, and the id is what
		// tells them apart afterwards.
		return coreerrors.Unauthorized(codeCallbackBadHash,
			"the signature of callback %s does not match", oid)
	}

	return nil
}

// callbackIdentity says which event a verified callback is, and what it asserts.
//
// Both tuples come ONLY from fields the signature covers. failed_reason_msg is
// deliberately excluded: PayTR does not sign it, so including it would let a
// resend that differs only in that field look like a different event — which is
// how a replay window is defeated while appearing to work.
//
// PayTR signs no event id, no nonce and no timestamp, so the order id is the
// whole identity: one order has one payment outcome.
func callbackIdentity(_ *http.Request, body []byte) (identity, content []string, err error) {
	form, parseErr := url.ParseQuery(string(body))
	if parseErr != nil {
		return nil, nil, coreerrors.Invalid(codeCallbackUnreadable,
			"the verified callback body could not be parsed")
	}

	oid := form.Get("merchant_oid")
	if oid == "" {
		return nil, nil, nil
	}

	return []string{oid},
		[]string{oid, form.Get("status"), form.Get("total_amount")},
		nil
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
//
// # What is NOT here any more
//
// The signature check. It ran here until ADR 0028 and it was the endpoint's
// only protection; it now runs in the core's callback ring, BEFORE the body is
// keyed and before anything derived from the payload is trusted. By the time
// this handler is entered the callback is verified, so a missing order id or an
// unreadable amount is a fault in a message PayTR really sent, not a possible
// forgery.
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
