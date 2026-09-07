package webhookout

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// maxBodyBytes bounds a request body.
//
// A registration is a URL, a handful of topic names and a sentence of prose. Two
// kilobytes is generous for that and far too small to be a way of filling
// memory.
const maxBodyBytes = 2 << 10

// listLimit bounds one page of the delivery listing.
//
// A hundred rows is what a human reads before deciding; the pile's true size is
// reported alongside, so a page that is full never has to be mistaken for the
// whole story.
const listLimit = 100

// codeInvalidRequest is the error code of a malformed registration.
const codeInvalidRequest = "webhookout_invalid_request"

// createRequest is what an operator posts to register a receiver.
type createRequest struct {
	// URL is where deliveries are POSTed.
	URL string `json:"url"`
	// Topics are the event names this receiver wants.
	Topics []string `json:"topics"`
	// Description is what a human wrote about the receiver.
	Description string `json:"description"`
}

// createResponse is the ONLY time the secret is returned.
type createResponse struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	Topics      []string  `json:"topics"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	// Secret is the signing key. It is returned here and NOWHERE else — not by
	// the listing, not by any other endpoint — so it has to be stored by
	// whoever registered the receiver at this moment.
	Secret string `json:"secret"`
	// SecretNote says out loud that this is the only time. A field an
	// integrator has to read is better than a paragraph in documentation they
	// have not opened, and the cost of missing it is deleting the receiver and
	// registering it again.
	SecretNote string `json:"secret_note"`
}

// endpointResponse is a receiver as the listing shows it: no secret.
type endpointResponse struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	Topics      []string  `json:"topics"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// deliveryResponse is one delivery row as a human reads it.
type deliveryResponse struct {
	ID         string    `json:"id"`
	EndpointID string    `json:"endpoint_id"`
	URL        string    `json:"url"`
	Event      string    `json:"event"`
	EventID    string    `json:"event_id"`
	Attempts   int64     `json:"attempts"`
	LastStatus int       `json:"last_status"`
	LastError  string    `json:"last_error"`
	CreatedAt  time.Time `json:"created_at"`
	// At is when it was given up on for a dead delivery, and when it is next
	// due for a pending one. The field is named for what it is in both states
	// rather than being two nullable columns a reader has to decide between.
	At time.Time `json:"at"`
	// Age is how long this delivery has been owed, rendered. A timestamp
	// answers "when" and a human reading a pile is asking "how long", which is
	// the same subtraction done wrong at 3am.
	Age string `json:"age"`
}

// endpointListResponse is the answer of GET /admin/v1/webhooks.
//
// The forwarded set travels INSIDE the listing rather than beside it, because it
// is the answer to the question that brought the operator here — "why is my
// receiver not getting X" — and reading it next to the receivers is cheaper than
// finding it in a changelog.
type endpointListResponse struct {
	Data            []endpointResponse `json:"data"`
	Count           int                `json:"count"`
	ForwardedTopics []string           `json:"forwarded_topics"`
}

// deliveryListResponse is the answer of GET /admin/v1/webhooks/deliveries.
//
// # One type for two listings, and the pointers are why
//
// "state=dead" carries five extra facts and "state=pending" carries none of
// them. The optional fields are POINTERS and slices rather than plain values so
// that a zero is told apart from an absence: the dead listing writes "total": 0
// when the pile is empty — which is the number an operator most wants to see —
// and `omitempty` on a plain int64 would DROP exactly that, turning "the pile is
// clear" into "no answer".
//
// The schema derived from this type therefore requires data, count and state and
// declares the rest optional, which is true of both listings and of neither on
// its own.
type deliveryListResponse struct {
	Data  []deliveryResponse `json:"data"`
	Count int                `json:"count"`
	State string             `json:"state"`
	// Total is the WHOLE pile, not the page. It is the number that decides
	// whether anybody is woken up, and a listing that reported only what fitted
	// would report the page size during an incident of any size.
	Total           *int64   `json:"total,omitempty"`
	AttemptsAllowed *int64   `json:"attempts_allowed,omitempty"`
	RetryWindow     string   `json:"retry_window,omitempty"`
	Exits           []string `json:"exits,omitempty"`
}

