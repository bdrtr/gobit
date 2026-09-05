package service

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
)

// This file is the module's ONLY face turned towards the ORDER (ADR 0001,
// ADR 0006).
//
// The module cannot import order and therefore cannot name its types. The
// access consists of three parts and all three of them sit here:
//
//  1. The NARROW interface that is needed is defined in this package
//     ([OrderContactReader]).
//  2. The concrete surface is resolved from the container BY NAME
//     ("order.interop").
//  3. The data carried is JSON; the schema is written out EXPLICITLY in the
//     [orderContact] documentation.

// OrderInteropName is the name, in the container, of the order module's
// PRIMITIVE reading surface (the SAME value as order.InteropName).
//
// The value is repeated by hand because modules cannot import each other
// (Principle 2.4); just as the core repeats coreplugin.NotificationProvidersName
// by hand. The price of divergence is concrete: if the name changes, this
// module cannot read the contact information of any order and every order
// notification would return an error — the compiler cannot catch this, but the
// integration test can.
const OrderInteropName = "order.interop"

// OrderContactReader is the NARROW surface the module wants from the order.
//
// It is defined on the consuming side and order's "order.interop" registration
// satisfies it STRUCTURALLY; there is NO compile-time tie between the two sides
// and there cannot be one (Principle 2.4). That is why the signature has to
// speak in primitive and stdlib types: had a type of order's been named here,
// that type would be ANOTHER type defined in this package and the concrete
// surface would not satisfy this interface.
type OrderContactReader interface {
	OrderContactJSON(ctx context.Context, orderID string) (json.RawMessage, error)
}

// orderContact is the JSON schema of the "order.interop" response.
//
//	{
//	  "order_id":      "order_01H…",
//	  "display_id":    "1042",       // a STRING without a fraction
//	  "email":         "a@b.com",    // may be EMPTY
//	  "currency_code": "TRY",
//	  "total":         "6100",       // minor unit, a STRING without a fraction
//	  "item_count":    "2"           // a STRING without a fraction
//	}
//
// ALL the values are strings and the field names are exactly the same as the
// payload of the "order.placed" event; the reasoning is in the order module's
// surface documentation. The only thing repeated here is the SCHEMA, not the
// rule.
//
// Unknown fields are IGNORED (json.Unmarshal's default): the side producing the
// body is not the caller but the order itself, that is, an unrecognized field
// is not a typo but a new field added to the surface. Counting it as an error
// would have meant every field added to order dropping all order notifications.
type orderContact struct {
	OrderID      string `json:"order_id"`
	DisplayID    string `json:"display_id"`
	Email        string `json:"email"`
	CurrencyCode string `json:"currency_code"`
	Total        string `json:"total"`
	ItemCount    string `json:"item_count"`
}

// NewOrderContacts produces an order reader that works LAZILY over the
// container.
//
// The laziness is mandatory: the Register order of the modules is not
// guaranteed and while this module is being registered "order.interop" may not
// be in the container yet (see the module.Module documentation). The resolution
// is deferred to the first use, that is, to the first "order.placed" event.
//
// The return type is an INTERFACE: the caller (module.go) has no need for the
// concrete type and the service asks for this interface anyway; in the tests a
// fake a few lines long is put in its place.
func NewOrderContacts(c *container.Container) OrderContactReader {
	return &lazyOrderContacts{c: c}
}

// lazyOrderContacts is lazy access to the "order.interop" surface.
type lazyOrderContacts struct {
	// c is the container the registration will be looked up in; it may be nil
	// (embedded use/test).
	c *container.Container

	// mu makes sure the reader is resolved only once.
	//
	// sync.Once was DELIBERATELY not used: Once makes the RESULT of the first
	// call permanent too, and a single resolution that fell over while order
	// was not yet registered would leave all notifications dead for the
	// lifetime of the process. The lock only stores the SUCCESSFUL result; an
	// error is retried on the next event.
	mu     sync.Mutex
	reader OrderContactReader
}

// OrderContactJSON returns the contact information of the order as raw JSON.
func (l *lazyOrderContacts) OrderContactJSON(
	ctx context.Context,
	orderID string,
) (json.RawMessage, error) {
	reader, err := l.resolve()
	if err != nil {
		return nil, err
	}
	return reader.OrderContactJSON(ctx, orderID)
}

// resolve resolves the order surface from the container and stores the result.
func (l *lazyOrderContacts) resolve() (OrderContactReader, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.reader != nil {
		return l.reader, nil
	}
	if l.c == nil {
		return nil, errors.Unavailable(CodeContactUnavailable,
			"there is no container; the %q surface cannot be resolved", OrderInteropName)
	}

	reader, err := container.Resolve[OrderContactReader](l.c, OrderInteropName)
	if err != nil {
		// The KIND is PRESERVED: when there is no registration NotFound comes,
		// when the type does not match Internal comes, and the two are
		// different faults — one is "order is not installed", the other is
		// "the signature of the surface has changed".
		return nil, errors.Wrap(err, errors.KindOf(err), CodeContactUnavailable,
			"the order reading surface %q could not be resolved; is the order module installed?",
			OrderInteropName)
	}

	l.reader = reader
	return reader, nil
}
