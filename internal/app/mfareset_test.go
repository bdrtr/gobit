package app

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
)

// The reset command is the ONLY answer for an administrator whose phone is gone:
// since ADR 0147 a login demands the factor, and no endpoint removes somebody
// else's. So what these tests hold is an operator's last resort — including the
// part that is easy to get wrong, which is what it PRINTS.

// fakeFactorRemover is the identity service as the command sees it.
type fakeFactorRemover struct {
	user    authmodels.User
	lookup  error
	had     bool
	removed string
	fail    error
}

func (f *fakeFactorRemover) GetUserByEmail(
	_ context.Context, _ string,
) (authmodels.User, error) {
	if f.lookup != nil {
		return authmodels.User{}, f.lookup
	}

	return f.user, nil
}

func (f *fakeFactorRemover) RemoveMFA(_ context.Context, userID string) (bool, error) {
	f.removed = userID

	return f.had, f.fail
}

// resetContainer provides the fake under the name the command resolves.
func resetContainer(t *testing.T, svc *fakeFactorRemover) *container.Container {
	t.Helper()

	c := container.New(slog.New(slog.DiscardHandler))
	require.NoError(t, c.Provide(auth.ServiceName, svc))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	return c
}

// TestTheResetRemovesTheFactorOfTheAccountItWasGiven is the command's whole job.
func TestTheResetRemovesTheFactorOfTheAccountItWasGiven(t *testing.T) {
	t.Parallel()

	svc := &fakeFactorRemover{
		user: authmodels.User{ID: "usr_01LOSTPHONE", Email: "ada@example.test"},
		had:  true,
	}
	var out bytes.Buffer

	require.NoError(t, resetSecondFactor(t.Context(), resetContainer(t, svc), &out,
		"ada@example.test"))

	assert.Equal(t, "usr_01LOSTPHONE", svc.removed,
		"the factor has to come off the account the address names")
	assert.Contains(t, out.String(), "no longer holds a second factor")
	assert.Contains(t, out.String(), "usr_01LOSTPHONE",
		"the operator has to see WHICH account they just opened up")
}

// TestTheResetSaysSoWhenThereWasNoFactor keeps the two outcomes apart.
//
// "There was none" is not a failure — the account ends up in the state the
// operator asked for — but it is the answer that says the telephone call was
// about something else. An operator told only "done" would go on believing the
// phone was the problem.
func TestTheResetSaysSoWhenThereWasNoFactor(t *testing.T) {
	t.Parallel()

	svc := &fakeFactorRemover{
		user: authmodels.User{ID: "usr_01NOFACTOR", Email: "ada@example.test"},
		had:  false,
	}
	var out bytes.Buffer

	require.NoError(t, resetSecondFactor(t.Context(), resetContainer(t, svc), &out,
		"ada@example.test"))

	assert.Contains(t, out.String(), "held no second factor")
	assert.NotContains(t, out.String(), "no longer holds",
		"a removal that removed nothing must not read like one that did")
}

// TestTheResetRefusesAnAccountItCannotRead removes nothing on a typo.
func TestTheResetRefusesAnAccountItCannotRead(t *testing.T) {
	t.Parallel()

	svc := &fakeFactorRemover{lookup: errors.NotFound("auth_user_not_found", "no such user")}
	var out bytes.Buffer

	err := resetSecondFactor(t.Context(), resetContainer(t, svc), &out, "typo@example.test")

	require.Error(t, err)
	assert.Empty(t, svc.removed, "a lookup that failed must not be followed by a write")
	assert.Contains(t, err.Error(), "typo@example.test",
		"the operator has to see which address found nothing")
}

// TestTheResetNeedsTheAddressRepeated is the guard on an irreversible act.
//
// It is irreversible in the sense that matters: the secret is gone, the person
// enrolls again, and in between their account is protected by a password alone.
// Repeating the address is the difference between "I meant that account" and a
// name a shell completed.
func TestTheResetNeedsTheAddressRepeated(t *testing.T) {
	t.Parallel()

	for name, args := range map[string][]string{
		"no confirmation":    {"ada@example.test"},
		"another address":    {"ada@example.test", "-confirm", "bob@example.test"},
		"no address at all":  {},
		"flags but no email": {"-confirm", "ada@example.test"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			email, err := parseMFAResetFlags(args)

			require.Error(t, err)
			assert.Empty(t, email)
		})
	}

	email, err := parseMFAResetFlags([]string{"ada@example.test", "-confirm", "ada@example.test"})
	require.NoError(t, err)
	assert.Equal(t, "ada@example.test", email)
}

// TestTheResetIsInTheUsageText keeps the command findable.
//
// An operator reaches for it during a telephone call, having never run it before.
// A verb the help text does not mention is a verb nobody finds in that moment.
func TestTheResetIsInTheUsageText(t *testing.T) {
	t.Parallel()

	text := usageText("test")

	assert.Contains(t, text, mfaResetCommand)
	assert.True(t, strings.Contains(text, "second factor"),
		"the usage line has to say what it does in the operator's words")
}
