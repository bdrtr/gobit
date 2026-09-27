package service_test

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// memImport is an import as the fake keeps it, file included.
type memImport struct {
	record models.Import
	file   []byte
	order  int
}

// importsOf returns the fake's imports, made on first use.
func (m *memStore) importsOf() map[string]*memImport {
	if m.imports == nil {
		m.imports = map[string]*memImport{}
	}

	return m.imports
}

// CreateImport keeps the import with its file.
func (m *memStore) CreateImport(_ context.Context, id string, file []byte, rowsTotal int) (models.Import, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	imports := m.importsOf()
	imports[id] = &memImport{
		record: models.Import{
			ID: id, Status: models.ImportPending, RowsTotal: rowsTotal, CreatedAt: time.Now(),
			Errors: []models.ImportError{},
		},
		file:  append([]byte(nil), file...),
		order: len(imports),
	}

	return imports[id].record, nil
}

// GetImport reads the import.
func (m *memStore) GetImport(_ context.Context, id string) (models.Import, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	found, ok := m.importsOf()[id]
	if !ok {
		return models.Import{}, errors.NotFound("product_not_found", "import not found: %s", id)
	}

	return found.record, nil
}

// ClaimImport takes the oldest open import.
func (m *memStore) ClaimImport(context.Context) (models.ClaimedImport, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var oldest *memImport
	for _, candidate := range m.importsOf() {
		if candidate.record.Status == models.ImportCompleted {
			continue
		}
		if oldest == nil || candidate.order < oldest.order {
			oldest = candidate
		}
	}
	if oldest == nil {
		return models.ClaimedImport{}, false, nil
	}
	oldest.record.Status = models.ImportRunning
	if oldest.record.StartedAt == nil {
		now := time.Now()
		oldest.record.StartedAt = &now
	}

	return models.ClaimedImport{
		ID: oldest.record.ID, File: oldest.file, RowsTotal: oldest.record.RowsTotal, RowsDone: oldest.record.RowsDone,
	}, true, nil
}

// RecordImportRow moves the import past the row once, as the statement does.
func (m *memStore) RecordImportRow(_ context.Context, id string, rowIndex int, outcome models.ImportRowOutcome) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	found, ok := m.importsOf()[id]
	if !ok || found.record.RowsDone != rowIndex {
		return nil
	}
	record := &found.record
	record.RowsDone++
	if outcome.Created {
		record.RowsCreated++
	}
	if outcome.Updated {
		record.RowsUpdated++
	}
	if outcome.Error != nil {
		record.RowsFailed++
		if len(record.Errors) < 1000 {
			record.Errors = append(record.Errors, *outcome.Error)
		}
	}

	return nil
}

// FinishImport closes the import when every row is done.
func (m *memStore) FinishImport(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	found, ok := m.importsOf()[id]
	if !ok || found.record.RowsDone != found.record.RowsTotal || found.record.Status == models.ImportCompleted {
		return nil
	}
	now := time.Now()
	found.record.Status, found.record.FinishedAt, found.file = models.ImportCompleted, &now, nil

	return nil
}