// deletedResponse is the answer of DELETE /admin/v1/webhooks/{id}.
type deletedResponse struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// deliveryActionResponse is the answer of a redrive or a discard.
type deliveryActionResponse struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	// Remaining is how much of the dead pile is left after the action.
	Remaining int64 `json:"remaining"`
	// JobAlarmClears says whether this action was the one that silenced the
	// operator alarm. It is reported rather than left to be inferred: the alarm
	// also watches orphaned rows, so an empty pile is NOT on its own the same
	// as a quiet alarm.
	JobAlarmClears bool `json:"job_alarm_clears"`
}

// singleEnvelope is the shape of a single-object answer.
//
// It is the repository's envelope, not one invented for this plugin: the value
// under "data" (plan Section 8, and plugins/searchpg answers the same way). A
// plugin that shaped its own responses differently would make a generated
// client carry two conventions for one API.
type singleEnvelope struct {
	Data any `json:"data"`
}

// handleCreate registers a receiver.
//
// # The registration surface EXISTS, and that is the point of it
//
// A table with no way to fill it is the defect this repository names most
// often. Everything below — the URL check, the topic check — is refusal at
// REGISTRATION rather than at delivery, because a receiver registered with a
// topic gobit does not publish is a subscription that never fires, and the only
// moment anyone is present to be told is this request.
func (m *webhookModule) handleCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createRequest
	if err := decodeBody(r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	target, err := validateURL(req.URL)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	topics, err := validateTopics(req.Topics)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	created, err := m.store.createEndpoint(ctx, target, topics, strings.TrimSpace(req.Description))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	m.log.InfoContext(ctx, "a webhook receiver was registered",
		"endpoint_id", created.ID, "url", created.URL, "topics", strings.Join(topics, ","))

	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: createResponse{
		ID:          created.ID,
		URL:         created.URL,
		Topics:      created.Topics,
		Description: created.Description,
		CreatedAt:   created.CreatedAt,
		Secret:      created.Secret,
		SecretNote: "this is the only time the secret is returned; " +
			"store it now, there is no endpoint that shows it again",
	}})
}

// handleList shows the registered receivers.
func (m *webhookModule) handleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	endpoints, err := m.store.listEndpoints(ctx)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	items := make([]endpointResponse, 0, len(endpoints))
	for _, e := range endpoints {
		items = append(items, endpointResponse{
			ID: e.ID, URL: e.URL, Topics: e.Topics,
			Description: e.Description, CreatedAt: e.CreatedAt,
		})
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, endpointListResponse{
		Data: items, Count: len(items), ForwardedTopics: ForwardedTopics,
	})
}

// handleDelete removes a receiver.
//
// The deliveries already owed to it are LEFT, and a deleted receiver with a
// dead delivery still shows in the listing — see the migration for why there is
// no cascade. What the deletion does stop is new deliveries being enqueued,
// which is the operator's actual intent when a receiver has gone away.
func (m *webhookModule) handleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	removed, err := m.store.deleteEndpoint(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	if !removed {
		corehttp.WriteError(ctx, w, coreerrors.NotFound(codeNotFound,
			"there is no webhook receiver with the id %q", id))

		return
	}

	m.log.InfoContext(ctx, "a webhook receiver was removed", "endpoint_id", id)

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{
		Data: deletedResponse{ID: id, Deleted: true},
	})
}

// The delivery listing's states.
const (
	// stateDead is the pile a human has to look at.
	stateDead = "dead"
	// statePending is what is still owed and being retried.
	statePending = "pending"
)

