package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// CustomersForDisclosure reads every customer row the subject resolves to.
//
// # Why it is not AnonymizeCustomers' resolver
//
// The subject is resolved on exactly the terms [lockErasureTargets] uses —
// three paths, both handles, the union of what each reaches, no deleted_at
// filter — and the two are deliberately kept as twins rather than merged into
// one function. The erasure's resolver takes a row lock inside a transaction and
// returns only ids, because everything it returns is about to be overwritten;
// this one takes no lock, opens no transaction and returns whole rows, because
// nothing here is overwritten. Sharing the code would mean either a read that
// locks or a write that does not, and both are worse than the duplication of
// three short SELECTs.
//
// The pairing is not left to a reader's memory: every claim below is a claim
// about what the erasure does too, and the queries carry the same argument in
// queries/customer.sql.
//
// # Why the three paths stay three
//
// A single (id = $1 OR email = $2) query would answer all three, and it would
// make the commonest admin request — a disclosure by customer id — a sequential
// scan instead of a primary-key lookup. The e-mail paths scan and that is
// accepted for the reason written on [lockErasureTargets]: customer_email_idx is
// PARTIAL, built WHERE deleted_at IS NULL, and these queries deliberately reach
// outside that predicate, so no index can serve them; a disclosure runs a
// handful of times per person per lifetime and sits on no path a shopper waits
// on. There is no reason to make the one path that can use an index give it up
// as well.
//
// # Why the deleted rows come back
//
// A soft delete writes deleted_at and updated_at and clears not one personal
// column, so a deleted customer's e-mail, name and phone are still in the table.
// The question a disclosure answers is what the database HOLDS, not what its
// listing screens show; filtering here would hand a person a file that omits the
// row their data is actually sitting in.
//
// A subject that matches nothing is NOT an error, for the reason the erasure
// gives: the sweep hands every holder the same subject and most holders have
// never seen the person.
func (r *Repo) CustomersForDisclosure(
	ctx context.Context,
	customerID, email string,
) ([]models.Customer, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	switch {
	case customerID != "" && email != "":
		// One query rather than two results merged in Go. A row satisfying both
		// conditions comes back ONCE, so nothing has to be de-duplicated — and
		// a de-duplication that was forgotten would show a person the same
		// record twice in their own file.
		rows, err := r.q.ListCustomersByIDOrEmailForDisclosure(ctx,
			customerdb.ListCustomersByIDOrEmailForDisclosureParams{ID: customerID, Email: email})
		if err != nil {
			return nil, wrapDB(err, "customers could not be read for disclosure: %s", customerID)
		}

		return toCustomers(rows)

	case customerID != "":
		row, err := r.q.GetCustomerForDisclosure(ctx, customerID)
		if err != nil {
			// A customer id this module does not know is an absence and not a
			// fault; the service turns it into the Nothing state, which is an
			// answer rather than a failure.
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}

			return nil, wrapDB(err, "customer could not be read for disclosure: %s", customerID)
		}

		customer, err := toCustomer(row)
		if err != nil {
			return nil, err
		}

		return []models.Customer{customer}, nil

	default:
		rows, err := r.q.ListCustomersByEmailForDisclosure(ctx, email)
		if err != nil {
			return nil, wrapDB(err, "customers could not be read for disclosure by e-mail")
		}

		return toCustomers(rows)
	}
}

// AddressesForDisclosure reads every address saved on the given customers.
//
// The ids arrive as a slice and the rows come back in ONE query, because a
// subject resolved by e-mail can reach any number of guest records and a query
// per record would tie the cost of a person's file to how often they have
// shopped.
//
// It carries no deleted_at filter for the reason above: a soft-deleted address
// row still holds the street, the door number and the phone. That also means
// customer_address_customer_idx cannot serve it — the index is partial, built
// WHERE deleted_at IS NULL — and the scan is accepted on the same trade as the
// e-mail path.
//
// An empty id list issues no query at all. Postgres would answer `= ANY('{}')`
// with an empty result perfectly well; the round trip is skipped because the
// caller only reaches this method after finding customers, so an empty list
// here would mean the caller had a bug, and a query that quietly answers
// "nothing" is the kind that lets one live.
func (r *Repo) AddressesForDisclosure(
	ctx context.Context,
	customerIDs []string,
) ([]models.CustomerAddress, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	if len(customerIDs) == 0 {
		return []models.CustomerAddress{}, nil
	}

	rows, err := r.q.ListAddressesForDisclosure(ctx, customerIDs)
	if err != nil {
		return nil, wrapDB(err, "customer addresses could not be read for disclosure")
	}

	out := make([]models.CustomerAddress, 0, len(rows))
	for i := range rows {
		out = append(out, toAddress(rows[i]))
	}

	return out, nil
}
