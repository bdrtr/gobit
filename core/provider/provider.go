// Package provider defines the core contracts of the components that connect
// to the outside world (payment, fulfillment, notification, file).
//
// The interfaces here live in the CORE and know no module (Principle 2.4). The
// concrete providers live either inside a module (e.g. the manual provider in
// the payment module) or in a plugin, and register themselves with the
// Registry; the core sees them only through these interfaces.
//
// # Why in the core
//
// Had the provider contract belonged to a module, plugins would have to import
// that module and Phase 9's goal — "add a provider without touching the core" —
// would break. Keeping the contract in the core makes the plugin and the module
// independent of each other.
package provider

// Provider is the surface every provider has in common.
type Provider interface {
	// ID is the provider's unique identity (e.g. "manual", "stripe").
	// Registration and selection go by this identity, and because it is
	// written into durable data it MUST NOT CHANGE from one release to the
	// next.
	ID() string
}
