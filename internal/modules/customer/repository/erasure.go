package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// AnonymizeCustomers overwrites the named personal columns of every customer
// the subject resolves to, together with the customer's addresses, in ONE
// transaction.
//
// # Why a transaction and not two updates
//
// The transaction is modeled on [Repo.DeleteCustomer] and needed for a
// stronger reason than that one. A soft delete that stopped half way would
// leave a deleted customer with live addresses, which is untidy; an ERASURE
// that stops half way leaves the person's street and phone in
// customer_address while their customer row says they were forgotten — and the
// report that was already handed to them says the same. The write is atomic so
// that the answer given to the data subject cannot be partly false.
//
// # Why the rows are locked first
//
// The rows are read FOR UPDATE before they are written (see
// queries/customer.sql, LockCustomerForErasure). Without the lock a concurrent
// update — the customer changing their own name in the storefront — can land
// between the read and the write, and the anonymization would either lose that
// write or, worse, let it settle AFTER the erasure and leave the row personal
// again while the report says otherwise.
//
// # What the subject means
//
// BOTH identifiers select rows, and a subject carrying both selects the UNION
// of what each reaches; the service refuses a subject that carries neither.
// Treating the id as an override would be wrong on the most ordinary case
// there is: a registered customer who has also checked out as a guest under
// the same address has one row under their id and further rows under their
// e-mail, and all of them are the same person. Resolving by e-mail deliberately
// reaches SEVERAL rows — any number of guest records can share one address
// (customer_account_email_uniq covers accounts only).
//
// A subject that matches nothing is NOT an error. The erasure sweep asks every
// holder about the same person, and most holders do not have them; an absence
// answered with an error would turn a normal sweep into a failed one.
func (r *Repo) AnonymizeCustomers(
	ctx context.Context,
	customerID, email string,
	now time.Time,
) (models.ErasureCount, error) {
	var count models.ErasureCount

	err := r.inTx(ctx, func(q *customerdb.Queries) error {
		ids, err := lockErasureTargets(ctx, q, customerID, email)
		if err != nil {
			return err
		}

		for _, id := range ids {
			count.Matched++

			// The customer row is rewritten UNCONDITIONALLY; whether anything
			// was left to rewrite is decided by the UPDATE's own WHERE, against
			// the columns themselves. Deciding it here from the e-mail would
			// read one column to answer a question about four: an anonymized
			// record stays live and UpdateCustomer patches column by column, so
			// a fresh first_name and phone can be written afterwards without
			// the e-mail ever changing. A second erasure would then skip the
			// write and report the person's name as anonymized.
			written, updErr := q.AnonymizeCustomer(ctx, customerdb.AnonymizeCustomerParams{
				ID:        id,
				Email:     models.AnonymousEmail(id),
				UpdatedAt: fromTime(now),
			})
			if updErr != nil {
				return wrapDB(updErr, "customer could not be anonymized: %s", id)
			}
			count.Rewritten += int(written)

			// The addresses are rewritten on the same terms and for the same
			// reason. An anonymized record still EXISTS and is still live, so
			// the storefront can add an address to it after the erasure; a pass
			// that trusted the customer row alone would walk past that address
			// for ever.
			written, addrErr := q.AnonymizeAddressesOfCustomer(ctx, customerdb.AnonymizeAddressesOfCustomerParams{
				CustomerID:  id,
				Placeholder: models.AnonymousPlaceholder,
				UpdatedAt:   fromTime(now),
			})
			if addrErr != nil {
				return wrapDB(addrErr, "customer addresses could not be anonymized: %s", id)
			}
			count.Rewritten += int(written)
		}
		return nil
	})
	if err != nil {
		return models.ErasureCount{}, err
	}
	return count, nil
}

// lockErasureTargets resolves the subject to the ids of locked customer rows.
//
// The three paths are separate queries rather than one query with two optional
// conditions, and that is a planner decision as much as a readability one: the
// id-only path is a primary-key lookup, while a query whose conditions are
// switched off by a NULL parameter offers the planner no index it can trust for
// any of the three.
//
// # What the e-mail path costs
//
// The e-mail path is a SEQUENTIAL SCAN of customer plus a sort, and no index
// serves it. customer_email_idx is PARTIAL — it is built WHERE deleted_at IS
// NULL — and these queries deliberately carry no deleted_at filter, because a
// soft-deleted row still holds the person's e-mail, name and phone; the index
// cannot answer a query that reaches outside its own predicate.
//
// The scan is accepted rather than fixed. An unfiltered index on customer
// (email) would serve it, but it would be paid for by every insert and every
// e-mail update of the busiest table in the module, in exchange for a query
// that runs at most a handful of times per person per lifetime — an erasure
// request is rare, it is not on any request path a shopper waits on, and the
// sweep that issues it is already doing one round trip per holder. If that
// trade ever changes, the index is the fix and this paragraph is the record of
// why it was not taken today.
func lockErasureTargets(
	ctx context.Context,
	q *customerdb.Queries,
	customerID, email string,
) ([]string, error) {
	switch {
	case customerID != "" && email != "":
		// One query, so the rows a subject with both handles reaches are locked
		// in a single ascending id order — the same order the e-mail path uses.
		// Locking the id first and the e-mail rows afterwards would let two
		// concurrent requests take the same locks in opposite orders. A row
		// matching both conditions comes back once, so nothing is erased twice.
		ids, err := q.LockCustomersByIDOrEmailForErasure(ctx,
			customerdb.LockCustomersByIDOrEmailForErasureParams{ID: customerID, Email: email})
		if err != nil {
			return nil, wrapDB(err, "customers could not be locked for erasure: %s", customerID)
		}
		return ids, nil

	case customerID != "":
		id, err := q.LockCustomerForErasure(ctx, customerID)
		if err != nil {
			// A customer id this module does not know is an absence, not a
			// fault: the sweep hands every holder the same subject and most
			// holders have never seen the person.
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return nil, wrapDB(err, "customer could not be locked for erasure: %s", customerID)
		}
		return []string{id}, nil

	default:
		ids, err := q.LockCustomersByEmailForErasure(ctx, email)
		if err != nil {
			return nil, wrapDB(err, "customers could not be locked for erasure by e-mail")
		}
		return ids, nil
	}
}
