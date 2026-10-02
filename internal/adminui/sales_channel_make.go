package adminui

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// Making a sales channel (ADR 0353): the Sales channels screen makes one
// with its name, description and whether it starts disabled, for an
// operator holding admin.

// ChannelMaker is the narrow surface a channel is made through: the auth
// module's.
type ChannelMaker interface {
	// MakeSalesChannel makes a channel and returns its id.
	MakeSalesChannel(ctx context.Context, name, description string, disabled bool) (string, error)
}

// makeSalesChannel makes the channel typed and returns to the screen, which
// says so; a refusal, a name another live channel holds included, comes
// back on the screen with what was typed in the form (ADR 0353).
func (u *UI) makeSalesChannel(w http.ResponseWriter, r *http.Request) {
	maker, ok := u.users.(ChannelMaker)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Sales channels unavailable",
			"The auth module's panel surface cannot make a sales channel in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	name := strings.TrimSpace(r.PostFormValue(formChannelName))
	_, err := maker.MakeSalesChannel(r.Context(), name,
		strings.TrimSpace(r.PostFormValue(formChannelDescription)), r.PostFormValue(formChannelDisabled) != "")
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, SalesChannelsPath+"?"+url.Values{paramWritten: {name}}.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err):
		u.renderSalesChannels(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The sales channel could not be made")
	}
}
