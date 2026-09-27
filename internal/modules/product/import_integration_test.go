//go:build integration

package product_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// TestAnImportMovesOnTheRealSchema is ADR 0205's record on the real schema:
// claimed by one run, moved a row at a time, a row recorded twice counted once,
// and its file dropped when it ends.
func TestAnImportMovesOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, link.New(testPool, nil), nil)
	repo := repository.New(testPool.Pool())

	// Imports left by other tests would be claimed first; this one works
	// through whatever is open before its own.
	for {
		n, err := svc.ApplyImports(ctx, time.Now().Add(time.Minute))
		require.NoError(t, err)
		if n == 0 {
			break
		}
	}

	created, err := svc.CreateImport(ctx, []byte("product_handle,product_title\nimp-a-"+
		time.Now().Format("150405.000000")+",A\nimp-b-"+time.Now().Format("150405.000000")+",B\n"))
	require.NoError(t, err)
	assert.Equal(t, models.ImportPending, created.Status)
	assert.Equal(t, 2, created.RowsTotal)

	claimed, ok, err := repo.ClaimImport(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, created.ID, claimed.ID)
	assert.NotEmpty(t, claimed.File)

	require.NoError(t, repo.RecordImportRow(ctx, created.ID, 0, models.ImportRowOutcome{Created: true}))
	require.NoError(t, repo.RecordImportRow(ctx, created.ID, 0, models.ImportRowOutcome{Created: true}),
		"a row recorded twice is not an error")
	halfway, err := svc.GetImport(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, halfway.RowsDone, "and it is counted once")
	assert.Equal(t, 1, halfway.RowsCreated)
	assert.Equal(t, models.ImportRunning, halfway.Status)
	assert.NotNil(t, halfway.StartedAt)

	require.NoError(t, repo.RecordImportRow(ctx, created.ID, 1, models.ImportRowOutcome{
		Error: &models.ImportError{Row: 3, Message: "refused"},
	}))
	require.NoError(t, repo.FinishImport(ctx, created.ID))

	finished, err := svc.GetImport(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ImportCompleted, finished.Status)
	assert.Equal(t, 1, finished.RowsFailed)
	assert.Equal(t, []models.ImportError{{Row: 3, Message: "refused"}}, finished.Errors)
	var fileGone bool
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT file IS NULL FROM product_import WHERE id = $1`, created.ID).Scan(&fileGone))
	assert.True(t, fileGone, "the file is dropped when the import ends")

	_, ok, err = repo.ClaimImport(ctx)
	require.NoError(t, err)
	assert.False(t, ok, "a completed import is not claimed again")
}

// TestTheImportConstraintsAreTheLastDefence writes past the service.
func TestTheImportConstraintsAreTheLastDefence(t *testing.T) {
	ctx := context.Background()
	violation := func(statement, constraint string) {
		t.Helper()
		_, err := testPool.Pool().Exec(ctx, statement)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		assert.Equal(t, constraint, pgErr.ConstraintName)
	}

	violation(`INSERT INTO product_import (id, file, rows_total, status) VALUES ('pimp_x1', 'x', 1, 'done')`,
		"product_import_status_valid")
	violation(`INSERT INTO product_import (id, file, rows_total, rows_done) VALUES ('pimp_x2', 'x', 1, 2)`,
		"product_import_rows_counted")
	violation(`INSERT INTO product_import (id, file, rows_total, rows_done, rows_created) VALUES ('pimp_x3', 'x', 1, 0, 1)`,
		"product_import_rows_counted")
	violation(`INSERT INTO product_import (id, file, rows_total) VALUES ('pimp_x4', NULL, 1)`,
		"product_import_file_while_open")
	violation(`INSERT INTO product_import (id, file, rows_total, rows_done, status) VALUES ('pimp_x5', NULL, 1, 1, 'completed')`,
		"product_import_finished_when_completed")
}
