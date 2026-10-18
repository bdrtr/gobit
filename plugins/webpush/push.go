package webpush

import (
	"context"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// ServiceName is the container name the plugin's push service is registered
// under (ADR 0446). A module or a plugin installed beside web-push resolves it
// on first use:
//
//	pusher, err := container.Resolve[*webpush.Pusher](c, webpush.ServiceName)
//
// It is registered only where the plugin is installed, so a caller that may
// run without it asks the container with Has first.
const ServiceName = ModuleName + ".service"

// maxPushCustomers bounds the customers one call names; a larger audience is
// pushed in several calls.
const maxPushCustomers = 1000

// codePushInvalid reports a push the service will not send.
const codePushInvalid = "webpush_push_invalid"

// Push is one message to the devices of a set of customers (ADR 0446).
//
// Either Template names a file in WEBPUSH_TEMPLATE_DIR, which Data fills and
// which is rendered per device in its locale, then its language, then the
// default; or Title and Body are the message as written, the same on every
// device.
type Push struct {
	// Template names the copy, as the file name spells it without its
	// locale and extension ("order.shipped"). Empty sends Title, Body and URL.
	Template string
	// Data fills the template. Every value is a string, as an event's are.
	Data map[string]string
	// Title and Body are the message when no template is named; both are
	// required then.
	Title string
	Body  string
	// URL is where a tap goes, a path on the storefront or an address; it may
	// be empty.
	URL string
	// Tag is the notification's tag: the service worker receives it beside
	// the copy, and a later push with the same tag replaces this one at the
	// push service before it is delivered. Empty names none.
	Tag string
}

// PushResult counts what a push did, device by device: a fan-out has no
// single truth value (ADR 0018).
type PushResult struct {
	// Devices is how many devices of the customers were tried.
	Devices int
	// Delivered is how many a push service accepted the message for.
	Delivered int
	// Removed is how many can never be sent to again and were deleted: gone
	// at the push service, minted under another key, holding unusable keys,
	// or at an endpoint that is not a push service's.
	Removed int
	// Failed is how many failed otherwise; those devices are kept.
	Failed int
}

// Pusher pushes a message to customers' devices with the plugin's own key,
// copy, endpoint rule and clean-up (ADR 0446). One serves every caller.
type Pusher struct {
	m *webpushModule
}

// PushToCustomer pushes to the devices bound to one customer. An empty id
// pushes to nobody: a guest has no devices.
func (p *Pusher) PushToCustomer(ctx context.Context, customerID string, push Push) (PushResult, error) {
	return p.PushToCustomers(ctx, []string{customerID}, push)
}

// PushToCustomers pushes to the devices bound to any of the customers, at
// most [maxPushCustomers] of them in one call.
//
// It refuses before reading a device a push it cannot render: an unknown
// template, a message without a title or a body, or one too large for a push;
// and a first read of the devices that fails. Past that point it returns nil
// and the counts; a device that failed is
// logged with its id and never its endpoint. A device bound to no customer is
// never pushed to, whatever the set holds.
func (p *Pusher) PushToCustomers(ctx context.Context, customerIDs []string, push Push) (PushResult, error) {
	if p == nil || p.m == nil || p.m.store == nil {
		return PushResult{}, coreerrors.Unavailable(codeSetupFailed,
			"the %s module is not registered; nothing was pushed", ModuleName)
	}
	if len(customerIDs) > maxPushCustomers {
		return PushResult{}, coreerrors.Invalid(codePushInvalid,
			"a push names at most %d customers; %d were given", maxPushCustomers, len(customerIDs))
	}
	render, name, err := p.renderer(push)
	if err != nil {
		return PushResult{}, err
	}
	// Rendered once in the default locale before the walk, so a template that
	// does not render is this call's error rather than every device's.
	if _, err := render(subscription{}); err != nil {
		return PushResult{}, err
	}

	m := p.m
	// The Topic header follows the tag: with none, nothing replaces this push
	// at the push service.
	topic := ""
	if tag := strings.TrimSpace(push.Tag); tag != "" {
		topic = topicFor(name, tag)
	}
	var total PushResult
	after := ""
	for {
		page, err := m.store.byCustomers(ctx, customerIDs, after, pageSize)
		if err != nil {
			if total.Devices == 0 {
				return total, err
			}
			m.opts.log.WarnContext(ctx, "a push could not read a page of devices",
				"after", after, "error", err)

			return total, nil
		}
		if len(page) == 0 {
			break
		}
		result := fanOut(ctx, m.sender, m.store, m.opts.log, page, m.opts.fingerprint, render, topic)
		total.Devices += result.Attempted
		total.Delivered += result.Delivered
		total.Removed += result.Gone
		total.Failed += result.Failed
		after = page[len(page)-1].ID
	}

	m.opts.log.InfoContext(ctx, "a push was sent to customers' devices",
		"push", name, "customers", len(customerIDs), "devices", total.Devices,
		"delivered", total.Delivered, "removed", total.Removed, "failed", total.Failed)

	return total, nil
}

// renderer is the push's per-device rendering and the name it is logged by.
func (p *Pusher) renderer(push Push) (render func(subscription) ([]byte, error), name string, err error) {
	tag := strings.TrimSpace(push.Tag)
	if name := strings.TrimSpace(push.Template); name != "" {
		templates := p.m.opts.templates

		return func(sub subscription) ([]byte, error) {
			return templates.renderTagged(name, sub.Locale, push.Data, tag)
		}, name, nil
	}

	title, body := strings.TrimSpace(push.Title), strings.TrimSpace(push.Body)
	if title == "" || body == "" {
		return nil, "", coreerrors.Invalid(codePushInvalid,
			"a push names a template, or carries a title and a body")
	}
	encoded, err := encodePayload(payload{Title: title, Body: body, URL: strings.TrimSpace(push.URL), Tag: tag}, "message")
	if err != nil {
		return nil, "", err
	}

	return func(subscription) ([]byte, error) { return encoded, nil }, "message", nil
}
