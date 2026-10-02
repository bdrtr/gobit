//go:build integration

package settings_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/settings"
	"github.com/bdrtr/gobit/internal/modules/settings/repository"
	"github.com/bdrtr/gobit/internal/modules/settings/service"
)

// postgresImage is the server the module's migration and queries run on.
const postgresImage = "postgres:16-alpine"

// testPool is the pool the package's tests share.
var testPool *db.Pool

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres starts a server, applies the module's migration and runs
// the tests on it.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_test"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "the postgres container could not be stopped: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "the postgres container could not be started: %v\n", err)
		return 1
	}
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection string could not be read: %v\n", err)
		return 1
	}
	testPool, err = db.New(ctx, db.DefaultConfig(dsn), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)
		return 1
	}
	defer testPool.Close()
	if err := db.Migrate(ctx, dsn, settings.New(nil).Migrations(), settings.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)
		return 1
	}

	return m.Run()
}

// TestTheStoreProfileIsWrittenFromWhatWasRead is ADR 0336 against a real
// PostgreSQL: the first profile is written from none, and a second first
// write is refused rather than written over it; the profile is then written
// from the moment it was last written, to the microsecond, and a write from
// a moment before is refused and changes nothing.
func TestTheStoreProfileIsWrittenFromWhatWasRead(t *testing.T) {
	ctx := context.Background()
	svc := service.New(repository.New(testPool.Pool()), service.Options{})
	shop := service.SetProfileInput{LegalName: "Gobit", TaxNumber: "1234567890", CountryCode: "TR"}

	first, err := svc.ReviseProfile(ctx, nil, shop)
	require.NoError(t, err)
	other := shop
	other.LegalName = "Other"
	_, err = svc.ReviseProfile(ctx, nil, other)
	require.Error(t, err)
	assert.Equal(t, service.CodeProfileRevised, errors.CodeOf(err), "a first profile written in between: %v", err)
	stored, err := svc.GetProfile(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Gobit", stored.LegalName, "the profile written first stands")

	// The moment crosses the panel as text, to the microsecond.
	read, err := time.Parse(time.RFC3339Nano, first.UpdatedAt.Format(time.RFC3339Nano))
	require.NoError(t, err)
	next := shop
	next.LegalName, next.TaxOffice = "Gobit A.S.", "Kadikoy"
	revised, err := svc.ReviseProfile(ctx, &read, next)
	require.NoError(t, err)
	assert.Equal(t, "Gobit A.S.|Kadikoy", revised.LegalName+"|"+revised.TaxOffice)
	assert.True(t, revised.UpdatedAt.After(first.UpdatedAt), "the write moves the moment")

	_, err = svc.ReviseProfile(ctx, &read, other)
	require.Error(t, err)
	assert.Equal(t, service.CodeProfileRevised, errors.CodeOf(err), "a moment before the revision: %v", err)
	stored, err = svc.GetProfile(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Gobit A.S.", stored.LegalName, "a stale write changes nothing")
}
