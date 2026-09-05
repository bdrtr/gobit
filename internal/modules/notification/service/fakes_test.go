package service_test

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// fakeStore is the in-memory counterpart of service.Store.
//
// The ONLY behavior imitated from the real store is the idempotency key: a
// second record IS NOT OPENED for the same (template, reference) pair. The
// service's claim that it "does not send duplicate notifications" rests
// entirely on this, and in the real database the thing that provides it is the
// unique index; the fake store applies the same rule with a map, so that the
// claim can be tested without Docker as well. That the constraint really is in
// place is verified separately in the integration test.
type fakeStore struct {
	mu sync.Mutex
	// records holds the log records by identifier.
	records map[string]models.Delivery
	// keys is the "<template>\x00<reference>" -> record identifier mapping; it
	// is the counterpart of the unique index.
	keys map[string]string
	// claimCount counts HOW MANY TIMES ClaimDelivery WAS CALLED.
	claimCount int

	// claimErr, when set, makes ClaimDelivery return this error.
	claimErr error
	// finishErr, when set, makes FinishDelivery return this error; the path
	// where the outcome cannot be written is tested with it.
	finishErr error
	// listErr, when set, makes ListDeliveries return this error.
	listErr error
}

// newFakeStore produces an empty fake store.
func newFakeStore() *fakeStore {
	return &fakeStore{
		records: map[string]models.Delivery{},
		keys:    map[string]string{},
	}
}

// deliveryKey produces the idempotency key.
//
// NUL is used as the separator: two different pairs such as "a.b"+"c" and
// "a"+"b.c" falling onto the same key would have meant the fake store behaving
// more strictly than the real index, and the test verifying a collision that
// does not exist.
func deliveryKey(template, reference string) string { return template + "\x00" + reference }

func (s *fakeStore) ClaimDelivery(_ context.Context, d models.Delivery) (models.Delivery, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.claimCount++
	if s.claimErr != nil {
		return models.Delivery{}, false, s.claimErr
	}

	key := deliveryKey(d.Template, d.Reference)
	if _, exists := s.keys[key]; exists {
		return models.Delivery{}, false, nil
	}

	d.CreatedAt = time.Now().UTC()
	d.UpdatedAt = d.CreatedAt
	s.records[d.ID] = d
	s.keys[key] = d.ID

	return d, true, nil
}

func (s *fakeStore) FinishDelivery(
	_ context.Context,
	id string,
	status models.DeliveryStatus,
	failure string,
) (models.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finishErr != nil {
		return models.Delivery{}, s.finishErr
	}

	record, ok := s.records[id]
	if !ok {
		return models.Delivery{}, errors.NotFound("test_not_found", "there is no record: %s", id)
	}

	record.Status = status
	record.Error = failure
	record.UpdatedAt = time.Now().UTC()
	s.records[id] = record

	return record, nil
}

func (s *fakeStore) GetDelivery(_ context.Context, id string) (models.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return models.Delivery{}, errors.NotFound("test_not_found", "there is no record: %s", id)
	}
	return record, nil
}

func (s *fakeStore) ListDeliveries(
	_ context.Context,
	filter models.DeliveryFilter,
) ([]models.Delivery, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listErr != nil {
		return nil, 0, s.listErr
	}

	out := make([]models.Delivery, 0, len(s.records))
	for id := range s.records {
		record := s.records[id]
		if filter.Reference != nil && record.Reference != *filter.Reference {
			continue
		}
		if filter.Status != nil && record.Status.String() != *filter.Status {
			continue
		}
		out = append(out, record)
	}
	return out, int64(len(out)), nil
}

// allRecords returns all the records of the store (for the test assertions).
func (s *fakeStore) allRecords() []models.Delivery {
	records, _, _ := s.ListDeliveries(context.Background(), models.DeliveryFilter{})
	return records
}

// fakeProvider is the scriptable counterpart of
// coreprovider.NotificationProvider.
type fakeProvider struct {
	mu sync.Mutex
	id string
	// sent holds the notifications that reached the provider; its COUNT is the
	// only proof of the claim "it was not sent a second time".
	sent []coreprovider.Notification
	// err, when set, makes Send return this error.
	err error
}

// newFakeProvider produces a fake provider with the given identifier.
func newFakeProvider(id string) *fakeProvider { return &fakeProvider{id: id} }

func (p *fakeProvider) ID() string { return p.id }

func (p *fakeProvider) Send(_ context.Context, n coreprovider.Notification) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// The call IS RECORDED whatever its outcome: the counter has to measure not
	// "how many notifications went out successfully" but "how many times the
	// provider was reached". A counter that did not count the failed attempt
	// could not test the claim "there is no retry after a failure".
	p.sent = append(p.sent, n)

	return p.err
}

// callCount returns how many times the provider was reached.
func (p *fakeProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sent)
}

// lastNotification returns the last notification that reached the provider.
func (p *fakeProvider) lastNotification() coreprovider.Notification {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.sent) == 0 {
		return coreprovider.Notification{}
	}
	return p.sent[len(p.sent)-1]
}

// fakeContacts is the scriptable counterpart of service.OrderContactReader.
//
// It produces the body of the real surface (order.interop) as a STRING:
// encoding it from a typed struct would give the illusion that the two sides
// share the same Go type — whereas the only thing shared is the JSON SCHEMA and
// the divergence happens precisely there.
type fakeContacts struct {
	mu sync.Mutex
	// body is the raw response to be read.
	body string
	// err, when set, makes the reading return this error.
	err error
	// requested is the order identifier of the last call.
	requested string
	// calls is the number of reads.
	calls int
}

func (c *fakeContacts) OrderContactJSON(_ context.Context, orderID string) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls++
	c.requested = orderID
	if c.err != nil {
		return nil, c.err
	}
	return json.RawMessage(c.body), nil
}

// newService builds a service with fake dependencies.
func newService(store service.Store, providers *service.ProviderRegistry, id string, contacts service.OrderContactReader) (*service.Service, error) {
	return service.New(service.Options{
		Store:      store,
		Providers:  providers,
		ProviderID: id,
		Contacts:   contacts,
	})
}
