package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// CodeContactRevised refuses a correction of a customer's name and phone when
// another writer changed them since the caller read them (ADR 0337).
const CodeContactRevised = "customer_contact_revised"

// ReviseContact corrects the customer's name and phone, writing them only
// while they are the ones the caller read, and refuses with
// [CodeContactRevised] when another writer changed any of them since (ADR
// 0337). The fields are trimmed and checked as on a new customer; the e-mail
// is not among them.
func (s *Service) ReviseContact(
	ctx context.Context, id string, read, next models.ContactTerms,
) (models.Customer, error) {
	if err := s.ready(); err != nil {
		return models.Customer{}, err
	}
	if err := requireID(id, models.CustomerIDPrefix, "customer id"); err != nil {
		return models.Customer{}, err
	}
	next = models.ContactTerms{
		FirstName: strings.TrimSpace(next.FirstName), LastName: strings.TrimSpace(next.LastName),
		Phone: strings.TrimSpace(next.Phone),
	}
	if err := validatePerson(next.FirstName, next.LastName, next.Phone); err != nil {
		return models.Customer{}, err
	}

	customer, revised, err := s.repo.ReviseContact(ctx, id, read, next, s.clock())
	if err != nil || revised {
		return customer, err
	}

	// Nothing was written: the customer is gone, or was corrected since.
	current, err := s.repo.GetCustomer(ctx, id)
	if err != nil {
		return models.Customer{}, err
	}

	return models.Customer{}, errors.Conflict(CodeContactRevised,
		"customer %s was corrected since it was read: the name is %q and the phone %q now; draw the page again",
		id, strings.TrimSpace(current.FirstName+" "+current.LastName), current.Phone)
}
