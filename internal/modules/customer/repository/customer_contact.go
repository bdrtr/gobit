package repository

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// ReviseContact writes the customer's name and phone while they are still the
// ones the caller read, and reports whether it did (ADR 0337); a customer who
// moved since, or who is not there, is left as they are.
func (r *Repo) ReviseContact(
	ctx context.Context, id string, read, next models.ContactTerms, now time.Time,
) (models.Customer, bool, error) {
	if err := r.ready(); err != nil {
		return models.Customer{}, false, err
	}

	row, err := r.q.ReviseCustomerContact(ctx, customerdb.ReviseCustomerContactParams{
		ID: id, FirstName: next.FirstName, LastName: next.LastName, Phone: next.Phone, UpdatedAt: fromTime(now),
		ReadFirstName: read.FirstName, ReadLastName: read.LastName, ReadPhone: read.Phone,
	})
	switch {
	case stderrors.Is(err, pgx.ErrNoRows):
		return models.Customer{}, false, nil
	case err != nil:
		return models.Customer{}, false, wrapDB(err, "customer %s could not be revised", id)
	}
	customer, err := toCustomer(row)

	return customer, err == nil, err
}
