package webpush

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// pushTemplates loads copy for "order.shipped" in the default, "tr" and
// "en-GB", as an operator names the files.
func pushTemplates(t *testing.T) templateSet {
	t.Helper()

	dir := t.TempDir()
	for name, title := range map[string]string{
		"order.shipped.tmpl":       "Shipped",
		"order.shipped.tr.tmpl":    "Kargoda",
		"order.shipped.en-GB.tmpl": "Dispatched",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(
			`{{define "title"}}`+title+` {{.display_id}}{{end}}{{define "body"}}b{{end}}{{define "url"}}/o/{{.order_id}}{{end}}`), 0o600))
	}
	templates, err := loadTemplates(dir)
	require.NoError(t, err)

	return templates
}

// rendered decodes what one device would receive.
func rendered(t *testing.T, render func(subscription) ([]byte, error), locale string) payload {
	t.Helper()

	raw, err := render(subscription{Locale: locale})
	require.NoError(t, err)
	var out payload
	require.NoError(t, json.Unmarshal(raw, &out))

	return out
}

// TestAPushIsRenderedInTheDevicesLanguage is ADR 0446's copy: a device's
// locale, then its language, then the default, the file's region spelled in
// any case.
func TestAPushIsRenderedInTheDevicesLanguage(t *testing.T) {
	t.Parallel()

	p := &Pusher{m: &webpushModule{opts: moduleOptions{templates: pushTemplates(t)}}}
	render, name, err := p.renderer(Push{
		Template: "order.shipped", Data: map[string]string{"display_id": "1001", "order_id": "order_1"},
		Tag: "order-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "order.shipped", name)

	for locale, title := range map[string]string{
		"": "Shipped 1001", "tr": "Kargoda 1001", "tr-tr": "Kargoda 1001",
		"en-gb": "Dispatched 1001", "en-us": "Shipped 1001", "de": "Shipped 1001",
	} {
		got := rendered(t, render, locale)
		assert.Equalf(t, title, got.Title, "locale %q", locale)
		assert.Equal(t, "/o/order_1", got.URL)
		assert.Equal(t, "order-1", got.Tag, "the tag reaches the service worker")
	}
}

// TestAWrittenPushIsSentAsWritten holds a push without a template to its
// title and body, and refuses one missing either.
func TestAWrittenPushIsSentAsWritten(t *testing.T) {
	t.Parallel()

	p := &Pusher{m: &webpushModule{opts: moduleOptions{templates: pushTemplates(t)}}}
	render, _, err := p.renderer(Push{Title: " Sale ", Body: "Today only", URL: "/sale"})
	require.NoError(t, err)
	assert.Equal(t, payload{Title: "Sale", Body: "Today only", URL: "/sale"}, rendered(t, render, "tr"))

	for _, push := range []Push{{Title: "Sale"}, {Body: "Today only"}, {}} {
		_, _, err := p.renderer(push)
		assert.Equal(t, codePushInvalid, coreerrors.CodeOf(err))
	}

	_, _, err = p.renderer(Push{Title: "Sale", Body: strings.Repeat("x", MaxPayloadBytes)})
	assert.Equal(t, codePayloadTooLarge, coreerrors.CodeOf(err), "a push too large is refused before a device is read")
}

// TestAPushIsRefusedBeforeADeviceIsRead holds the call's own refusals: no
// module, too many customers, a template nobody wrote.
func TestAPushIsRefusedBeforeADeviceIsRead(t *testing.T) {
	t.Parallel()

	_, err := (&Pusher{}).PushToCustomer(t.Context(), "cust_1", Push{Title: "a", Body: "b"})
	assert.Equal(t, coreerrors.KindUnavailable, coreerrors.KindOf(err))

	p := &Pusher{m: &webpushModule{store: &store{}, opts: moduleOptions{templates: pushTemplates(t)}}}
	_, err = p.PushToCustomers(t.Context(), make([]string, maxPushCustomers+1), Push{Title: "a", Body: "b"})
	assert.Equal(t, codePushInvalid, coreerrors.CodeOf(err))

	_, err = p.PushToCustomer(t.Context(), "cust_1", Push{Template: "order.lost"})
	assert.Equal(t, codeTemplateUnknown, coreerrors.CodeOf(err))
}
