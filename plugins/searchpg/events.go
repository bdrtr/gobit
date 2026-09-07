package searchpg

import (
	"context"
	"encoding/json"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// This file holds the subscribers that keep the index FRESH.
//
// # The error policy: an error IS returned, but it is NOT a "retry" request
//
// The [eventbus.EventBus] contract is explicit: if a handler returns an error,
// the error is logged and the event COUNTS AS PROCESSED; no backend redelivers
// it. The Redis backend ACKs the message INDEPENDENTLY of the handler's result
// (see the deferred ack inside redisBus.dispatch). So the sentence "if I return
// the error the bus will try again" is FALSE in this framework, and a handler
// relying on it would miss a missed event forever.
//
// Why return an error at all, then? Because SWALLOWING it (returning nil) is the
// only option with a real cost: the bus logs a handler that returns an error at
// ERROR level together with the event name, the event id and the error chain.
// Had we returned nil, the index falling behind the record would be visible
// nowhere.
//
// Nor is there a retry inside the handler. The contract allows one but it would
// be wrong here: a handler waiting while the catalog is unreachable piles up
// goroutines on the InMemory backend, and on the Redis backend it blocks the
// single consumer loop and delays every event on the SAME stream. One event
// being late is cheaper than the whole catalog stream stopping.
//
// The accepted price: a missed event leaves the index behind the record. There
// is a repair path and it is called by hand — POST /admin/v1/search/reindex (see
// [searchModule.reindex]). Automatic repair (an outbox or a scanning job) was
// deliberately not written; the plugin's scope is a search index, not a second
// reliable-delivery mechanism.
//
// # The handlers are IDEMPOTENT and REENTRANT
//
// The contract guarantees no ordering, and the InMemory backend can call the
// same handler concurrently. Both paths are reduced to a single statement (an
// upsert, or an unconditional delete), so processing one event twice gives the
// SAME result as processing it once.

// codeEventInvalid reports that an event payload does not match the contract.
const codeEventInvalid = "searchpg_event_payload_invalid"

// productView is the INDEXED subset of the storefront record's fields.
//
// The whole record is not defined here and unrecognized fields are ignored
// SILENTLY (json.Unmarshal's default). That is the opposite of the
// DisallowUnknownFields rule used on requests, and it is deliberate: the side
// PRODUCING the body here is the catalog itself rather than a caller, so an
// unrecognized field is not a typo but a new field added to product. Treating it
// as an error would bring indexing down every time the catalog grew.
type productView struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Description string `json:"description"`
	Variants    []struct {
		Title string `json:"title"`
		SKU   string `json:"sku"`
	} `json:"variants"`
	Tags []struct {
		Value string `json:"value"`
	} `json:"tags"`
}

// productWritten handles the "product.created" and "product.updated" events.
//
// # The status in the payload is DELIBERATELY not used
//
// The event carries the product's publication status AT THAT MOMENT and gives a
// subscriber the right to "cheaply drop events not worth reading": on a bulk
// update over draft products it could say "remove it from the index" without
// reading the record at all.
//
// That shortcut was NOT taken, because the value can be STALE and the bus
// guarantees no ordering. The concrete failure: if a product is moved to draft
// and published again immediately after, and the two events are delivered out of
// order, the shortcut removes the product from the index and it stays INVISIBLE
// in search until the next write — without producing any error at all. Reading
// the record on every event removes that race: the decision rests on the
// catalog's CURRENT state rather than on what the event says, and the handler
// that runs last always writes the freshest truth.
//
// The price paid is one read even on draft-product updates. If this path gets
// hot, the right step is not to trust the status but to add a buffer that
// processes events in batches.
func (m *searchModule) productWritten(ctx context.Context, e eventbus.Event) error {
	id, err := eventProductID(e)
	if err != nil {
		return err
	}
	if err := m.ready(); err != nil {
		return err
	}

	documents, err := m.documents(ctx, []string{id})
	if err != nil {
		return err
	}

	// NO catalog record came back: the product was unpublished, archived, or
	// deleted between the event and this read. Because the index is a mirror of
	// the storefront, the right action is not to write but to REMOVE; otherwise
	// an unpublished product would go on appearing in search.
	if len(documents) == 0 {
		removed, err := m.index.Delete(ctx, id)
		if err != nil {
			return err
		}
		m.log.DebugContext(ctx, "the product is not visible in the storefront; it was removed from the index",
			"event", e.Name, "product_id", id, "removed", removed)

		return nil
	}

	if err := m.index.Upsert(ctx, documents); err != nil {
		return err
	}
	m.log.DebugContext(ctx, "the product was indexed", "event", e.Name, "product_id", id)

	return nil
}

