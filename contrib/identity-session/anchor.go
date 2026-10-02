package identitysession

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// SessionAnchors is the OPTIONAL capability a store offers to end a customer's
// sessions (ADR 0374): it keeps the moment their sessions count from, and a
// session cookie issued before it proves nobody.
//
// It is not a method of [Credentials], which is published, for
// [PasswordReset]'s reason. A store that does not offer it keeps the shape this
// module had before: a session works until it expires, and nothing ends it
// sooner but the signing key.
type SessionAnchors interface {
	// SessionsValidFrom answers the moment the customer's sessions count from,
	// or the zero time when every session of theirs counts — a customer with no
	// credential here included.
	SessionsValidFrom(ctx context.Context, customerID string) (time.Time, error)
	// EndSessionsBefore makes every session of the customer issued before the
	// moment prove nobody. It never moves the moment back, so two requests
	// racing leave the later one, and it answers [ErrNoCredential] for a
	// customer with no credential here.
	EndSessionsBefore(ctx context.Context, customerID string, moment time.Time) error
}

// ErrNoCredential is a customer this store holds no credential for.
var ErrNoCredential = errors.New("identity-session: that customer has no credential here")

// CodeSessionsNotEnded is a customer whose sessions this module cannot end:
// they sign in some other way than a password kept here.
const CodeSessionsNotEnded = "identity_session_sessions_not_ended"

// anchorPrecision is the resolution a session's issue and the anchor are
// compared at.
//
// A millisecond, rather than the second an expiry is written in: the cookie a
// reset answers with is issued in the same second as the anchor it moved, and
// at a second's resolution so would a cookie stolen a moment before. The anchor
// is truncated to it before it is written, so the database's microseconds
// cannot round it past the cookie issued after it.
const anchorPrecision = time.Millisecond

// issuedBefore says whether a session issued at one moment was issued before
// the anchor, at [anchorPrecision]. A zero anchor ends nothing; a zero issue is
// a cookie sealed before cookies carried one, and any anchor ends it.
func issuedBefore(issued, anchor time.Time) bool {
	if anchor.IsZero() {
		return false
	}

	return issued.Truncate(anchorPrecision).Before(anchor.Truncate(anchorPrecision))
}

// sessionAnchors answers the store's capability, or nil.
func (m *Module) sessionAnchors() SessionAnchors {
	anchors, _ := m.store.(SessionAnchors)

	return anchors
}

// endSessions moves the customer's anchor to now, so every session issued
// before this request proves nobody.
func (m *Module) endSessions(ctx context.Context, customerID string) error {
	anchors := m.sessionAnchors()
	if anchors == nil {
		return nil
	}

	return anchors.EndSessionsBefore(ctx, customerID, m.sessions.now().UTC().Truncate(anchorPrecision))
}

// revokeOtherSessions ends every session of the caller but the one making the
// request (ADR 0374): the anchor moves to now, and the caller gets a cookie
// issued after it.
func (m *Module) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	customerID, err := m.sessions.CustomerID(r)
	if err != nil {
		m.refuseSession(w, r, err)

		return
	}

	switch err := m.endSessions(r.Context(), customerID); {
	case errors.Is(err, ErrNoCredential):
		corehttp.WriteError(r.Context(), w, coreerrors.Conflict(CodeSessionsNotEnded,
			"this account does not sign in with a password kept here, so its sessions cannot be ended here"))

		return
	case err != nil:
		m.unavailable(w, r, "the other sessions could not be ended", err)

		return
	}

	m.log.InfoContext(r.Context(), "identity-session ended a customer's other sessions",
		"customer_id", customerID)

	m.sessions.Issue(w, customerID)
	corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)
}

// refuseSession answers a request whose session could not be read: a failure
// to read the anchor as itself, and every refusal as one 401.
func (m *Module) refuseSession(w http.ResponseWriter, r *http.Request, err error) {
	var classified *coreerrors.Error
	if errors.As(err, &classified) {
		corehttp.WriteError(r.Context(), w, err)

		return
	}

	corehttp.WriteError(r.Context(), w, coreerrors.Unauthorized(CodeNoSession,
		"the request carries no valid session"))
}

// The store keeps the anchor on the credential row (ADR 0374).
var _ SessionAnchors = pgCredentials{}

// SessionsValidFrom reads the credential's anchor; no row and no anchor are
// both the zero time.
func (s pgCredentials) SessionsValidFrom(ctx context.Context, customerID string) (time.Time, error) {
	var from *time.Time
	switch err := s.pool.QueryRow(ctx,
		`SELECT sessions_valid_from FROM customer_credentials WHERE customer_id = $1`,
		customerID).Scan(&from); {
	case errors.Is(err, pgx.ErrNoRows):
		return time.Time{}, nil
	case err != nil:
		return time.Time{}, fmt.Errorf("identity-session: the session anchor could not be read: %w", err)
	}
	if from == nil {
		return time.Time{}, nil
	}

	return *from, nil
}

// EndSessionsBefore moves the credential's anchor forward. GREATEST skips a
// NULL, so the first move sets it and a later one never moves it back.
func (s pgCredentials) EndSessionsBefore(ctx context.Context, customerID string, moment time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE customer_credentials
		 SET sessions_valid_from = GREATEST(sessions_valid_from, $2)
		 WHERE customer_id = $1`, customerID, moment)
	if err != nil {
		return fmt.Errorf("identity-session: the sessions could not be ended: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNoCredential
	}

	return nil
}
