package repository

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The schema's own rules, by the names the migration gives them.
const (
	// constraintNamePresent refuses a profile with no legal name.
	constraintNamePresent = "store_profile_name_present"
	// constraintCountryValid refuses a country code that is not two capitals.
	constraintCountryValid = "store_profile_country_valid"
	// constraintSingleton refuses a second profile.
	constraintSingleton = "store_profile_singleton"
)

// sqlStateCheckViolation is PostgreSQL's code for a failed CHECK.
const sqlStateCheckViolation = "23514"

// classify turns a driver error into a typed one.
//
// Every rule this table holds is one a client can correct, so leaving them
// unclassified would answer a fixable request with a 500 and put the real reason
// in the log alone. The names are the migration's, character for character.
func classify(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != sqlStateCheckViolation {
		return coreerrors.Wrap(err, coreerrors.KindInternal, codeQueryFailed,
			"the store profile could not be written")
	}

	switch {
	case strings.HasSuffix(pgErr.ConstraintName, constraintNamePresent):
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidProfile,
			"the shop's legal name is required; a document cannot be issued under no name")
	case strings.HasSuffix(pgErr.ConstraintName, constraintCountryValid):
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidProfile,
			"the country has to be an ISO 3166-1 alpha-2 code in capitals")
	case strings.HasSuffix(pgErr.ConstraintName, constraintSingleton):
		// Unreachable through this package: the identifier is a constant. It is
		// named anyway, because a second profile written by hand is the state
		// the CHECK exists to refuse and an unclassified 500 would hide which
		// rule refused it.
		return coreerrors.Wrap(err, coreerrors.KindConflict, codeInvalidProfile,
			"one installation is one shop; a second store profile cannot be written")
	}

	return coreerrors.Wrap(err, coreerrors.KindInternal, codeQueryFailed,
		"the store profile could not be written")
}

// codeInvalidProfile reports a profile the schema refused.
const codeInvalidProfile = "settings_store_profile_invalid"
