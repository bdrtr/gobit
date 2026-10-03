package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// The error codes of the guest-to-account conversion.
const (
	// CodeAlreadyAccount reports that the record is already an account.
	CodeAlreadyAccount = "customer_already_account"
	// CodeEmailTaken reports that the e-mail is in use by another account.
	CodeEmailTaken = "customer_email_taken"
)

// CreateCustomer writes a new customer.
//
// If the e-mail of a registered account is already in use, errors.Conflict is
// returned; the rule lives in the partial unique index in the database (see
// [IndexAccountEmail]) and is not repeated in the application — had it been,
// the race between two concurrent registrations would still be settled by the
// index.
func (r *Repo) CreateCustomer(ctx context.Context, c models.Customer) (models.Customer, error) {
	if err := r.ready(); err != nil {
		return models.Customer{}, err
	}

	meta, err := fromMetadata(c.Metadata)
	if err != nil {
		return models.Customer{}, err
	}

	row, err := r.q.InsertCustomer(ctx, customerdb.InsertCustomerParams{
		ID:         c.ID,
		Email:      c.Email,
		FirstName:  c.FirstName,
		LastName:   c.LastName,
		Phone:      c.Phone,
		HasAccount: c.HasAccount,
		Metadata:   meta,
		CreatedAt:  fromTime(c.CreatedAt),
	})
	if err != nil {
		if ConstraintName(err) == IndexAccountEmail {
			return models.Customer{}, errors.Wrap(err, errors.KindConflict, CodeEmailTaken,
				"an account registered with the e-mail %q already exists", c.Email)
		}
		return models.Customer{}, wrapDB(err, "the customer could not be created")
	}
	return toCustomer(row)
}

// GetCustomer returns the customer by id; errors.NotFound if it does not
// exist.
func (r *Repo) GetCustomer(ctx context.Context, id string) (models.Customer, error) {
	if err := r.ready(); err != nil {
		return models.Customer{}, err
	}

	row, err := r.q.GetCustomer(ctx, id)
	if err != nil {
		return models.Customer{}, notFoundOr(err, CodeCustomerNotFound, "customer not found: %s", id)
	}
	return toCustomer(row)
}

// GetAccountByEmail returns the REGISTERED account by e-mail; errors.NotFound
// if it does not exist.
//
// Guest records are left out on purpose: since more than one guest can share
// an e-mail, the question "the one customer with this e-mail" has no single
// right answer among guests (see models.Customer).
func (r *Repo) GetAccountByEmail(ctx context.Context, email string) (models.Customer, error) {
	if err := r.ready(); err != nil {
		return models.Customer{}, err
	}

	row, err := r.q.GetAccountByEmail(ctx, email)
	if err != nil {
		return models.Customer{}, notFoundOr(err, CodeCustomerNotFound,
			"no account registered with the e-mail %q was found", email)
	}
	return toCustomer(row)
}

// ListCustomers returns the filtered, paged list of customers together with
// the TOTAL number of records that match the filter.
func (r *Repo) ListCustomers(
	ctx context.Context,
	filter models.CustomerFilter,
	limit, offset int64,
) ([]models.Customer, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	// The cursor arrives as SQL NULL when it names no position; the COALESCE
	// sentinels in the query turn that into "start at the top". Sending a zero
	// TIME instead would make the first page come back empty with no error
	// anywhere.
	afterAt := pgtype.Timestamptz{}
	if !filter.After.Time.IsZero() {
		afterAt = pgtype.Timestamptz{Time: filter.After.Time, Valid: true}
	}

	var afterID *string
	if filter.After.ID != "" {
		afterID = &filter.After.ID
	}

	rows, err := r.q.ListCustomers(ctx, customerdb.ListCustomersParams{
		Email:      filter.Email,
		HasAccount: filter.HasAccount,
		GroupID:    filter.GroupID,
		Lim:        toInt32(limit),
		Off:        toInt32(offset),
		AfterAt:    afterAt,
		AfterID:    afterID,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "the customer list could not be read")
	}

	total, err := r.q.CountCustomers(ctx, customerdb.CountCustomersParams{
		Email:      filter.Email,
		HasAccount: filter.HasAccount,
		GroupID:    filter.GroupID,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "the customers could not be counted")
	}

	customers, err := toCustomers(rows)
	if err != nil {
		return nil, 0, err
	}
	return customers, total, nil
}

// GetCustomersByIDs returns the customers that match the given ids in ONE
// query. No record comes back for an id that is not found, and that is not an
// error (the Query layer's FetchByIDs contract, ADR 0004).
func (r *Repo) GetCustomersByIDs(ctx context.Context, ids []string) ([]models.Customer, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.Customer{}, nil
	}

	rows, err := r.q.ListCustomersByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "the customers could not be read")
	}
	return toCustomers(rows)
}

