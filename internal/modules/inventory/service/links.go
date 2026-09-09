package service

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English.

import "github.com/bdrtr/gobit/core/link"

// The names this module writes on the ends of its link definitions.
//
// A link end names an ENTITY, not a module: when the Query layer resolves a
// link it reads the far end's module field as the entity name and looks the
// provider up under "<entity>.query" (ADR 0004). The product module's links.go
// carries the same warning for the same end.
const (
	// ModuleName is this module's name; it is used on the ends this module owns.
	ModuleName = "inventory"
	// EntityStockLocation is the entity name of a warehouse.
	//
	// It registers NO Query provider today and does not need one for the link
	// to work: the checkout reads the binding through core/link itself, not
	// through an expansion. The name still has to be right — a link end is
	// what a future expansion would resolve — and it is the table's own name.
	EntityStockLocation = "stock_location"
	// EntitySalesChannel is the sales channel entity of the auth module.
	//
	// The module is called "auth" and its entity "sales_channel"; the entity is
	// what goes on the link end, because "auth.query" is registered nowhere.
	// The product module writes the same name for the same reason and this
	// package cannot import either of them (Principle 2.4).
	EntitySalesChannel = "sales_channel"
)

// LinkStockLocationSalesChannel binds a warehouse to a sales channel it ships
// for.
//
// # What the binding MEANS, and what its absence means
//
// A channel with at least one warehouse bound ships from THOSE warehouses and
// no others: the checkout narrows its reservation candidates to them, so an
// order placed on that channel cannot quietly be served from a warehouse the
// channel does not serve.
//
// A channel with NO warehouse bound is NOT "a channel that ships from nowhere".
// It is a channel nobody has configured, and it ships from anywhere — which is
// the behavior of every installation that existed before this link, and the
// only reading that does not turn an upgrade into an outage. The price is that
// the safe state has to be written down rather than derived: a merchant who
// binds one warehouse has narrowed the channel, and a merchant who binds none
// has said nothing at all.
const LinkStockLocationSalesChannel = "stock_location_sales_channel"

// Definitions are the link definitions this module declares.
//
// They are declared at REGISTRATION and verified idempotently on every startup
// (ADR 0005), which is why the schema lives next to the definition rather than
// in a migration: the link tables belong to core/link and no module owns them.
func Definitions() []link.LinkDefinition {
	return []link.LinkDefinition{
		{
			Name: LinkStockLocationSalesChannel,
			// The warehouse is the FROM end because this module declares it and
			// owns that end; the channel is somebody else's record and this
			// module never validates it (Principle 2.2). The reverse direction
			// — "which warehouses does this channel ship from" — is the one the
			// checkout reads, and core/link answers it in one query.
			From:        link.LinkSide{Module: ModuleName, Entity: EntityStockLocation, Field: "stock_location_id"},
			To:          link.LinkSide{Module: EntitySalesChannel, Entity: EntitySalesChannel, Field: "sales_channel_id"},
			Cardinality: link.ManyToMany,
		},
	}
}
