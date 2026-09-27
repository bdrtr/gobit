package repository

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// ProductContent is what a restore writes back to the product's own row: every
// descriptive field, a nil one written as NULL (ADR 0221).
type ProductContent struct {
	Handle        string
	Title         string
	Subtitle      *string
	Description   *string
	Thumbnail     *string
	Discountable  bool
	Weight        *int32
	Length        *int32
	Height        *int32
	Width         *int32
	Material      *string
	OriginCountry *string
	CollectionID  *string
	TypeID        *string
	Metadata      map[string]any
}

// AppendProductRevision inserts a revision and stamps its version on the
// product; the caller holds the product's row lock.
func (r *Repo) AppendProductRevision(ctx context.Context, rev models.Revision) error {
	if err := r.q.InsertProductRevision(ctx, productdb.InsertProductRevisionParams{
		ID:         rev.ID,
		ProductID:  rev.ProductID,
		Version:    rev.Version,
		RecordedAt: pgtype.Timestamptz{Time: rev.RecordedAt, Valid: true},
		Changed:    rev.Changed,
		RequestID:  rev.RequestID,
		Snapshot:   rev.Snapshot,
	}); err != nil {
		return wrapDB(err, "could not record revision %d of product (%s)", rev.Version, rev.ProductID)
	}
	n, err := r.q.SetProductVersion(ctx, productdb.SetProductVersionParams{Version: rev.Version, ID: rev.ProductID})
	if err != nil {
		return wrapDB(err, "could not stamp version %d on product (%s)", rev.Version, rev.ProductID)
	}
	if n == 0 {
		return notFound("product", rev.ProductID)
	}
	return nil
}

// LatestProductRevision reads a product's newest revision; ok is false when it
// has none.
func (r *Repo) LatestProductRevision(ctx context.Context, productID string) (rev models.Revision, ok bool, err error) {
	row, err := r.q.GetLatestProductRevision(ctx, productID)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Revision{}, false, nil
	}
	if err != nil {
		return models.Revision{}, false, wrapDB(err, "could not read the latest revision of product (%s)", productID)
	}
	return toRevision(row), true, nil
}

// GetProductRevision reads one revision of a product with its snapshot.
func (r *Repo) GetProductRevision(ctx context.Context, productID string, version int64) (models.Revision, error) {
	row, err := r.q.GetProductRevision(ctx, productdb.GetProductRevisionParams{ProductID: productID, Version: version})
	if err != nil {
		return models.Revision{}, wrapDB(err, "product (%s) has no revision %d", productID, version)
	}
	return toRevision(row), nil
}

// ListProductRevisions reads a page of a product's revisions, newest first and
// without their snapshots, with how many it has.
func (r *Repo) ListProductRevisions(
	ctx context.Context, productID string, limit, offset int,
) ([]models.Revision, int64, error) {
	rows, err := r.q.ListProductRevisions(ctx, productdb.ListProductRevisionsParams{
		ProductID: productID, RowLimit: toInt32(limit), RowOffset: toInt32(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "could not list the revisions of product (%s)", productID)
	}
	count, err := r.q.CountProductRevisions(ctx, productID)
	if err != nil {
		return nil, 0, wrapDB(err, "could not count the revisions of product (%s)", productID)
	}
	out := make([]models.Revision, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.Revision{
			ID: row.ID, ProductID: row.ProductID, Version: row.Version, RecordedAt: toTime(row.RecordedAt),
			Changed: row.Changed, RequestID: row.RequestID,
		})
	}
	return out, count, nil
}

// RestoreProductContent writes a revision's descriptive fields back to the
// product's row.
func (r *Repo) RestoreProductContent(ctx context.Context, productID string, c ProductContent) error {
	meta, err := fromMetadata(c.Metadata)
	if err != nil {
		return err
	}
	n, err := r.q.RestoreProductContent(ctx, productdb.RestoreProductContentParams{
		Handle: c.Handle, Title: c.Title, Subtitle: c.Subtitle, Description: c.Description,
		Thumbnail: c.Thumbnail, Discountable: c.Discountable, Weight: c.Weight, Length: c.Length,
		Height: c.Height, Width: c.Width, Material: c.Material, OriginCountry: c.OriginCountry,
		CollectionID: c.CollectionID, TypeID: c.TypeID, Metadata: meta, ID: productID,
	})
	if err != nil {
		return wrapDB(err, "could not restore product (%s)", productID)
	}
	if n == 0 {
		return notFound("product", productID)
	}
	return nil
}

// toRevision turns a revision row into the model.
func toRevision(row productdb.ProductRevision) models.Revision {
	return models.Revision{
		ID: row.ID, ProductID: row.ProductID, Version: row.Version, RecordedAt: toTime(row.RecordedAt),
		Changed: row.Changed, RequestID: row.RequestID, Snapshot: json.RawMessage(row.Snapshot),
	}
}

// LiveTagIDs returns those of the given tags that are not removed.
func (r *Repo) LiveTagIDs(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	live, err := r.q.ListLiveTagIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "could not read which of %d tags stand", len(ids))
	}
	return live, nil
}

// LiveCategoryIDs returns those of the given categories that are not removed.
func (r *Repo) LiveCategoryIDs(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	live, err := r.q.ListLiveCategoryIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "could not read which of %d categories stand", len(ids))
	}
	return live, nil
}

// OptionValueProductID returns the product a live option value belongs to.
func (r *Repo) OptionValueProductID(ctx context.Context, valueID string) (string, error) {
	id, err := r.q.GetOptionValueProductID(ctx, valueID)
	if err != nil {
		return "", wrapDB(err, "option value not found: %s", valueID)
	}
	return id, nil
}
