package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// importErrorCap is how many refused rows an import keeps the reason of.
const importErrorCap = 1000

// CreateImport writes an import with its file (ADR 0205).
func (r *Repo) CreateImport(ctx context.Context, id string, file []byte, rowsTotal int) (models.Import, error) {
	row, err := r.q.CreateImport(ctx, productdb.CreateImportParams{
		ID: id, File: file, RowsTotal: int32(min(rowsTotal, maxInt32)), //nolint:gosec // bounded above
	})
	if err != nil {
		return models.Import{}, wrapDB(err, "could not write the import")
	}

	return toImport(productdb.GetImportRow(row))
}

// GetImport reads an import without its file.
func (r *Repo) GetImport(ctx context.Context, id string) (models.Import, error) {
	row, err := r.q.GetImport(ctx, id)
	if err != nil {
		return models.Import{}, wrapDB(err, "import not found: %s", id)
	}

	return toImport(row)
}

// ClaimImport takes the oldest import with rows left, and reports false when
// there is none.
func (r *Repo) ClaimImport(ctx context.Context) (models.ClaimedImport, bool, error) {
	row, err := r.q.ClaimImport(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.ClaimedImport{}, false, nil
	}
	if err != nil {
		return models.ClaimedImport{}, false, wrapDB(err, "could not claim an import")
	}

	return models.ClaimedImport{
		ID: row.ID, File: row.File, RowsTotal: int(row.RowsTotal), RowsDone: int(row.RowsDone),
	}, true, nil
}

// RecordImportRow moves the import past the row at rowIndex. A row recorded
// already is not counted again: the statement matches only while rows_done is
// still rowIndex.
func (r *Repo) RecordImportRow(
	ctx context.Context, id string, rowIndex int, outcome models.ImportRowOutcome,
) error {
	var reason []byte
	if outcome.Error != nil {
		raw, err := json.Marshal(outcome.Error)
		if err != nil {
			return coreerrors.Wrap(err, coreerrors.KindInternal, codeDBFailed,
				"the error of row %d could not be encoded", outcome.Error.Row)
		}
		reason = raw
	}
	err := r.q.RecordImportRow(ctx, productdb.RecordImportRowParams{
		Created: flag(outcome.Created), Updated: flag(outcome.Updated), Failed: flag(outcome.Error != nil),
		Error: reason, ErrorCap: importErrorCap, ID: id,
		RowIndex: int32(min(rowIndex, maxInt32)), //nolint:gosec // bounded above
	})

	return wrapDB(err, "could not record row %d of import %s", rowIndex, id)
}

// FinishImport closes an import whose rows are all done and drops its file.
func (r *Repo) FinishImport(ctx context.Context, id string) error {
	return wrapDB(r.q.FinishImport(ctx, id), "could not finish import %s", id)
}

// maxInt32 bounds a count written to an INTEGER column.
const maxInt32 = 1<<31 - 1

// flag is a boolean as the 0 or 1 a counter is moved by.
func flag(set bool) int32 {
	if set {
		return 1
	}

	return 0
}

// toImport converts an import row.
func toImport(row productdb.GetImportRow) (models.Import, error) {
	var reasons []models.ImportError
	if err := json.Unmarshal(row.Errors, &reasons); err != nil {
		return models.Import{}, coreerrors.Wrap(err, coreerrors.KindInternal, codeDBFailed,
			"the errors of import %s could not be read", row.ID)
	}

	return models.Import{
		ID: row.ID, Status: models.ImportStatus(row.Status),
		RowsTotal: int(row.RowsTotal), RowsDone: int(row.RowsDone),
		RowsCreated: int(row.RowsCreated), RowsUpdated: int(row.RowsUpdated), RowsFailed: int(row.RowsFailed),
		Errors: reasons, CreatedAt: toTime(row.CreatedAt),
		StartedAt: toTimePtr(row.StartedAt), FinishedAt: toTimePtr(row.FinishedAt),
	}, nil
}
