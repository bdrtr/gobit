package models

import "time"

// ImportStatus is where an import stands (ADR 0205).
type ImportStatus string

// The import statuses.
const (
	// ImportPending has not been started by a job yet.
	ImportPending ImportStatus = "pending"
	// ImportRunning has rows left and a job has started it.
	ImportRunning ImportStatus = "running"
	// ImportCompleted has had every row applied or refused; its file is gone.
	ImportCompleted ImportStatus = "completed"
)

// Import is a CSV file of the catalog a job works through, and what its rows
// did (ADR 0205).
type Import struct {
	ID     string
	Status ImportStatus
	// RowsTotal is the file's rows, the header left out; RowsDone the rows a
	// job has been through.
	RowsTotal int
	RowsDone  int
	// RowsCreated, RowsUpdated and RowsFailed are what the done rows did: a
	// row that created its product or variant, one that changed an existing
	// one, and one that was refused.
	RowsCreated int
	RowsUpdated int
	RowsFailed  int
	// Errors are the first refused rows; RowsFailed counts them all.
	Errors     []ImportError
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// ImportError is one refused row.
type ImportError struct {
	// Row is the row's number in the file, the header being row 1.
	Row int `json:"row"`
	// Message says why.
	Message string `json:"message"`
}

// ClaimedImport is an import a job has taken, with the file it works through.
type ClaimedImport struct {
	ID        string
	File      []byte
	RowsTotal int
	RowsDone  int
}

// ImportRowOutcome is what one row did.
type ImportRowOutcome struct {
	Created, Updated bool
	// Error is set when the row was refused.
	Error *ImportError
}
