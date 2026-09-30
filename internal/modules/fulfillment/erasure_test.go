package fulfillment_test

import (
	"testing"

	"github.com/bdrtr/gobit/internal/modules/fulfillment"
	"github.com/bdrtr/gobit/internal/schemaaudit"
)

// notPersonalColumns lists the fulfillment columns that hold nothing about a
// person (ADR 0278). A parcel's identifiers, its order and option, its
// provider's id, its state and its stamps describe the shipment; its lines are
// quantities of order lines; and the shipping options, their rules, profiles
// and locations are the shop's configuration, the same for every buyer.
var notPersonalColumns = map[string][]string{
	"fulfillments": {
		"id", "reference", "shipping_option_id", "provider_id", "external_id", "status",
		"shipped_at", "delivered_at", "canceled_at", "returned_at", "created_at", "updated_at",
	},
	"fulfillment_items": {
		"id", "fulfillment_id", "line_item_id", "quantity", "seq", "created_at", "updated_at",
	},
	"fulfillment_manual_shipments": {
		"id", "option_id", "reference", "status", "created_at", "updated_at",
	},
	"shipping_options": {
		"id", "name", "region_id", "shipping_profile_id", "provider_id", "price_type", "amount",
		"currency_code", "is_return", "admin_only", "data", "metadata", "created_at", "updated_at",
		"deleted_at",
	},
	"shipping_option_rules": {
		"id", "shipping_option_id", "attribute", "operator", "rule_values", "created_at",
		"updated_at", "deleted_at",
	},
	"shipping_profiles": {
		"id", "name", "type", "metadata", "created_at", "updated_at", "deleted_at",
	},
	"shipping_locations": {
		"location_id", "priority", "created_at", "updated_at",
	},
	"shipping_location_regions": {
		"location_id", "region_id", "created_at",
	},
}

// TestTheDeclarationCoversEveryColumnOfTheSchema holds the declaration to the
// columns the migrations leave, each judged once.
func TestTheDeclarationCoversEveryColumnOfTheSchema(t *testing.T) {
	t.Parallel()

	module := fulfillment.New()
	schemaaudit.Cover(t, module.Migrations(), module.PersonalData(), notPersonalColumns)
}
