// Package link is the layer that relates modules to each other WITHOUT a
// foreign key.
//
// Plan Section 2.2 forbids foreign keys between the tables of different
// modules: every piece of data belongs to exactly one module, and that module
// must remain extractable into a separate service later. An FK nails the two
// modules' tables to the same database and the same lifecycle.
//
// This package moves the relation into a THIRD table: every link lives in its
// own table (e.g. "link_product_price") and that table REFERENCES no module's
// table. A link table holds only two free-form id strings; whether those ids
// really exist is the owning module's responsibility and is cleaned up, when
// needed, by the workflow's compensation step. As a result:
//
//   - Modules never have to know each other's schema.
//   - A module's table can be dropped and recreated; the link table is
//     unaffected (an FK would have blocked the DROP).
//   - The relation keeps working through the same surface once a module moves
//     into its own service; the only thing that changes is where the link
//     table lives.
//
// # Why the schema is not in a migration file
//
// Link tables are not a static set: which links exist is declared by the
// MODULES at startup through [LinkService.Define] (plan Section 5.1,
// Module.Register), and a plugin must be able to add its own link without
// touching the core. That is why the schema is built at declaration time and
// idempotently (CREATE ... IF NOT EXISTS) rather than from a fixed migration
// file. The declaration itself runs in a single transaction under an advisory
// lock; two processes starting at the same time do not race on each other's
// DDL.
//
// The price of this is that the definition is written to a durable ledger
// (link_definitions) and compared on every startup: a definition that silently
// changed between releases is caught that way.
//
// # Cardinality is enforced by a database constraint
//
// [Cardinality] is enforced by a unique index, not by a "read then write"
// check in the application layer. An application-layer check is open to a race
// between two concurrent requests (both read, both write); the index leaves
// the race to the database itself and turns a violation into a typed
// errors.Conflict.
//
// # Table names and injection
//
// Table names cannot be parameterized in SQL; the name is necessarily produced
// by string concatenation. That is why a link name is validated against a
// strict pattern in [LinkDefinition.Validate] and the table name is derived
// from the validated name exactly ONCE, at [Define] time (see [TableName]).
// The runtime Create/Delete/List paths use the pre-built statements; no string
// coming from a caller ever reaches SQL text. For the same rigor on the
// migration side see internal/core/db MigrationsTable.
package link

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Error codes; the caller can branch on these through errors.CodeOf.
const (
	codeNameInvalid          = "link_name_invalid"
	codeSideInvalid          = "link_side_invalid"
	codeCardinalityInvalid   = "link_cardinality_invalid"
	codeIDInvalid            = "link_id_invalid"
	codeNotDefined           = "link_not_defined"
	codeDefinitionConflict   = "link_definition_conflict"
	codeCardinalityViolation = "link_cardinality_violation"
	codeUnavailable          = "link_db_unavailable"
	codeDefineFailed         = "link_define_failed"
	codeQueryFailed          = "link_query_failed"
	codeCanceled             = "link_canceled"
)

const (
	// tablePrefix is the common prefix of link tables. The prefix separates
	// link tables from the modules' own tables at a glance.
	tablePrefix = "link_"
	// definitionsTable is the durable ledger of link definitions.
	definitionsTable = tablePrefix + "definitions"
	// fromIndexSuffix and toIndexSuffix build the names of the indexes that
	// enforce cardinality. The names are also used in error mapping:
	// PostgreSQL reports the name of the violated constraint, and from it we
	// write which end was violated.
	fromIndexSuffix = "_from_uniq"
	toIndexSuffix   = "_to_uniq"
	// toLookupSuffix is the suffix of the NON-unique index that speeds up the
	// reverse-direction lookup under ManyToMany.
	toLookupSuffix = "_to_lookup"
	// relkindTable and relkindIndex are pg_class.relkind values.
	relkindTable = "r"
	relkindIndex = "i"
	// maxNameLen is the maximum length of a link name. PostgreSQL truncates
	// identifiers to 63 bytes; the longest derived name is
	// tablePrefix + name + toIndexSuffix (5 + 40 + 10 = 55), so a limit of 40
	// makes silent truncation impossible.
	maxNameLen = 40
	// maxIDLen is the maximum length of the linked ids. Ids go into a unique
	// btree index; if an index entry exceeds ~2704 bytes PostgreSQL raises an
	// obscure error. The limit turns that into a readable validation error
	// (the prefixed ULID/KSUID ids of plan Section 8 are ~30 characters).
	maxIDLen = 255
)

