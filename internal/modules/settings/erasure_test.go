package settings_test

import (
	"testing"

	"github.com/bdrtr/gobit/internal/modules/settings"
	"github.com/bdrtr/gobit/internal/schemaaudit"
)

// notPersonalColumns lists the shop profile's columns that hold nothing about
// a person (ADR 0278): the singleton's id and its stamps. Every other column
// is the shop's legal identity, which for a sole trader is a person's, and is
// declared.
var notPersonalColumns = map[string][]string{
	"store_profile": {"id", "created_at", "updated_at"},
}

// TestTheDeclarationCoversEveryColumnOfTheSchema holds the declaration to the
// columns the migrations leave, each judged once.
func TestTheDeclarationCoversEveryColumnOfTheSchema(t *testing.T) {
	t.Parallel()

	module := settings.New(nil)
	schemaaudit.Cover(t, module.Migrations(), module.PersonalData(), notPersonalColumns)
}
