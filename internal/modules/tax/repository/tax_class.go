package repository

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; the files beside it stay Turkish.

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/repository/taxdb"
)

// CreateTaxClass writes a class.
func (r *Repo) CreateTaxClass(
	ctx context.Context, class models.TaxClass, now time.Time,
) (models.TaxClass, error) {
	if err := r.ready(); err != nil {
		return models.TaxClass{}, err
	}

	metadata, err := fromJSONMap(class.Metadata)
	if err != nil {
		return models.TaxClass{}, err
	}

	row, err := r.queries(ctx).InsertTaxClass(ctx, taxdb.InsertTaxClassParams{
		ID:        class.ID,
		Name:      class.Name,
		Metadata:  metadata,
		CreatedAt: fromTime(now),
	})
	if err != nil {
		return models.TaxClass{}, wrapDB(err, "the tax class could not be written: %s", class.Name)
	}

	return toTaxClass(row)
}

// GetTaxClass reads a class by id.
func (r *Repo) GetTaxClass(ctx context.Context, id string) (models.TaxClass, error) {
	if err := r.ready(); err != nil {
		return models.TaxClass{}, err
	}

	row, err := r.queries(ctx).GetTaxClass(ctx, id)
	if err != nil {
		return models.TaxClass{}, notFoundOr(err, CodeTaxClassNotFound,
			"tax class not found: %s", id)
	}

	return toTaxClass(row)
}

// ListTaxClasses returns the live classes by name.
func (r *Repo) ListTaxClasses(ctx context.Context) ([]models.TaxClass, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.queries(ctx).ListTaxClasses(ctx)
	if err != nil {
		return nil, wrapDB(err, "the tax classes could not be listed")
	}

	return toTaxClasses(rows)
}

// DeleteTaxClass retires a class that holds no product.
//
// The count is read INSIDE the transaction, so a membership written while the
// delete is deciding is either seen by this read or blocked behind it. A class
// deleted with products still in it would leave those products matching a rule
// whose class nothing can name any more — the rule would go on applying and the
// operator would have no way to see why.
func (r *Repo) DeleteTaxClass(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return r.WithTx(ctx, func(ctx context.Context) error {
		q := r.queries(ctx)

		held, err := q.CountTaxClassMembers(ctx, id)
		if err != nil {
			return wrapDB(err, "the number of products in the class could not be read: %s", id)
		}
		if held > 0 {
			return errors.Conflict(CodeConstraintViolation,
				"class %s still holds %d product(s); take them out of it first", id, held)
		}

		if _, err := q.SoftDeleteTaxClass(ctx, taxdb.SoftDeleteTaxClassParams{
			ID: id, DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeTaxClassNotFound, "tax class not found: %s", id)
		}

		return nil
	})
}

// SetTaxClassMember binds a product to a class, MOVING it when it already
// belongs to another.
//
// The class is read inside the same transaction so a membership cannot be
// written against a class that is being deleted beside it.
func (r *Repo) SetTaxClassMember(
	ctx context.Context, member models.TaxClassMember, now time.Time,
) (models.TaxClassMember, error) {
	if err := r.ready(); err != nil {
		return models.TaxClassMember{}, err
	}

	var out models.TaxClassMember
	err := r.WithTx(ctx, func(ctx context.Context) error {
		q := r.queries(ctx)

		if _, err := q.GetTaxClass(ctx, member.TaxClassID); err != nil {
			return notFoundOr(err, CodeTaxClassNotFound,
				"tax class not found: %s", member.TaxClassID)
		}

		row, err := q.InsertTaxClassMember(ctx, taxdb.InsertTaxClassMemberParams{
			ID:         member.ID,
			TaxClassID: member.TaxClassID,
			ProductID:  member.ProductID,
			CreatedAt:  fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "the product could not be bound to the class: %s", member.ProductID)
		}

		out = toTaxClassMember(row)

		return nil
	})
	if err != nil {
		return models.TaxClassMember{}, err
	}

	return out, nil
}

// RemoveTaxClassMember takes the product out of whatever class it is in.
func (r *Repo) RemoveTaxClassMember(ctx context.Context, productID string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.queries(ctx).SoftDeleteTaxClassMember(ctx,
		taxdb.SoftDeleteTaxClassMemberParams{ProductID: productID, DeletedAt: fromTime(now)}); err != nil {
		return notFoundOr(err, CodeTaxClassNotFound,
			"the product is in no tax class: %s", productID)
	}

	return nil
}

// ListTaxClassMembers returns a class's products.
func (r *Repo) ListTaxClassMembers(
	ctx context.Context, classID string,
) ([]models.TaxClassMember, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.queries(ctx).ListTaxClassMembers(ctx, classID)
	if err != nil {
		return nil, wrapDB(err, "the products of the class could not be listed: %s", classID)
	}

	return toTaxClassMembers(rows), nil
}

// ClassesOfProducts resolves the class of every given product in ONE query.
//
// A product in no class is ABSENT from the map rather than present with an
// empty id: the query returns rows, not a census, and the caller reads a
// missing entry as "this line carries no class key".
func (r *Repo) ClassesOfProducts(
	ctx context.Context, productIDs []string,
) (map[string]string, error) {
	out := make(map[string]string, len(productIDs))
	if len(productIDs) == 0 {
		return out, nil
	}
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.queries(ctx).ListTaxClassMembersByProducts(ctx, productIDs)
	if err != nil {
		return nil, wrapDB(err, "the tax classes of the products could not be read")
	}

	for i := range rows {
		out[rows[i].ProductID] = rows[i].TaxClassID
	}

	return out, nil
}

// toTaxClass converts a row into the domain model.
func toTaxClass(row taxdb.TaxClass) (models.TaxClass, error) {
	metadata, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.TaxClass{}, err
	}

	return models.TaxClass{
		ID:        row.ID,
		Name:      row.Name,
		Metadata:  metadata,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}, nil
}

// toTaxClasses converts a row slice into domain models.
func toTaxClasses(rows []taxdb.TaxClass) ([]models.TaxClass, error) {
	out := make([]models.TaxClass, 0, len(rows))
	for i := range rows {
		class, err := toTaxClass(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, class)
	}

	return out, nil
}

// toTaxClassMember converts a row into the domain model.
func toTaxClassMember(row taxdb.TaxClassMember) models.TaxClassMember {
	return models.TaxClassMember{
		ID:         row.ID,
		TaxClassID: row.TaxClassID,
		ProductID:  row.ProductID,
		CreatedAt:  toTime(row.CreatedAt),
		UpdatedAt:  toTime(row.UpdatedAt),
		DeletedAt:  toTimePtr(row.DeletedAt),
	}
}

// toTaxClassMembers converts a row slice into domain models.
func toTaxClassMembers(rows []taxdb.TaxClassMember) []models.TaxClassMember {
	out := make([]models.TaxClassMember, 0, len(rows))
	for i := range rows {
		out = append(out, toTaxClassMember(rows[i]))
	}

	return out
}