// namePattern is the pattern link, module and field names must match.
//
// It is deliberately identical to the module-name pattern in
// internal/core/db: both prevent an unvalidated string from becoming an SQL
// identifier. Forbidding uppercase letters and quotes also closes the
// surprises that follow from PostgreSQL down-casing unquoted identifiers.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,` + fmt.Sprint(maxNameLen-1) + `}$`)

// reservedNames are the names that cannot be used as a link name.
//
// The name "definitions" would resolve to the same table as
// [definitionsTable]; a link would write over its own ledger. That is why the
// name is DERIVED from the ledger's name; if the ledger is renamed, the ban
// follows by itself.
var reservedNames = []string{strings.TrimPrefix(definitionsTable, tablePrefix)}

// Cardinality decides how many records a link may bind to.
//
// The zero value is [OneToOne]; that is, an undeclared cardinality picks the
// STRICTEST constraint. Were it the other way round (free ManyToMany as the
// zero value), a missing declaration would silently allow extra links and the
// mistake would only be noticed after the data was corrupted.
type Cardinality uint8

// Cardinality kinds.
const (
	// OneToOne is uniqueness on both ends: one fromID may bind to a single
	// toID, and one toID to a single fromID.
	OneToOne Cardinality = iota
	// OneToMany lets one fromID bind to many toIDs, but a toID may bind to
	// only a single fromID.
	OneToMany
	// ManyToMany is free; only the (fromID, toID) pair is unique.
	ManyToMany
)

// String returns the readable name of the Cardinality.
//
// This spelling is stable because it is WRITTEN into the ledger: had the
// numeric iota value been stored, inserting a new kind between the constants
// would silently shift the meaning of every definition already on disk.
func (c Cardinality) String() string {
	switch c {
	case OneToOne:
		return "one_to_one"
	case OneToMany:
		return "one_to_many"
	case ManyToMany:
		return "many_to_many"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(c))
	}
}

// Valid reports whether the value is a defined cardinality.
func (c Cardinality) Valid() bool {
	return c == OneToOne || c == OneToMany || c == ManyToMany
}

// LinkSide is one end of a link: which field of which module is being bound.
//
// Field is NOT A COLUMN NAME in the link table (see the [TableName] comments);
// it is metadata naming the counterpart of the bound id in the owning module.
// The Query layer (plan Section 5.3, ADR 0004) uses it to find which field of
// the root record feeds the link.
type LinkSide struct { //nolint:revive // the name comes from the binding contract in plan Section 5.2
	// Module is the name of the module owning this end (e.g. "pricing").
	Module string
	// Entity is this end's entity name in the Query layer (e.g. "price_set").
	//
	// It is SEPARATE from Module because one module can offer several
	// entities: the product module registers both a "product" and a "variant"
	// provider, while the pricing module's entity is "price_set". Query looks
	// up an expansion's target provider under the name
	// "<Entity>" + query.ProviderSuffix; writing the module name here means an
	// errors.NotFound at runtime.
	//
	// If left empty, Module is used — a module whose only entity carries its
	// own name does not have to fill the field in.
	//
	// Entity is NOT part of the link's IDENTITY and is not written to the
	// durable definition ledger: the link table's schema does not depend on
	// it, it only affects in-process query routing. A wrong value surfaces on
	// the first expansion as a clear NotFound naming the name that was looked
	// up.
	Entity string
	// Field is the name of the id field in that module (e.g. "price_set_id").
	Field string
}

