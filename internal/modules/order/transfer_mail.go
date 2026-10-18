package order

import (
	"context"
	"log/slog"
	"sync"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// notificationInteropName is the notification module's cross-module surface
// in the container. It is that module's name, repeated here for the reason
// returnFlowName is: this module cannot import that one.
const notificationInteropName = "notification.interop"

// transferMail resolves the notification module on first use and mails a
// transfer request's token through it (ADR 0450).
//
// Lazy for the reason every cross-module wrapper here is: the notification
// module may register after this one. A missing surface is an error and not
// a silent skip: the service withdraws a request whose mail failed, so no
// request waits that its holder was never asked.
type transferMail struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  service.TransferMail
	err  error
}

// That the wrapper satisfies the surface the service expects is pinned down at
// compile time.
var _ service.TransferMail = (*transferMail)(nil)

// Send delivers one message through the notification module.
func (m *transferMail) Send(
	ctx context.Context, template, channel, reference, to string, data map[string]string,
) error {
	m.once.Do(func() {
		m.svc, m.err = container.Resolve[service.TransferMail](m.c, notificationInteropName)
		if m.err != nil {
			m.err = errors.Wrap(m.err, errors.KindUnavailable, service.CodeTransferMailFailed,
				"the %s module could not resolve the notification surface (%q); a transfer "+
					"request cannot be mailed", ModuleName, notificationInteropName)

			return
		}
		m.log.InfoContext(ctx, "transfer mail bound", "surface", notificationInteropName)
	})
	if m.err != nil {
		return m.err
	}

	return m.svc.Send(ctx, template, channel, reference, to, data)
}
