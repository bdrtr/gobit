package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// TestRecoverNeedsTheIDRepeated is the operator-facing safety property, and it
// is cheap enough to run on every commit.
//
// Recovery WRITES: it releases reservations, cancels the order and voids the
// authorization. The id is a value copied out of a `gobit stuck` listing, where
// one line up or down is a different customer's saga — so a run without the
// repeat does NOTHING.
func TestRecoverNeedsTheIDRepeated(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"no arguments at all":       {},
		"only the id":               {"wfx_1"},
		"a confirmation for nobody": {"-" + flagConfirm, "wfx_1"},
		"another execution's id":    {"wfx_1", "-" + flagConfirm, "wfx_2"},
		"an empty confirmation":     {"wfx_1", "-" + flagConfirm, ""},
		"the flags before the id":   {"-" + flagConfirm, "wfx_1", "wfx_1"},
	}

	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := parseRecoverFlags(args)

			require.Error(t, err, "nothing may run without the id repeated")
			assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
			assert.Equal(t, codeRecoverFailed, coreerrors.CodeOf(err))
		})
	}
}

// TestRecoverAcceptsTheRepeatedID draws the other side of the boundary: the one
// shape that runs.
func TestRecoverAcceptsTheRepeatedID(t *testing.T) {
	t.Parallel()

	parsed, err := parseRecoverFlags([]string{"wfx_ABANDONED01", "-" + flagConfirm, "wfx_ABANDONED01"})

	require.NoError(t, err)
	assert.Equal(t, "wfx_ABANDONED01", parsed.executionID)
}

// TestRecoverRefusesALeftoverArgument keeps a mistyped command from being read
// as a valid one.
//
// `recover wfx_1 -confirm wfx_1 wfx_2` is somebody trying to recover two
// executions; taking the first and dropping the second silently would leave the
// operator believing both were handled.
func TestRecoverRefusesALeftoverArgument(t *testing.T) {
	t.Parallel()

	_, err := parseRecoverFlags([]string{"wfx_1", "-" + flagConfirm, "wfx_1", "wfx_2"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "wfx_2")
}

// TestUsageTextNamesTheRecoverCommand keeps the help text honest: a command the
// binary answers but does not list is a command nobody finds during an incident.
func TestUsageTextNamesTheRecoverCommand(t *testing.T) {
	t.Parallel()

	text := usageText()

	assert.Contains(t, text, recoverCommand)
	assert.Contains(t, text, "-"+flagConfirm+" ID")
}