// EntityName returns this end's entity name in the Query layer.
// It falls back to Module when Entity is empty.
func (s LinkSide) EntityName() string {
	if s.Entity != "" {
		return s.Entity
	}
	return s.Module
}

// String writes the end as "module.field".
func (s LinkSide) String() string {
	return s.Module + "." + s.Field
}

// LinkDefinition is the declaration of one relation between two modules.
//
// A definition is treated as IMMUTABLE: declaring a different definition under
// the same name produces an errors.Conflict (see [LinkService.Define]).
type LinkDefinition struct { //nolint:revive // the name comes from the binding contract in plan Section 5.2
	// Name is the link's unique name (e.g. "product_price"); the table name is
	// derived from it.
	Name string
	// From is the link's source end.
	From LinkSide
	// To is the link's target end.
	To LinkSide
	// Cardinality is the multiplicity of the relation; it is translated into a
	// database constraint.
	Cardinality Cardinality
}

// String writes the definition readably for error and log messages.
func (d LinkDefinition) String() string {
	return fmt.Sprintf("%s(%s -> %s, %s)", d.Name, d.From, d.To, d.Cardinality)
}

// Validate checks that the definition is consistent and can be turned safely
// into an SQL identifier.
//
// Every invalid case returns in the errors.Invalid class. Validation runs at
// the very start of Define; an invalid name NEVER reaches the database.
func (d LinkDefinition) Validate() error {
	if err := validateName(d.Name); err != nil {
		return err
	}
	if err := validateSide(d.From, "From"); err != nil {
		return err
	}
	if err := validateSide(d.To, "To"); err != nil {
		return err
	}
	if !d.Cardinality.Valid() {
		return errors.Invalid(codeCardinalityInvalid,
			"the cardinality of link %q is undefined (%s)", d.Name, d.Cardinality)
	}
	return nil
}

// validateName checks that a link name can be turned safely into a table name.
func validateName(name string) error {
	if !namePattern.MatchString(name) {
		return errors.Invalid(codeNameInvalid,
			"invalid link name %q (expected pattern: %s)", name, namePattern.String())
	}
	for _, reserved := range reservedNames {
		if name == reserved {
			return errors.Invalid(codeNameInvalid,
				"%q is a reserved link name; it collides with the %s table", name, definitionsTable)
		}
	}
	// In PostgreSQL, tables and indexes share the SAME namespace (pg_class).
	// A link named "x_from_uniq" resolves to the same relation name as the
	// uniqueness index of link "x"; in that case CREATE ... IF NOT EXISTS
	// raises a NOTICE rather than an error and SKIPS, meaning the cardinality
	// constraint is silently never created. The suffixes are derived from the
	// constants so that the ban follows a change in the naming scheme by
	// itself.
	for _, suffix := range []string{fromIndexSuffix, toIndexSuffix, toLookupSuffix} {
		if strings.HasSuffix(name, suffix) {
			return errors.Invalid(codeNameInvalid,
				"the name %q collides with the link index namespace (the %q suffix is reserved)", name, suffix)
		}
	}
	return nil
}

// validateSide validates the module and field names of one end. label says
// which end the error message is about.
func validateSide(side LinkSide, label string) error {
	if !namePattern.MatchString(side.Module) {
		return errors.Invalid(codeSideInvalid,
			"the module name of the %s end is invalid: %q (expected pattern: %s)",
			label, side.Module, namePattern.String())
	}
	if !namePattern.MatchString(side.Field) {
		return errors.Invalid(codeSideInvalid,
			"the field name of the %s end is invalid: %q (expected pattern: %s)",
			label, side.Field, namePattern.String())
	}
	return nil
}

// validateID checks that a bound id is usable.
//
// Ids reach SQL as PARAMETERS, so they carry no injection risk; the validation
// here exists to catch meaningless records (empty string, whitespace only,
// leading/trailing whitespace) and huge ids exceeding the index limit early.
// label says which end the error message is about.
//
// A padded id is NOT TRIMMED, it is rejected; for the reasoning see
// [LinkService].
func validateID(id, label string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return errors.Invalid(codeIDInvalid, "%s cannot be empty", label)
	}
	if trimmed != id {
		return errors.Invalid(codeIDInvalid, "%s cannot carry leading/trailing whitespace: %q", label, id)
	}
	if len(id) > maxIDLen {
		return errors.Invalid(codeIDInvalid,
			"%s can be at most %d bytes, %d bytes were given", label, maxIDLen, len(id))
	}
	return nil
}