// handleDeliveries shows what was given up on, or what is still owed.
//
// # A dead letter has to be READABLE, and this is where
//
// B12's rule: a write-only ledger nobody reads is the same mistake as
// audit_log. The delivery job FAILS its run while the pile is non-empty, which
// is what makes `gobit jobs` say something is wrong; this is where a human goes
// to find out what, and it carries what a decision needs — which event, to
// which URL, how many attempts, the last status and the last error, and how
// long the delivery has been owed.
//
// The pending half exists because the pile is EMPTY for the first day of an
// outage. A surface that could only show dead letters would answer "nothing is
// wrong" for twenty-six hours while every delivery to a broken receiver piled
// up behind a backoff.
func (m *webhookModule) handleDeliveries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	state := r.URL.Query().Get("state")
	if state == "" {
		state = stateDead
	}

	switch state {
	case stateDead:
		report, err := m.store.deadLetters(ctx, listLimit)
		if err != nil {
			corehttp.WriteError(ctx, w, err)

			return
		}

		items := renderDeliveries(report.Oldest)
		total := report.Count
		allowed := maxAttempts
		corehttp.WriteJSON(ctx, w, http.StatusOK, deliveryListResponse{
			Data:            items,
			Count:           len(items),
			State:           stateDead,
			Total:           &total,
			AttemptsAllowed: &allowed,
			RetryWindow:     deliveryWindow().String(),
			Exits: []string{
				"POST /admin/v1/webhooks/deliveries/{id}/redrive",
				"POST /admin/v1/webhooks/deliveries/{id}/discard",
			},
		})
	case statePending:
		rows, err := m.store.pending(ctx, listLimit)
		if err != nil {
			corehttp.WriteError(ctx, w, err)

			return
		}

		items := renderDeliveries(rows)
		corehttp.WriteJSON(ctx, w, http.StatusOK, deliveryListResponse{
			Data: items, Count: len(items), State: statePending,
		})
	default:
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"state has to be %q or %q; %q was given", stateDead, statePending, state))
	}
}

// handleRedrive puts one dead delivery back in the queue.
//
// One id per call, and refusing a bulk flag is deliberate rather than
// unfinished: the same argument `gobit deadletters` records. The act is cheap —
// a single primary-key update — so the refusal costs nothing and buys the
// reading of the row that a one-keystroke "redrive all" would skip.
func (m *webhookModule) handleRedrive(w http.ResponseWriter, r *http.Request) {
	m.act(w, r, "redriven", m.store.redrive)
}

// handleDiscard removes one dead delivery for good.
func (m *webhookModule) handleDiscard(w http.ResponseWriter, r *http.Request) {
	m.act(w, r, "discarded", m.store.discard)
}

// act runs one of the two exits and closes with whether the alarm will clear.
//
// The closing sentence is the point of sharing this code. The operator arrived
// because a job is failing; the question they leave with is whether it will
// stop failing, and answering it needs a re-read of the pile AFTER the act. A
// response that only said "ok" would send them back to the job listing to find
// out.
func (m *webhookModule) act(
	w http.ResponseWriter, r *http.Request,
	verb string, do func(ctx context.Context, id string) (bool, error),
) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	acted, err := do(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	if !acted {
		corehttp.WriteError(ctx, w, coreerrors.NotFound(codeNotFound,
			"there is no DEAD webhook delivery with the id %q; a delivery that is still "+
				"being retried cannot be %s", id, verb))

		return
	}

	report, err := m.store.deadLetters(ctx, 1)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	orphans, err := m.store.orphanCount(ctx)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	m.log.InfoContext(ctx, "a dead webhook delivery was acted on",
		"delivery_id", id, "action", verb, "remaining", report.Count)

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: deliveryActionResponse{
		ID:             id,
		Action:         verb,
		Remaining:      report.Count,
		JobAlarmClears: report.Count == 0 && orphans == 0,
	}})
}

