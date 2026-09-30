package file_test

import (
	"testing"

	"github.com/bdrtr/gobit/internal/modules/file"
	"github.com/bdrtr/gobit/internal/schemaaudit"
)

// notPersonalColumns lists the columns of an upload that hold nothing about a
// person (ADR 0278): its identifiers and the provider's key for the content,
// what the content is and how big, its digest, and its stamps. uploaded_by is
// a staff member's id, which resolves through the auth module as api_key's
// created_by does and is judged there.
var notPersonalColumns = map[string][]string{
	"file_uploads": {
		"id", "storage_key", "provider_id", "content_type", "size", "checksum",
		"uploaded_by", "created_at", "updated_at",
	},
}

// TestTheDeclarationCoversEveryColumnOfTheSchema holds the declaration to the
// columns the migrations leave, each judged once.
func TestTheDeclarationCoversEveryColumnOfTheSchema(t *testing.T) {
	t.Parallel()

	module := file.New(file.Options{})
	schemaaudit.Cover(t, module.Migrations(), module.PersonalData(), notPersonalColumns)
}
