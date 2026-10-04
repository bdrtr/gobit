package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification"
	notificationsvc "github.com/bdrtr/gobit/internal/modules/notification/service"
	"github.com/bdrtr/gobit/plugins/notificationsmtp"
)

// TestTheSMTPProviderTellsTheNotificationModuleWhatItHolds holds ADR 0386's copy
// check together across the plugin boundary: the SMTP plugin implements the
// core's optional TemplateHolder (its compile-time assertion), and this test
// asks the provider the notification module's registry hands back after the
// plugin registered it. A host that wrapped the provider on the way in would
// drop the optional method while every compile-time check stayed green, and
// every SMTP installation without order.completed.tmpl would go back to one
// failed delivery row per completed order.
func TestTheSMTPProviderTellsTheNotificationModuleWhatItHolds(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "order.placed.tmpl"),
		[]byte(`{{define "subject"}}Order {{.display_id}}{{end}}{{define "body"}}Thank you.{{end}}`), 0o600))

	c := container.New(discardLogger())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	providers := notificationsvc.NewProviderRegistry()
	require.NoError(t, c.Provide(notification.ProvidersName, providers))

	host := coreplugin.NewHost(c, nil, nil, discardLogger(), map[string]string{
		"SMTP_HOST":         "smtp.example.test",
		"SMTP_FROM":         "Store <no-reply@example.test>",
		"SMTP_TEMPLATE_DIR": dir,
	})
	plugins := coreplugin.NewRegistry(discardLogger())
	plugins.Add(notificationsmtp.New())
	require.NoError(t, plugins.Install(t.Context(), host))
	require.NoError(t, plugins.Start(t.Context(), host))

	provider, err := providers.Get(notificationsmtp.ProviderID)
	require.NoError(t, err)
	holder, ok := provider.(coreprovider.TemplateHolder)
	require.True(t, ok, "the SMTP provider answers the notification module's question")
	assert.True(t, holder.HoldsTemplate(notificationsvc.TemplateOrderPlaced))
	assert.False(t, holder.HoldsTemplate(notificationsvc.TemplateOrderCompleted),
		"an installation that wrote no completion copy is not asked for a completion")
}
