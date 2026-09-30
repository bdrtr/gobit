package app

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus/outbox"
)

// TestEveryPrintedCommandNamesTheProgram holds ADR 0254 across the text the
// operator subcommands print: each piece that hands an operator a command to
// type names the program the embedder named, and none of them names gobit
// before a subcommand.
//
// "gobit" alone may still appear, since the usage text speaks of a gobit
// checkout; what may not is "gobit" followed by one of this program's own
// subcommands.
func TestEveryPrintedCommandNamesTheProgram(t *testing.T) {
	t.Parallel()

	const program = "shop"

	var deadLetters strings.Builder
	require.NoError(t, writeDeadLetters(program, &deadLetters, outbox.DeadLetterReport{
		Count:  3,
		Oldest: []outbox.DeadLetter{letter("evt_1", "order.placed", "the broker refused", 5)},
	}, 1, deadLetterFixture))
	_, listErr := parseDeadLetterListFlags(program, []string{"evt_1"})
	require.Error(t, listErr)
	_, actionErr := parseDeadLetterAction(program, cmdDiscard, nil)
	require.Error(t, actionErr)
	_, newErr := parseNewFlags(program, nil)
	require.Error(t, newErr)

	texts := map[string]string{
		"usage":               usageText(program, "dev"),
		"rollback plan":       downPlanText(program, ownerState{owner: "cart", version: 4}, 2),
		"seed reset plan":     resetPlanText(program, "shop_db", "", "report"),
		"dead-letter listing": deadLetters.String(),
		"dead-letter outcome": deadLetterOutcomeText(program, deadLetterAction{verb: cmdRedrive, eventID: "evt_1"}, 2),
		"list refusal":        listErr.Error(),
		"action refusal":      actionErr.Error(),
		"new refusal":         newErr.Error(),
	}

	subcommands := []string{
		cmdMigrate, stuckCommand, recoverCommand, jobsCommand, deadLettersCommand, seedCommand,
		refoldInvoicesCommand, mfaResetCommand, newCommand, mcpCommand, cmdHelp,
	}
	named := regexp.MustCompile(`\b` + regexp.QuoteMeta(binaryName) + ` (` + strings.Join(subcommands, "|") + `)\b`)
	for what, text := range texts {
		assert.Contains(t, text, program+" ", "the %s names no command to type", what)
		assert.Empty(t, named.FindAllString(text, -1), "the %s still names gobit:\n%s", what, text)
	}
}
