package identitysession

import (
	"encoding/json"
	"io"
	"net/http"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// maxBodyBytes bounds a sign-in body.
//
// An e-mail and a password fit in a few hundred bytes; the limit is what keeps a
// caller from making the server hold a megabyte before it decides the password
// is wrong.
const maxBodyBytes = 4 << 10

// The codes this module answers with. A client branches on these.
const (
	// CodeRejected is a sign-in whose e-mail and password do not match.
	//
	// It is ONE code for a wrong password, an unknown address and a stored hash
	// this module cannot read: telling them apart would let anybody enumerate
	// which addresses have accounts, one request at a time.
	CodeRejected = "identity_session_rejected"
	// CodeInvalid is a request this module cannot read.
	CodeInvalid = "identity_session_invalid"
	// CodeNotWritten is a credential the database refused.
	CodeNotWritten = "identity_session_not_written"
	// CodeUnavailable is a failure on this module's side.
	CodeUnavailable = "identity_session_unavailable"
)

// decode reads a JSON body and answers the caller itself when it cannot.
//
// It returns whether the handler may continue, which keeps the error path out of
// every handler. The answer goes through [corehttp.WriteError] and the error
// comes from [coreerrors]: the envelope's shape, the masking and the request id
// are the core's decisions, and a hand-written body here would be a second copy
// of all three that drifts from the routes beside it.
func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		corehttp.WriteError(r.Context(), w,
			coreerrors.Invalid(CodeInvalid, "the request body could not be parsed"))

		return false
	}

	return true
}