// renderDeliveries turns rows into the listing's shape.
func renderDeliveries(rows []deadLetter) []deliveryResponse {
	out := make([]deliveryResponse, 0, len(rows))
	for i := range rows {
		d := &rows[i]
		out = append(out, deliveryResponse{
			ID: d.ID, EndpointID: d.EndpointID, URL: d.URL,
			Event: d.EventName, EventID: d.EventID,
			Attempts: d.Attempts, LastStatus: d.LastStatus, LastError: d.LastError,
			CreatedAt: d.CreatedAt, At: d.DeadAt,
			Age: time.Since(d.CreatedAt).Truncate(time.Second).String(),
		})
	}

	return out
}

// validateTopics refuses a topic this installation cannot deliver.
//
// # Why this is a refusal and not a warning
//
// A receiver registered for "order.shipped" — a topic gobit does not publish —
// would sit in the table looking correct and receive nothing, forever, with
// nobody able to tell it apart from a receiver whose events simply have not
// happened yet. That is the same silent-subscription failure the arch gate
// catches inside the repository, arriving through the API instead, and the only
// moment an integrator is present to be told is this request.
//
// The message names the supported set rather than saying "invalid topic",
// because the next thing that happens otherwise is a support ticket asking what
// the topics are.
func validateTopics(topics []string) ([]string, error) {
	if len(topics) == 0 {
		return nil, coreerrors.Invalid(codeInvalidRequest,
			"topics cannot be empty; a receiver with no topics is registered, visible and "+
				"can never be delivered to. The supported topics are: %s",
			strings.Join(ForwardedTopics, ", "))
	}

	out := make([]string, 0, len(topics))
	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if !slices.Contains(ForwardedTopics, topic) {
			return nil, coreerrors.Invalid(codeInvalidRequest,
				"%q is not a topic this installation publishes; the supported topics are: %s",
				topic, strings.Join(ForwardedTopics, ", "))
		}
		if slices.Contains(out, topic) {
			continue
		}
		out = append(out, topic)
	}

	return out, nil
}

// validateURL refuses a destination this sender will not post to.
//
// # https, unless the host is loopback
//
// The signature proves who sent a delivery; it proves nothing about who else
// read it. A body carrying order ids, totals and item counts over plaintext
// http is readable by everything on the path, and the receiver has no way to
// notice. So plain http is refused — except to a loopback address, which cannot
// leave the machine and is what a local integration test posts to.
//
// # What this does NOT do
//
// It does not resolve the host and refuse a private address. An operator with
// the webhook:write scope can already reach a great deal of an installation,
// so pointing gobit at 169.254.169.254 is not a privilege they gain here; and a
// resolve-then-connect check is famously not a check at all, because the name
// can resolve differently the second time. Closing it properly means a dialer
// that refuses the address at connect time, which is a separate change with its
// own configuration shape.
func validateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", coreerrors.Invalid(codeInvalidRequest,
			"url has to be an absolute http(s) URL; %q was given", raw)
	}

	switch parsed.Scheme {
	case "https":
		return raw, nil
	case "http":
		if isLoopback(parsed.Hostname()) {
			return raw, nil
		}

		return "", coreerrors.Invalid(codeInvalidRequest,
			"url has to be https for a host that is not loopback: the signature proves who "+
				"SENT a delivery, not who else read it, and the body carries order data")
	default:
		return "", coreerrors.Invalid(codeInvalidRequest,
			"url has to be an absolute http(s) URL; the scheme %q is not one", parsed.Scheme)
	}
}

// isLoopback reports whether a host name can only mean this machine.
func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "[::1]"
}

// decodeBody reads a bounded, strict JSON body.
//
// Unknown fields are REFUSED. The producer here is a human or their script, so
// an unrecognized field is a typo — "topic" for "topics" would otherwise
// register a receiver with no topics and no complaint.
func decodeBody(r *http.Request, target any) error {
	reader := http.MaxBytesReader(nil, r.Body, maxBodyBytes)

	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"the request body could not be read")
	}
	if _, err := reader.Read(make([]byte, 1)); err != io.EOF {
		return coreerrors.Invalid(codeInvalidRequest,
			"the request body has to hold exactly one JSON object")
	}

	return nil
}
