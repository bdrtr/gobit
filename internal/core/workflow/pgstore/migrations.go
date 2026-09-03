package pgstore

import (
	"embed"
	"io/fs"
)

// MigrationOwner is the name of the core component that owns this schema.
//
// db.Migrate separates the version ledger by owner (a distinct
// <owner>_schema_migrations table per owner), so the name lives in exactly one
// place: callers spelling "workflow" by hand would mean the version ledger
// quietly splitting in two the day the name changed.
const MigrationOwner = "workflow"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded set with the "migrations/" prefix stripped.
// db.Migrate opens the source with iofs.New(src, "."), so the ROOT has to be
// the directory the files are in.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Migrations returns the migration files of the workflow schema.
//
// The core applies them with
// db.Migrate(ctx, dsn, pgstore.Migrations(), pgstore.MigrationOwner); rolling
// back uses the same source through db.MigrateDown.
func Migrations() fs.FS {
	return migrationsRoot
}

// mustSub opens the subdirectory and panics if it cannot.
//
// The panic is safe here: the directory name is a compile-time constant and the
// embed directive above has already verified at compile time that the files
// exist. Returning an error would force every caller to handle a branch that can
// never be taken.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("pgstore: the embedded migrations directory could not be opened: " + err.Error())
	}

	return sub
}
