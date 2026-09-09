package service

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; the files beside it stay Turkish.

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// maxClassNameLen bounds the class name.
//
// It is the same ceiling the module puts on a rate's name: the two are the same
// kind of thing — a word an operator types to pick a record — and a second
// number would be a second answer to one question.
const maxClassNameLen = 120

// CreateTaxClassInput is the write input of a tax class.
type CreateTaxClassInput struct {
	// Name is what the operator picks the class by; it is REQUIRED and no two
	// live classes may share one.
	Name string
	// Metadata is the caller's free extra data.
	Metadata map[string]any
}

// CreateTaxClass opens a class: a set of products taxed the same way.
//
// # What a class is for
//
// A rule could name ONE product or a type the catalog does not have, so a shop
// taxing books at one rate and electronics at another had to write one rule per
// product and rewrite them as the catalog grew. A class is the merchant's own
// word for the set, and a rule written against it keeps applying to every
// product they put in it.
//
// # Why the name is unique among LIVE classes
//
// The operator picks a class by name, so two live classes sharing one would
// make that choice ambiguous. A retired class keeps its name out of the way:
// the index is partial, exactly like the default rate's one table over.
func (s *Service) CreateTaxClass(
	ctx context.Context, in CreateTaxClassInput,
) (models.TaxClass, error) {
	if err := s.ready(); err != nil {
		return models.TaxClass{}, err
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		return models.TaxClass{}, errors.Invalid(CodeInvalidInput,
			"the tax class name is required")
	}
	if len(name) > maxClassNameLen {
		return models.TaxClass{}, errors.Invalid(CodeInvalidInput,
			"the tax class name is at most %d characters, %d given",
			maxClassNameLen, len(name))
	}

	now := s.clock()

	return s.repo.CreateTaxClass(ctx, models.TaxClass{
		ID:       models.NewTaxClassID(now),
		Name:     name,
		Metadata: in.Metadata,
	}, now)
}

// GetTaxClass returns the class by its id.
func (s *Service) GetTaxClass(ctx context.Context, id string) (models.TaxClass, error) {
	if err := s.ready(); err != nil {
		return models.TaxClass{}, err
	}
	if err := requireID(id, models.TaxClassIDPrefix, "tax class id"); err != nil {
		return models.TaxClass{}, err
	}

	return s.repo.GetTaxClass(ctx, id)
}

// ListTaxClasses returns the live classes, by name.
//
// There is no paging: a shop's tax classes are counted in tens — they are the
// merchant's own vocabulary, not their catalog — and a cursor nobody advances
// is a contract kept for nothing.
func (s *Service) ListTaxClasses(ctx context.Context) ([]models.TaxClass, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}

	return s.repo.ListTaxClasses(ctx)
}

// DeleteTaxClass retires a class that holds no product.
//
// A class deleted with products still in it would leave those products matching
// a rule whose class nothing can name any more: the rule would go on applying
// and the operator would have no way to see why. The refusal is decided under
// the delete's own transaction in the repository.
func (s *Service) DeleteTaxClass(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.TaxClassIDPrefix, "tax class id"); err != nil {
		return err
	}

	return s.repo.DeleteTaxClass(ctx, id, s.clock())
}

// SetProductTaxClass puts a product in a class, MOVING it out of any other.
//
// # Why moving rather than refusing
//
// A product is in at most one class, and reclassifying one is the ordinary
// thing an operator does. Refusing the second write would make them delete the
// first membership for no reason, and the two calls would leave a window in
// which the product is in no class at all.
//
// # The product id is NOT verified
//
// It belongs to the catalog and this module knows no catalog record (Principle
// 2.2), exactly as a rule's reference id is not verified. A membership written
// for an id that does not exist is harmless: no line carrying it ever enters a
// calculation.
func (s *Service) SetProductTaxClass(
	ctx context.Context, classID, productID string,
) (models.TaxClassMember, error) {
	if err := s.ready(); err != nil {
		return models.TaxClassMember{}, err
	}
	if err := requireID(classID, models.TaxClassIDPrefix, "tax class id"); err != nil {
		return models.TaxClassMember{}, err
	}
	if err := requireReferenceID(productID); err != nil {
		return models.TaxClassMember{}, err
	}

	now := s.clock()

	return s.repo.SetTaxClassMember(ctx, models.TaxClassMember{
		ID:         models.NewTaxClassMemberID(now),
		TaxClassID: classID,
		ProductID:  productID,
	}, now)
}

// RemoveProductTaxClass takes the product out of whatever class it is in.
//
// The class is not named: a product is in at most one, so naming it would be a
// second way to say the same thing and a way to get it wrong.
func (s *Service) RemoveProductTaxClass(ctx context.Context, productID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireReferenceID(productID); err != nil {
		return err
	}

	return s.repo.RemoveTaxClassMember(ctx, productID, s.clock())
}

// ListTaxClassMembers returns the products of a class.
func (s *Service) ListTaxClassMembers(
	ctx context.Context, classID string,
) ([]models.TaxClassMember, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(classID, models.TaxClassIDPrefix, "tax class id"); err != nil {
		return nil, err
	}

	return s.repo.ListTaxClassMembers(ctx, classID)
}
