package auth

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// TestTheSenderCarriesTheTOKENAndNothingElseIdentifying is the bridge's own test.
//
// The service's tests use a fake seam, so they prove what the FLOW does with a
// token and say nothing about what this wrapper hands the notification module. A
// mutation blanking the token here survived them — which is the gap a fake always
// leaves, one layer down (ADR 0137).
func TestTheSenderCarriesTheTOKENAndNothingElseIdentifying(t *testing.T) {
	t.Parallel()

	spy := &spyMessenger{}
	c := container.New(nil)
	require.NoError(t, c.Provide(notificationInteropName, spy))

	sender := newInvitationSender(c, slog.New(slog.DiscardHandler))

	require.NoError(t, sender.SendInvitation(t.Context(),
		"colleague@example.test", "a-token-only-they-should-hold", "user_01SENDERTEST0000000"))

	require.Len(t, spy.calls, 1)
	call := spy.calls[0]

	assert.Equal(t, InvitationTemplate, call.template)
	assert.Equal(t, coreprovider.ChannelEmail, call.channel)
	assert.Equal(t, "colleague@example.test", call.to)
	assert.Equal(t, "a-token-only-they-should-hold", call.data[invitationFieldToken],
		"the token has to REACH the provider; a message without it is a message "+
			"telling somebody an invitation exists and not how to use it")
	assert.Equal(t, "user_01SENDERTEST0000000", call.reference,
		"the reference is half the notification module's idempotency key, so a "+
			"retried delivery of ONE invitation does not send twice")
}

// TestAMissingNotificationModuleIsAnERROR holds the fail-closed direction.
//
// The service writes the invitation row before asking for it to be sent, so a
// silent skip here would leave a row its owner will never hear of.
func TestAMissingNotificationModuleIsAnERROR(t *testing.T) {
	t.Parallel()

	sender := newInvitationSender(container.New(nil), slog.New(slog.DiscardHandler))

	err := sender.SendInvitation(t.Context(), "nobody@example.test", "t", "user_01NOMODULE00000000")

	require.Error(t, err)
	assert.Contains(t, err.Error(), notificationInteropName,
		"the refusal has to name what is missing; \"could not send\" sends an operator "+
			"looking at their mail provider")
}

// spyMessenger records what the sender asked the notification module to carry.
type spyMessenger struct {
	calls []messengerCall
}

// messengerCall is one Send.
type messengerCall struct {
	template, channel, reference, to string
	data                             map[string]string
}

// Send records the call.
func (s *spyMessenger) Send(
	_ context.Context, template, channel, reference, to string, data map[string]string,
) error {
	s.calls = append(s.calls, messengerCall{
		template: template, channel: channel, reference: reference, to: to, data: data,
	})

	return nil
}