// UpdateCustomer updates the given fields of the customer; errors.NotFound if
// it does not exist.
func (r *Repo) UpdateCustomer(
	ctx context.Context,
	id string,
	patch models.CustomerPatch,
	now time.Time,
) (models.Customer, error) {
	if err := r.ready(); err != nil {
		return models.Customer{}, err
	}

	meta, err := patchMetadata(patch.Metadata)
	if err != nil {
		return models.Customer{}, err
	}

	row, err := r.q.UpdateCustomer(ctx, customerdb.UpdateCustomerParams{
		ID:        id,
		Email:     patch.Email,
		FirstName: patch.FirstName,
		LastName:  patch.LastName,
		Phone:     patch.Phone,
		Metadata:  meta,
		UpdatedAt: fromTime(now),
	})
	if err != nil {
		if ConstraintName(err) == IndexAccountEmail {
			return models.Customer{}, errors.Wrap(err, errors.KindConflict, CodeEmailTaken,
				"the e-mail is in use by another account")
		}
		return models.Customer{}, notFoundOr(err, CodeCustomerNotFound, "customer not found: %s", id)
	}
	return toCustomer(row)
}

// PromoteGuest turns a guest record into an account.
//
// Three outcomes are reported separately, and all of them are decided in ONE
// transaction while the customer row is LOCKED:
//
//   - No record: errors.NotFound.
//   - The record is already an account: errors.Conflict
//     ([CodeAlreadyAccount]).
//   - The e-mail belongs to another account: errors.Conflict
//     ([CodeEmailTaken]).
//
// Even though the pre-check is made under the lock, the partial unique index
// remains the LAST gate: another transaction that slips in between the check
// and the update (one opening a new account with the same e-mail) is caught
// only by the index. That is why the uniqueness violation is translated into
// the same code too; the caller does not have to tell the two paths apart.
func (r *Repo) PromoteGuest(ctx context.Context, id string, now time.Time) (models.Customer, error) {
	var out models.Customer

	err := r.inTx(ctx, func(q *customerdb.Queries) error {
		current, err := q.GetCustomerForUpdate(ctx, id)
		if err != nil {
			return notFoundOr(err, CodeCustomerNotFound, "customer not found: %s", id)
		}
		if current.HasAccount {
			return errors.Conflict(CodeAlreadyAccount, "the customer already has an account: %s", id)
		}

		taken, err := q.AccountEmailTakenByOther(ctx, customerdb.AccountEmailTakenByOtherParams{
			Email: current.Email,
			ID:    id,
		})
		if err != nil {
			return wrapDB(err, "the e-mail conflict could not be checked")
		}
		if taken {
			return errors.Conflict(CodeEmailTaken,
				"an account registered with the e-mail %q already exists", current.Email)
		}

		row, err := q.PromoteCustomerToAccount(ctx, customerdb.PromoteCustomerToAccountParams{
			ID:        id,
			UpdatedAt: fromTime(now),
		})
		if err != nil {
			if ConstraintName(err) == IndexAccountEmail {
				return errors.Wrap(err, errors.KindConflict, CodeEmailTaken,
					"an account registered with the e-mail %q already exists", current.Email)
			}
			return notFoundOr(err, CodeCustomerNotFound, "the customer could not be turned into an account: %s", id)
		}

		out, err = toCustomer(row)
		return err
	})
	if err != nil {
		return models.Customer{}, err
	}
	return out, nil
}

// DeleteCustomer soft-deletes the customer and their addresses;
// errors.NotFound if the customer does not exist.
//
// The addresses are deleted in the SAME transaction: the foreign key's ON
// DELETE CASCADE runs only on a real delete, and since a soft delete is an
// UPDATE it does not take the addresses with it by itself. Group memberships,
// on the other hand, are LEFT in place — a deleted customer shows up in no list
// anyway, and the membership row goes by cascade the day the record is really
// deleted.
func (r *Repo) DeleteCustomer(ctx context.Context, id string, now time.Time) error {
	return r.inTx(ctx, func(q *customerdb.Queries) error {
		if _, err := q.SoftDeleteCustomer(ctx, customerdb.SoftDeleteCustomerParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeCustomerNotFound, "customer not found: %s", id)
		}

		if err := q.SoftDeleteAddressesOfCustomer(ctx, customerdb.SoftDeleteAddressesOfCustomerParams{
			CustomerID: id,
			DeletedAt:  fromTime(now),
		}); err != nil {
			return wrapDB(err, "the customer's addresses could not be deleted: %s", id)
		}
		return nil
	})
}

// toCustomer converts a generated row into the domain model.
func toCustomer(row customerdb.Customer) (models.Customer, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.Customer{}, err
	}
	return models.Customer{
		ID:         row.ID,
		Email:      row.Email,
		FirstName:  row.FirstName,
		LastName:   row.LastName,
		Phone:      row.Phone,
		HasAccount: row.HasAccount,
		Metadata:   meta,
		CreatedAt:  toTime(row.CreatedAt),
		UpdatedAt:  toTime(row.UpdatedAt),
		DeletedAt:  toTimePtr(row.DeletedAt),
	}, nil
}

// toCustomers converts a slice of rows into domain models.
func toCustomers(rows []customerdb.Customer) ([]models.Customer, error) {
	out := make([]models.Customer, 0, len(rows))
	for i := range rows {
		c, err := toCustomer(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}
