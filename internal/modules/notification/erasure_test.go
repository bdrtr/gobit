package notification_test

import (
	"testing"

	"github.com/bdrtr/gobit/internal/modules/notification"
	"github.com/bdrtr/gobit/internal/schemaaudit"
)

// notPersonalColumns lists the delivery log's columns that hold nothing about
// a person (ADR 0278): which template went over which channel and provider,
// for which order, with what outcome and when. The recipient is not stored.
var notPersonalColumns = map[string][]string{
	"notification_deliveries": {
		"id", "template", "channel", "reference", "provider_id", "status", "created_at", "updated_at",
	},
}

// TestTheDeclarationCoversEveryColumnOfTheSchema holds the declaration to the
// columns the migrations leave, each judged once.
func TestTheDeclarationCoversEveryColumnOfTheSchema(t *testing.T) {
	t.Parallel()

	module := notification.New(notification.Options{})
	schemaaudit.Cover(t, module.Migrations(), module.PersonalData(), notPersonalColumns)
}
