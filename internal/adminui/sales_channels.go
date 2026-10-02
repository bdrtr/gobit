package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The Sales channels screen (ADR 0352): the auth module's channels through
// its channel entity, for an operator who may read the users; an operator
// holding admin corrects each one's name, description and whether it is in
// use from what the row was drawn with.

// SalesChannelsPath lists the channels, and SalesChannelPath corrects one.
const (
	SalesChannelsPath = URLPrefix + "/sales-channels"
	SalesChannelPath  = SalesChannelsPath + "/{id}"
)

// salesChannelsLabel is what the section is called on screen.
const salesChannelsLabel = "Sales channels"

// salesChannelsPerPage is the list's page size, the other lists'.
const salesChannelsPerPage = 25

// The channel's fields the screen reads beside its id and name.
const (
	fieldChannelDescription = "description"
	fieldChannelDisabled    = "is_disabled"
)

// The row form's fields: the terms the row was drawn with and the typed
// ones.
const (
	formChannelReadName        = "read_name"
	formChannelReadDescription = "read_description"
	formChannelReadDisabled    = "read_disabled"
	formChannelName            = "name"
	formChannelDescription     = "description"
	formChannelDisabled        = "disabled"
	formChannelID              = "channel_id"
)

// ChannelReviser is the narrow surface a channel is corrected through: the
// auth module's.
type ChannelReviser interface {
	// ReviseSalesChannel corrects the channel from the terms read, both as
	// JSON, and refuses when they are no longer the ones read.
	ReviseSalesChannel(ctx context.Context, id string, read, next json.RawMessage) error
}

// channelTerms are a channel's terms as the surface takes them; the json
// tags spell the fields the channel provider publishes.
type channelTerms struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDisabled  bool   `json:"is_disabled"`
}

// channelRow is one channel as the screen draws it: its terms as read, and
// as its form offers them, what was typed when its correction was refused.
type channelRow struct {
	ID        string
	CreatedAt time.Time
	Read      channelTerms
	Form      channelTerms
	Refused   bool
}

// listSalesChannels renders the channels.
func (u *UI) listSalesChannels(w http.ResponseWriter, r *http.Request) {
	u.renderSalesChannels(w, r, http.StatusOK, "", nil)
}

// reviseSalesChannel corrects the channel in the path from the terms its row
// was drawn with and returns to the page, which says so; a refusal, a
// channel revised since or a name another channel holds included, comes back
// on the page with what was typed in that row (ADR 0352).
func (u *UI) reviseSalesChannel(w http.ResponseWriter, r *http.Request) {
	reviser, ok := u.users.(ChannelReviser)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Sales channels unavailable",
			"The auth module's panel surface cannot correct a sales channel in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	next := channelTerms{
		Name:        strings.TrimSpace(r.PostFormValue(formChannelName)),
		Description: strings.TrimSpace(r.PostFormValue(formChannelDescription)),
		IsDisabled:  r.PostFormValue(formChannelDisabled) != "",
	}
	readDisabled, err := strconv.ParseBool(r.PostFormValue(formChannelReadDisabled))
	if err != nil {
		err = errors.Invalid("admin_ui_read_channel",
			"Whether the channel was in use when its row was drawn could not be read; draw the list again.")
	}
	var readJSON, nextJSON []byte
	if err == nil {
		readJSON, err = json.Marshal(channelTerms{
			Name: r.PostFormValue(formChannelReadName), Description: r.PostFormValue(formChannelReadDescription),
			IsDisabled: readDisabled,
		})
	}
	if err == nil {
		nextJSON, err = json.Marshal(next)
	}
	if err == nil {
		err = reviser.ReviseSalesChannel(r.Context(), id, readJSON, nextJSON)
	}
	switch {
	case err == nil:
		landing := url.Values{paramWritten: {next.Name}}
		if page := pageNumber(r.URL.Query().Get("page")); page > 1 {
			landing.Set("page", strconv.Itoa(page))
		}
		corehttp.WriteRedirect(r.Context(), w, SalesChannelsPath+"?"+landing.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		typed := r.PostForm
		typed.Set(formChannelID, id)
		u.renderSalesChannels(w, r, http.StatusUnprocessableEntity, messageFor(err), typed)
	default:
		u.unexpectedFailure(w, r, err, "The sales channel could not be corrected")
	}
}

// renderSalesChannels lists the channels a page at a time, the newest first
// as the module orders them, with a refused correction's reason and what was
// typed in the row of the channel it was sent for. An operator who may
// correct holds admin, and with it the privilege to read the channels.
func (u *UI) renderSalesChannels(w http.ResponseWriter, r *http.Request, code int, refused string, typed url.Values) {
	page := pageNumber(r.URL.Query().Get("page"))
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntitySalesChannel,
		Fields: []string{fieldID, fieldChannelName, fieldChannelDescription, fieldChannelDisabled, fieldCreatedAt},
		Limit:  salesChannelsPerPage + 1,
		Offset: (page - 1) * salesChannelsPerPage,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The sales channels could not be read.")
		return
	}
	hasNext := len(records) > salesChannelsPerPage
	if hasNext {
		records = records[:salesChannelsPerPage]
	}

	refusedID := typed.Get(formChannelID)
	rows := make([]channelRow, 0, len(records))
	for _, record := range records {
		row := channelRow{
			ID:        recordString(record, fieldID),
			CreatedAt: recordTime(record, fieldCreatedAt),
			Read: channelTerms{
				Name: recordString(record, fieldChannelName), Description: recordString(record, fieldChannelDescription),
				IsDisabled: recordBool(record, fieldChannelDisabled),
			},
		}
		row.Form = row.Read
		if refusedID != "" && row.ID == refusedID {
			row.Refused = true
			row.Form = channelTerms{
				Name: typed.Get(formChannelName), Description: typed.Get(formChannelDescription),
				IsDisabled: typed.Get(formChannelDisabled) != "",
			}
		}
		rows = append(rows, row)
	}

	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, canRevise := u.users.(ChannelReviser)
	_, canMake := u.users.(ChannelMaker)
	// A refusal with no channel named is the new channel's form's (ADR 0353).
	var making url.Values
	if typed != nil && refusedID == "" {
		making = typed
	}
	data := map[string]any{
		canCreateKey: canMake && principal.HasScope(scopeAdmin),
		typedKey:     making,
		titleKey:     salesChannelsLabel,
		channelsKey:  rows,
		canReviseKey: canRevise && principal.HasScope(scopeAdmin),
		writtenKey:   r.URL.Query().Get(paramWritten),
		refusedKey:   refused,
	}
	addPaging(data, page, hasNext, SalesChannelsPath)

	u.templates.render(w, r, code, "sales_channels.gohtml", data)
}