// productDeleted handles the "product.deleted" event.
//
// The catalog is NOT READ at all: a soft-deleted record comes back from no read
// anyway, so the read round would always end with an empty result. The deletion
// also does not look at the status — which is why the event payload carrying no
// status is not a gap.
func (m *searchModule) productDeleted(ctx context.Context, e eventbus.Event) error {
	id, err := eventProductID(e)
	if err != nil {
		return err
	}
	if err := m.ready(); err != nil {
		return err
	}

	removed, err := m.index.Delete(ctx, id)
	if err != nil {
		return err
	}
	m.log.DebugContext(ctx, "the deleted product was removed from the index",
		"event", e.Name, "product_id", id, "removed", removed)

	return nil
}

// eventProductID reads the product id from the event payload.
//
// That the value is a STRING is the contract (see product's service/events.go):
// because the Redis backend turns the payload into JSON, a numeric field reaches
// a subscriber as a float64, and the rule "every value is a string" exists
// precisely to prevent that. Carrying on quietly with an empty id when the type
// does not match would write rubbish into the index; returning an error makes
// the broken contract visible in the log.
func eventProductID(e eventbus.Event) (string, error) {
	raw, ok := e.Data[eventFieldProductID]
	if !ok {
		return "", coreerrors.Invalid(codeEventInvalid,
			"the payload of the %q event has no %q field", e.Name, eventFieldProductID)
	}

	id, ok := raw.(string)
	if !ok {
		return "", coreerrors.Invalid(codeEventInvalid,
			"the %q field of the %q event has to be a string (%T arrived)", e.Name, eventFieldProductID, raw)
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return "", coreerrors.Invalid(codeEventInvalid,
			"the %q field of the %q event is empty", e.Name, eventFieldProductID)
	}

	return id, nil
}

// documents reads the catalog records of the given ids and turns them into
// search documents.
//
// No channel id is passed (nil): the index is independent of the channel and the
// filtering is done by the catalog at read time (see [catalogRequest]).
//
// No document is produced for an id the catalog does not have; the returned
// slice may be SHORTER than what was asked for, and that is not an error.
func (m *searchModule) documents(ctx context.Context, ids []string) ([]document, error) {
	records, err := m.catalog.products(ctx, ids, nil)
	if err != nil {
		return nil, err
	}

	documents := make([]document, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		b, err := buildDocument(record)
		if err != nil {
			return nil, err
		}
		// Deduplication is MANDATORY: the single-statement upsert is refused by
		// PostgreSQL the second time it sees one id. The catalog returns every id
		// once today; this gate exists so as not to depend on that behavior.
		if _, again := seen[b.productID]; again {
			continue
		}
		seen[b.productID] = struct{}{}
		documents = append(documents, b)
	}

	return documents, nil
}

// buildDocument builds the weighted search document from a storefront record.
//
// The weight distribution is documented on [document]. A record with no id returns
// an ERROR: writing a row whose primary key is empty corrupts the index quietly
// and would produce a search result id that can never be resolved.
func buildDocument(raw json.RawMessage) (document, error) {
	var product productView
	if err := json.Unmarshal(raw, &product); err != nil {
		return document{}, coreerrors.Wrap(err, coreerrors.KindInternal, codeCatalogResponse,
			"the catalog record could not be decoded")
	}
	if strings.TrimSpace(product.ID) == "" {
		return document{}, coreerrors.Internal(codeCatalogResponse,
			"the catalog record has no %q field; the schema of the %q surface may have changed",
			"id", catalogInteropName)
	}

	keywords := make([]string, 0, 2+len(product.Variants)*2+len(product.Tags))
	keywords = append(keywords, product.Handle, product.Subtitle)
	for _, variant := range product.Variants {
		keywords = append(keywords, variant.Title, variant.SKU)
	}
	for _, tag := range product.Tags {
		keywords = append(keywords, tag.Value)
	}

	return document{
		productID: product.ID,
		title:     product.Title,
		keywords:  strings.Join(keywords, " "),
		body:      product.Description,
	}, nil
}
