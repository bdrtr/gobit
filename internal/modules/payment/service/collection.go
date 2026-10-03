package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// CreateCollectionInput holds the fields of a new payment collection.
type CreateCollectionInput struct {
	// Reference is the identifier of the caller's own record (a cart or an
	// order); it is required. IT IS NOT A FOREIGN KEY and its existence is not
	// validated here.
	Reference string
	// Amount is the total amount that has to be collected (minor unit); it has
	// to be positive.
	Amount int64
	// CurrencyCode is the ISO 4217 code; it is required.
	CurrencyCode string
	// CustomerID is WHOSE money the collection gathers; it can be left empty.
	//
	// A guest paying by card names nobody. The tenders whose funds belong to a
	// PERSON (store credit) cannot work without this field, and the alternative
	// was to read the owner from the client's data — that is, spending somebody
	// else's balance by writing their name (ADR 0152).
	CustomerID string
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
}

// CreatePaymentCollection creates a new payment collection.
//
// The collection is born in the "not_paid" status: no session has been opened
// and nothing has been captured yet. The amount CANNOT BE ZERO (see
// [models.MinAmount]); no payment is collected for an order whose amount is
// zero, and since such a collection could never become "captured" it would be
// a dead record waiting for payment forever.
func (s *Service) CreatePaymentCollection(
	ctx context.Context,
	in CreateCollectionInput,
) (models.PaymentCollection, error) {
	reference := strings.TrimSpace(in.Reference)
	if err := requireText("reference", reference); err != nil {
		return models.PaymentCollection{}, err
	}
	if err := requireAmount("amount", in.Amount); err != nil {
		return models.PaymentCollection{}, err
	}
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return models.PaymentCollection{}, err
	}

	return s.store.CreatePaymentCollection(ctx, models.PaymentCollection{
		ID:           models.NewPaymentCollectionID(),
		Reference:    reference,
		CustomerID:   strings.TrimSpace(in.CustomerID),
		Amount:       in.Amount,
		CurrencyCode: currency,
		Status:       models.CollectionNotPaid,
		Metadata:     in.Metadata,
	})
}

// GetPaymentCollection returns the collection by its identifier; errors.NotFound
// if there is none.
func (s *Service) GetPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error) {
	if err := requireText("id", id); err != nil {
		return models.PaymentCollection{}, err
	}
	return s.store.GetPaymentCollection(ctx, id)
}

// ListCollectionsInput is the input of a collection listing.
type ListCollectionsInput struct {
	// Reference, if given, returns only the collections attached to that
	// reference.
	Reference *string
	// Status, if given, returns only the collections in that status.
	Status *string
	// Page holds the paging parameters.
	Page Page
}

// ListPaymentCollections returns the collections page by page.
// The second return value is the count of ALL the rows matching the filter,
// not of the page.
func (s *Service) ListPaymentCollections(
	ctx context.Context,
	in ListCollectionsInput,
) ([]models.PaymentCollection, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}

	filter := models.CollectionFilter{Limit: page.Limit, Offset: page.Offset}
	if in.Reference != nil {
		reference := strings.TrimSpace(*in.Reference)
		if err := requireText("reference", reference); err != nil {
			return nil, 0, err
		}
		filter.Reference = &reference
	}
	if in.Status != nil {
		status := models.CollectionStatus(strings.TrimSpace(*in.Status))
		if !status.Valid() {
			return nil, 0, invalidStatus(*in.Status)
		}
		value := status.String()
		filter.Status = &value
	}

	return s.store.ListPaymentCollections(ctx, filter)
}

// ListPaymentCollectionsByIDs returns the collections of the given identifiers
// in a SINGLE query. No record is returned for an identifier that is not
// found; that is not an error.
func (s *Service) ListPaymentCollectionsByIDs(
	ctx context.Context,
	ids []string,
) ([]models.PaymentCollection, error) {
	if len(ids) == 0 {
		return []models.PaymentCollection{}, nil
	}
	return s.store.PaymentCollectionsByIDs(ctx, ids)
}

// ListPaymentMomentsByIDs returns WHEN each collection's money moved.
//
// It is a second call rather than more fields on the collection: the amounts
// are asked for on every read and the moments almost never, and making the
// common read carry two correlated aggregates would be paying for the rare
// question every time.
func (s *Service) ListPaymentMomentsByIDs(
	ctx context.Context,
	ids []string,
) ([]models.PaymentMoments, error) {
	if len(ids) == 0 {
		return []models.PaymentMoments{}, nil
	}

	return s.store.PaymentMomentsByCollectionIDs(ctx, ids)
}

// ListPaymentMovementsByIDs returns every capture and refund of the
// collections, oldest first within each (ADR 0170).
func (s *Service) ListPaymentMovementsByIDs(
	ctx context.Context,
	ids []string,
) ([]models.PaymentMovement, error) {
	if len(ids) == 0 {
		return []models.PaymentMovement{}, nil
	}

	return s.store.PaymentMovementsByCollectionIDs(ctx, ids)
}

// ListPaymentSessions returns the collection's sessions.
//
// The collection's existence is verified first: for a collection that does not
// exist the answer has to be "no collection" rather than "no sessions"; the two
// mean different things to the caller.
func (s *Service) ListPaymentSessions(ctx context.Context, collectionID string) ([]models.PaymentSession, error) {
	if err := requireText("payment_collection_id", collectionID); err != nil {
		return nil, err
	}
	if _, err := s.store.GetPaymentCollection(ctx, collectionID); err != nil {
		return nil, err
	}
	return s.store.ListPaymentSessionsByCollection(ctx, collectionID)
}

// ListPayments returns the collection's captures.
func (s *Service) ListPayments(ctx context.Context, collectionID string) ([]models.Payment, error) {
	if err := requireText("payment_collection_id", collectionID); err != nil {
		return nil, err
	}
	if _, err := s.store.GetPaymentCollection(ctx, collectionID); err != nil {
		return nil, err
	}
	return s.store.ListPaymentsByCollection(ctx, collectionID)
}

// GetPaymentSession returns the session by its identifier; errors.NotFound if
// there is none.
func (s *Service) GetPaymentSession(ctx context.Context, id string) (models.PaymentSession, error) {
	if err := requireText("id", id); err != nil {
		return models.PaymentSession{}, err
	}
	return s.store.GetPaymentSession(ctx, id)
}

// GetPayment returns the capture by its identifier; errors.NotFound if there is
// none.
func (s *Service) GetPayment(ctx context.Context, id string) (models.Payment, error) {
	if err := requireText("id", id); err != nil {
		return models.Payment{}, err
	}
	return s.store.GetPayment(ctx, id)
}

// ListRefunds returns the capture's refunds.
func (s *Service) ListRefunds(ctx context.Context, paymentID string) ([]models.Refund, error) {
	if err := requireText("payment_id", paymentID); err != nil {
		return nil, err
	}
	if _, err := s.store.GetPayment(ctx, paymentID); err != nil {
		return nil, err
	}
	return s.store.ListRefundsByPayment(ctx, paymentID)
}
