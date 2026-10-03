package repository

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// TestAUniquenessViolationIsCodedByConstraintName checks that the request that
// loses the race gets the SAME code as the request caught by the service's
// "read first" check.
//
// Both paths describe the same situation: a second root region for a country,
// a second default rate for a region. Had the codes diverged, an admin UI that
// branches on the code would see the same situation under two different codes
// across two concurrent requests and map the message wrongly.
//
// The test needs no real database: what it proves is that SQLSTATE 23505 is
// mapped by constraint NAME, and that the constraint names really are these is
// shown separately in the integration tests.
func TestAUniquenessViolationIsCodedByConstraintName(t *testing.T) {
	tests := map[string]struct {
		constraint string
		wantCode   string
	}{
		"a country's second root region": {
			constraint: constraintRegionCountryRoot,
			wantCode:   service.CodeRootExists,
		},
		"a region's second default rate": {
			constraint: constraintRateDefault,
			wantCode:   service.CodeDefaultExists,
		},
		"a constraint with no service counterpart": {
			constraint: "tax_rate_rule_uniq",
			wantCode:   CodeDuplicate,
		},
		"no constraint name reported": {
			constraint: "",
			wantCode:   CodeDuplicate,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := wrapDB(&pgconn.PgError{
				Code:           sqlstateUniqueViolation,
				ConstraintName: tc.constraint,
			}, "the record could not be written")

			require.Error(t, err)
			assert.True(t, errors.IsConflict(err), "a uniqueness violation is a CONFLICT")
			assert.Equal(t, tc.wantCode, errors.CodeOf(err))
			assert.Contains(t, err.Error(), tc.constraint,
				"the message has to name the rule that was broken")
		})
	}
}