// TableName builds the table name from a link name.
//
// The name is validated HERE, and on failure an empty name is returned
// together with errors.Invalid. Keeping the validation inside the function
// itself makes it structurally impossible for a name from outside to become a
// table name unvalidated (the same reasoning as internal/core/db
// MigrationsTable).
func TableName(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	return tablePrefix + name, nil
}

// LinkService allows cross-module links to be declared and managed
// (plan Section 5.2).
//
// All methods are goroutine-safe.
//
// # The id contract
//
// Bound ids are free-form strings (link knows no module's schema), but they
// cannot be empty, CANNOT CARRY LEADING/TRAILING WHITESPACE and cannot exceed
// the maximum length; a violation returns errors.Invalid. The whitespace ban
// closes a silent data loss: a link created with "var_1\n", which picked up a
// trailing newline from an external source (CSV, HTTP header, JSON), can NEVER
// be read back with the clean "var_1" — List returns an empty slice, which is
// not an error by contract, and since the compensation step's Delete is a
// no-op too the row would stay orphaned forever. The same drift would also
// effectively pierce the [OneToOne] and [OneToMany] constraints by counting
// "var_1" and "var_1\n" as two separate ends.
//
// An id is not trimmed silently: trimming separates the id the caller sent
// from the id we store, and the difference only becomes visible after the data
// is corrupted.
type LinkService interface { //nolint:revive // the name comes from the binding contract in plan Section 5.2
	// Define declares a link definition and creates its table (if absent).
	//
	// The call is idempotent: the same definition can be redeclared on every
	// startup. Declaring a DIFFERENT definition under the SAME NAME returns
	// errors.Conflict — because a definition change (cardinality above all)
	// requires migrating the existing data and cannot be done silently.
	Define(ctx context.Context, def LinkDefinition) error

	// Create links fromID with toID.
	//
	// Linking the same pair a second time is a NO-OP (not an error): saga
	// retries rerun the same step, and idempotency is a requirement of plan
	// Section 2.6. A CARDINALITY violation (the same end already bound to
	// another record), in contrast, returns errors.Conflict; that is a data
	// error, not a retry.
	Create(ctx context.Context, name, fromID, toID string) error

	// Delete removes the link between fromID and toID.
	//
	// If the link is already absent the call is a NO-OP: compensation steps
	// also run after a failed Create, and "absent" is precisely the desired
	// outcome.
	Delete(ctx context.Context, name, fromID, toID string) error

	// List returns the toIDs bound to fromID.
	//
	// The result is sorted ASCENDING by toID; with no links at all it returns
	// an empty slice (not nil). An undefined link name produces
	// errors.NotFound.
	List(ctx context.Context, name, fromID string) ([]string, error)

	// ListMany returns the links of several fromIDs in a SINGLE query.
	//
	// The Query layer (ADR 0004) batches expansions; calling List once per
	// root record would produce an N+1. The returned map holds only the
	// fromIDs that have at least one link; every slice is sorted ascending by
	// toID.
	ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)
	// ListManyByTo resolves the reverse direction in bulk: for each of the
	// given toIDs it returns the fromIDs bound to it.
	//
	// The Query layer uses this when the expansion's root entity sits on the
	// link's To end. Without it every reverse expansion either falls to a
	// query per record (N+1) or is not supported at all.
	ListManyByTo(ctx context.Context, name string, toIDs []string) (map[string][]string, error)

	// Definition returns the definition of the named link.
	//
	// The Query layer learns which module and which field a link resolves to
	// from here. An undefined name produces errors.NotFound.
	Definition(ctx context.Context, name string) (LinkDefinition, error)
}
