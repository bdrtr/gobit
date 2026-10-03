package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// CodeInvalidDefaultKind reports an undefined kind of default flag.
const CodeInvalidDefaultKind = "customer_invalid_default_kind"

// CreateAddress writes a new address for the customer.
//
// If the address itself is to be marked as a default, the customer's old
// default OF THAT KIND is cleared first; both happen in one transaction. The
// lock order is the same in EVERY flow — the customer row first, then the
// addresses (see [Repo.SetDefaultAddress]).
//
// The customer's existence is verified together with the lock: had only the
// foreign key been relied on, a missing customer would have reached the client
// as a 422 instead of a 404.
func (r *Repo) CreateAddress(ctx context.Context, a models.CustomerAddress) (models.CustomerAddress, error) {
	var out models.CustomerAddress

	err := r.inTx(ctx, func(q *customerdb.Queries) error {
		if _, err := q.GetCustomerForUpdate(ctx, a.CustomerID); err != nil {
			return notFoundOr(err, CodeCustomerNotFound, "customer not found: %s", a.CustomerID)
		}

		if err := clearDefaults(ctx, q, a.CustomerID, a.IsDefaultShipping, a.IsDefaultBilling, a.CreatedAt); err != nil {
			return err
		}

		row, err := q.InsertCustomerAddress(ctx, customerdb.InsertCustomerAddressParams{
			ID:                a.ID,
			CustomerID:        a.CustomerID,
			FirstName:         a.FirstName,
			LastName:          a.LastName,
			Company:           a.Company,
			Address1:          a.Address1,
			Address2:          a.Address2,
			City:              a.City,
			Province:          a.Province,
			CountryCode:       a.CountryCode,
			PostalCode:        a.PostalCode,
			Phone:             a.Phone,
			IsDefaultShipping: a.IsDefaultShipping,
			IsDefaultBilling:  a.IsDefaultBilling,
			CreatedAt:         fromTime(a.CreatedAt),
		})
		if err != nil {
			return wrapDB(err, "the customer address could not be created")
		}
		out = toAddress(row)
		return nil
	})
	if err != nil {
		return models.CustomerAddress{}, err
	}
	return out, nil
}

// GetAddress returns the address by its id AND ITS OWNER; errors.NotFound if
// it does not exist.
func (r *Repo) GetAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error) {
	if err := r.ready(); err != nil {
		return models.CustomerAddress{}, err
	}

	row, err := r.q.GetCustomerAddress(ctx, customerdb.GetCustomerAddressParams{
		ID:         addressID,
		CustomerID: customerID,
	})
	if err != nil {
		return models.CustomerAddress{}, notFoundOr(err, CodeAddressNotFound,
			"customer address not found: %s", addressID)
	}
	return toAddress(row), nil
}

// ListAddresses returns the customer's addresses.
func (r *Repo) ListAddresses(ctx context.Context, customerID string) ([]models.CustomerAddress, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListCustomerAddresses(ctx, customerID)
	if err != nil {
		return nil, wrapDB(err, "the customer's addresses could not be read: %s", customerID)
	}

	out := make([]models.CustomerAddress, 0, len(rows))
	for i := range rows {
		out = append(out, toAddress(rows[i]))
	}
	return out, nil
}

// UpdateAddress updates the given fields of the address; errors.NotFound if it
// does not exist.
//
// The default flags CANNOT be changed here: a flag concerns the customer's
// other addresses too, so it cannot be set with a single-row update (see
// [Repo.SetDefaultAddress]).
func (r *Repo) UpdateAddress(
	ctx context.Context,
	customerID, addressID string,
	patch models.AddressPatch,
	now time.Time,
) (models.CustomerAddress, error) {
	if err := r.ready(); err != nil {
		return models.CustomerAddress{}, err
	}

	row, err := r.q.UpdateCustomerAddress(ctx, customerdb.UpdateCustomerAddressParams{
		ID:          addressID,
		CustomerID:  customerID,
		FirstName:   patch.FirstName,
		LastName:    patch.LastName,
		Company:     patch.Company,
		Address1:    patch.Address1,
		Address2:    patch.Address2,
		City:        patch.City,
		Province:    patch.Province,
		CountryCode: patch.CountryCode,
		PostalCode:  patch.PostalCode,
		Phone:       patch.Phone,
		UpdatedAt:   fromTime(now),
	})
	if err != nil {
		return models.CustomerAddress{}, notFoundOr(err, CodeAddressNotFound,
			"customer address not found: %s", addressID)
	}
	return toAddress(row), nil
}

// DeleteAddress soft-deletes the address; errors.NotFound if it does not
// exist.
//
// The default flag the address carries needs no separate clearing: the partial
// unique indexes are defined with the condition deleted_at IS NULL, so the
// deleted row leaves the index's scope and the customer can assign a new
// default.
func (r *Repo) DeleteAddress(ctx context.Context, customerID, addressID string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeleteCustomerAddress(ctx, customerdb.SoftDeleteCustomerAddressParams{
		ID:         addressID,
		CustomerID: customerID,
		DeletedAt:  fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodeAddressNotFound, "customer address not found: %s", addressID)
	}
	return nil
}

