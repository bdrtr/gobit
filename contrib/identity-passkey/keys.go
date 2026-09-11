package identitypasskey

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The codes the listing and the removal answer with.
const (
	// CodeNoSuchKey is a credential id that is not one of the caller's.
	//
	// ONE code for never existed, already gone, somebody else's and not even
	// base64url. Telling them apart would answer, for any id a caller cares to
	// try, whether it belongs to somebody — and the caller's OWN ids are in the
	// listing, so nothing is hidden from them that they could have asked for.
	CodeNoSuchKey = "identity_passkey_no_such_key"
	// CodeLastWayIn is a removal that would leave the account with no way in.
	CodeLastWayIn = "identity_passkey_last_way_in"
)

// keyDTO is one passkey on the wire.
type keyDTO struct {
	ID         string     `json:"id"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	Transports []string   `json:"transports"`
	// Removable is ADVISORY: it is what the rule says at the moment of the
	// listing, and the removal decides again under a lock. A client that
	// disabled its button on this is right nearly always and the endpoint is
	// what is authoritative.
	Removable bool `json:"removable"`
	// NotRemovableReason is present only when Removable is false.
	NotRemovableReason string `json:"not_removable_reason,omitempty"`
}

// keysResponse is the listing envelope.
//
// The list is under "data" and an empty one is an empty ARRAY, never null and
// never a 404: a person with no passkeys has an empty list of them, and a client
// that had to branch on null would branch wrongly the first time.
type keysResponse struct {
	Data []keyDTO `json:"data"`
}

// listKeys answers the caller's own passkeys.
//
// It takes no parameters and names no customer. A listing that accepted a
// customer id would be a listing of anybody's devices, and the caller is whoever
// the bound verifier proves — the same rule the registration follows.
func (m *Module) listKeys(w http.ResponseWriter, r *http.Request) {
	customerID, ok := m.callerOrRefuse(w, r)
	if !ok {
		return
	}

	keys, err := m.store.ListForCustomer(r.Context(), customerID)
	if err != nil {
		m.unavailable(w, r, "the keys could not be listed", err)

		return
	}

	// Whether another way in exists is asked ONCE for the whole listing, and it
	// is asked even when the person has several keys: the answer decides the
	// advisory flag on the single-key case and costs one query.
	//
	// A failure here is NOT a listing with the flag omitted. A client reading a
	// key without `removable` would have to guess, and the guess that matters is
	// the one that offers a button that ends an account.
	otherWayIn := false
	if len(keys) < 2 {
		otherWayIn, err = m.otherSignIn.Exists(r.Context(), customerID)
		if err != nil {
			m.cannotCheck(w, r, err)

			return
		}
	}

	response := keysResponse{Data: make([]keyDTO, 0, len(keys))}
	for _, key := range keys {
		dto := keyDTO{
			ID:         key.ID,
			CreatedAt:  key.CreatedAt,
			LastUsedAt: key.LastUsedAt,
			Transports: key.Transports,
			Removable:  len(keys) > 1 || otherWayIn,
		}
		if !dto.Removable {
			dto.NotRemovableReason = CodeLastWayIn
		}
		if dto.Transports == nil {
			dto.Transports = []string{}
		}
		response.Data = append(response.Data, dto)
	}

	corehttp.WriteJSON(r.Context(), w, http.StatusOK, response)
}

// removeKey removes one of the caller's own passkeys.
//
// # The order of the two questions
//
// The store answers first, under a lock, and it answers about ROWS. Only when it
// says the row was the last one does this ask whether something else is a way in
// — so the ordinary removal makes no cross-module call at all, and the question
// that can fail is asked only when its answer changes the outcome.
func (m *Module) removeKey(w http.ResponseWriter, r *http.Request) {
	customerID, ok := m.callerOrRefuse(w, r)
	if !ok {
		return
	}

	credentialID, err := decodeCredentialID(chi.URLParam(r, "credential_id"))
	if err != nil {
		// Not base64url is not a different answer from not yours: both are ids
		// this caller has no key for.
		m.noSuchKey(w, r)

		return
	}

	// Whether the last key may go is decided BEFORE the transaction, because the
	// question can reach another module and a query made while this removal
	// holds a row lock would take a second connection from the same pool.
	//
	// It is asked only when it can matter. A person with two keys never reaches
	// across the boundary at all, and a stale answer cannot widen the rule: the
	// guard is applied again under the lock, where the row count decides on its
	// own whenever there is more than one.
	allowLast, ok := m.mayRemoveTheLast(w, r, customerID)
	if !ok {
		return
	}

	surviving, err := m.store.Remove(r.Context(), customerID, credentialID, allowLast)
	switch {
	case errors.Is(err, ErrNoCredential):
		m.noSuchKey(w, r)

		return
	case errors.Is(err, ErrLastWayIn):
		corehttp.WriteError(r.Context(), w, coreerrors.Conflict(CodeLastWayIn,
			"that is the only way into this account, so it cannot be removed; register a "+
				"passkey on ANOTHER device first, or ask the shop to set a password").
			WithDetails(map[string]any{"credentials_remaining": surviving}))

		return
	case err != nil:
		m.unavailable(w, r, "the key could not be removed", err)

		return
	}

	corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)
}

// mayRemoveTheLast asks whether the account has a way in that is not a passkey.
//
// It answers the caller's second value as "carry on": a failure has already been
// written to the response, because "we could not check" must not become "you have
// no other way in" and must not become a removal either.
//
// The unlocked count it reads first can be stale, and both directions are safe.
// Stale-high means the question was skipped and the locked guard refuses; stale-low
// means it was asked when it need not have been, and the locked count permits the
// removal regardless of the answer.
func (m *Module) mayRemoveTheLast(
	w http.ResponseWriter, r *http.Request, customerID string,
) (allowLast, carryOn bool) {
	keys, err := m.store.ListForCustomer(r.Context(), customerID)
	if err != nil {
		m.unavailable(w, r, "the keys could not be counted", err)

		return false, false
	}
	if len(keys) > 1 {
		return false, true
	}

	allowLast, err = m.otherSignIn.Exists(r.Context(), customerID)
	if err != nil {
		m.cannotCheck(w, r, err)

		return false, false
	}

	return allowLast, true
}

// noSuchKey answers an id that is not the caller's.
func (m *Module) noSuchKey(w http.ResponseWriter, r *http.Request) {
	corehttp.WriteError(r.Context(), w, coreerrors.NotFound(CodeNoSuchKey,
		"you have no passkey with that identifier"))
}

// cannotCheck answers when nobody could say whether another way in exists.
//
// It is a 500 and not a 409, and the distinction is the whole reason
// [identitysession.ErrPasswordUnknown] is a named error: "you have no other way
// in" is a sentence for the person and "we could not check" is a fault of the
// installation. Folding them would tell somebody their account has one door when
// nobody looked.
func (m *Module) cannotCheck(w http.ResponseWriter, r *http.Request, err error) {
	if unknownOtherSignIn(err) {
		m.log.ErrorContext(r.Context(),
			"identity-passkey: whether the customer has another way in could not be determined",
			"error", err)
		corehttp.WriteError(r.Context(), w, coreerrors.Internal(CodeUnavailable,
			"whether you have another way into this account could not be checked, so "+
				"nothing was changed"))

		return
	}

	m.unavailable(w, r, "the other sign-in could not be asked", err)
}