// SetDefaultAddress makes the address the customer's default shipping or
// billing address.
//
// # Lock order
//
// The transaction ALWAYS locks the customer row first and only then touches the
// address rows. Because the order is fixed, two concurrent assignments for the
// same customer wait for each other and run one after the other; were there a
// flow taking the locks in the opposite order, the database would kill one of
// the transactions with a deadlock.
//
// # Why clearing is not enough
//
// The "clear the old one, mark the new one" step is NOT on its own the source
// of correctness; the constraint is the partial unique index that allows one
// flagged row per customer. The clearing step is only the way to satisfy that
// constraint. The lock and the index work together: the lock serializes the
// race, and the index rejects a second flag on any path where the lock was
// skipped or set up wrongly.
func (r *Repo) SetDefaultAddress(
	ctx context.Context,
	customerID, addressID string,
	kind models.DefaultKind,
	now time.Time,
) (models.CustomerAddress, error) {
	if !kind.Valid() {
		return models.CustomerAddress{}, errors.Invalid(CodeInvalidDefaultKind,
			"undefined kind of default flag: %d", uint8(kind))
	}

	var out models.CustomerAddress

	err := r.inTx(ctx, func(q *customerdb.Queries) error {
		if _, err := q.GetCustomerForUpdate(ctx, customerID); err != nil {
			return notFoundOr(err, CodeCustomerNotFound, "customer not found: %s", customerID)
		}
		// The address's ownership and liveness are verified BEFORE marking;
		// otherwise the old default would be cleared, the new one could never
		// be marked, and the customer would be left with no default.
		if _, err := q.GetCustomerAddress(ctx, customerdb.GetCustomerAddressParams{
			ID:         addressID,
			CustomerID: customerID,
		}); err != nil {
			return notFoundOr(err, CodeAddressNotFound, "customer address not found: %s", addressID)
		}

		if err := clearDefaults(ctx, q, customerID,
			kind == models.DefaultShipping, kind == models.DefaultBilling, now); err != nil {
			return err
		}

		var (
			row customerdb.CustomerAddress
			err error
		)
		if kind == models.DefaultShipping {
			row, err = q.MarkDefaultShipping(ctx, customerdb.MarkDefaultShippingParams{
				ID: addressID, CustomerID: customerID, UpdatedAt: fromTime(now),
			})
		} else {
			row, err = q.MarkDefaultBilling(ctx, customerdb.MarkDefaultBillingParams{
				ID: addressID, CustomerID: customerID, UpdatedAt: fromTime(now),
			})
		}
		if err != nil {
			return notFoundOr(err, CodeAddressNotFound, "the customer address could not be made the default: %s", addressID)
		}

		out = toAddress(row)
		return nil
	})
	if err != nil {
		return models.CustomerAddress{}, err
	}
	return out, nil
}

// clearDefaults removes the default flags of the requested kinds.
//
// The caller is INSIDE a transaction and has already locked the customer row,
// so no lock is taken again here.
func clearDefaults(
	ctx context.Context,
	q *customerdb.Queries,
	customerID string,
	shipping, billing bool,
	now time.Time,
) error {
	if shipping {
		if err := q.ClearDefaultShipping(ctx, customerdb.ClearDefaultShippingParams{
			CustomerID: customerID, UpdatedAt: fromTime(now),
		}); err != nil {
			return wrapDB(err, "the default shipping address could not be cleared: %s", customerID)
		}
	}
	if billing {
		if err := q.ClearDefaultBilling(ctx, customerdb.ClearDefaultBillingParams{
			CustomerID: customerID, UpdatedAt: fromTime(now),
		}); err != nil {
			return wrapDB(err, "the default billing address could not be cleared: %s", customerID)
		}
	}
	return nil
}

// toAddress converts a generated row into the domain model.
func toAddress(row customerdb.CustomerAddress) models.CustomerAddress {
	return models.CustomerAddress{
		ID:                row.ID,
		CustomerID:        row.CustomerID,
		FirstName:         row.FirstName,
		LastName:          row.LastName,
		Company:           row.Company,
		Address1:          row.Address1,
		Address2:          row.Address2,
		City:              row.City,
		Province:          row.Province,
		CountryCode:       row.CountryCode,
		PostalCode:        row.PostalCode,
		Phone:             row.Phone,
		IsDefaultShipping: row.IsDefaultShipping,
		IsDefaultBilling:  row.IsDefaultBilling,
		CreatedAt:         toTime(row.CreatedAt),
		UpdatedAt:         toTime(row.UpdatedAt),
		DeletedAt:         toTimePtr(row.DeletedAt),
	}
}
