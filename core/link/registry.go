package link

import (
	"fmt"
	"maps"
	"slices"
	"sync"
)

// linkTable is the runtime information of a single declared link: the
// definition, the table name, the names of the indexes enforcing cardinality
// and the pre-built SQL statements.
//
// Building the statements HERE, once at Define time, is deliberate: because a
// table name cannot be parameterized in SQL the statements are assembled by
// string concatenation, and that concatenation is fed ONLY from a validated
// definition (see [LinkDefinition.Validate]). The Create/Delete/List paths use
// the ready statement; no string given by a caller reaches SQL text, they all
// travel as parameters.
type linkTable struct {
	def       LinkDefinition
	table     string
	fromIndex string
	toIndex   string

	insert   string
	remove   string
	list     string
	listMany string
	// listManyByTo resolves the reverse direction (to_id -> from_id) in bulk.
	// The Query layer uses it when the root entity sits on the link's To end
	// (see ADR 0004).
	listManyByTo string
	// lookupIndex is the NON-UNIQUE index that keeps the reverse-direction
	// query from doing a table scan under ManyToMany. Under the other
	// cardinalities to_id already carries a unique index.
	lookupIndex string
}

// newLinkTable builds the runtime information from a validated definition.
// The caller must have validated def beforehand.
func newLinkTable(def LinkDefinition) (*linkTable, error) {
	table, err := TableName(def.Name)
	if err != nil {
		return nil, err
	}

	return &linkTable{
		def:       def,
		table:     table,
		fromIndex: table + fromIndexSuffix,
		toIndex:   table + toIndexSuffix,

		// The ON CONFLICT target is EXPLICITLY (from_id, to_id). A targetless
		// "ON CONFLICT DO NOTHING" would also swallow violations of the
		// indexes enforcing cardinality; binding the same record to two
		// different targets would then be a silent loss rather than an error.
		insert: fmt.Sprintf(
			`INSERT INTO %s (from_id, to_id) VALUES ($1, $2) ON CONFLICT (from_id, to_id) DO NOTHING`,
			table),
		remove: fmt.Sprintf(`DELETE FROM %s WHERE from_id = $1 AND to_id = $2`, table),
		// The ordering must be deterministic so that API responses and tests
		// are reproducible; since (from_id, to_id) is the primary key,
		// ordering by to_id gives a TOTAL order (there can be no ties).
		list: fmt.Sprintf(`SELECT to_id FROM %s WHERE from_id = $1 ORDER BY to_id`, table),
		listMany: fmt.Sprintf(
			`SELECT from_id, to_id FROM %s WHERE from_id = ANY($1) ORDER BY from_id, to_id`, table),
		listManyByTo: fmt.Sprintf(
			`SELECT to_id, from_id FROM %s WHERE to_id = ANY($1) ORDER BY to_id, from_id`, table),
		lookupIndex: table + toLookupSuffix,
	}, nil
}

// requiredIndexes returns the names of the indexes that enforce cardinality.
//
// It is derived from the SAME switch as [linkTable.ddl]; if the two drift
// apart, the verification looks for the wrong thing. Adding a new cardinality
// requires updating both.
func (lt *linkTable) requiredIndexes() []string {
	switch lt.def.Cardinality {
	case OneToOne:
		return []string{lt.fromIndex, lt.toIndex}
	case OneToMany:
		return []string{lt.toIndex}
	case ManyToMany:
		return []string{lt.lookupIndex}
	default:
		return nil
	}
}

// ddl returns the statements that create the link table and its cardinality
// constraints.
//
// All of them are IF NOT EXISTS: Define is called again on every startup and
// finding an existing schema is the normal case.
//
// A link table REFERENCES no module's table (plan Section 2.2); the ids are
// free text. Whether the record a link points at really exists is the owning
// module's and the workflow compensation's responsibility.
func (lt *linkTable) ddl() []string {
	stmts := []string{fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
	from_id    TEXT NOT NULL,
	to_id      TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY (from_id, to_id)
)`, lt.table)}

	// The primary key (from_id, to_id) already gives uniqueness of the pair;
	// the indexes below NARROW the cardinality.
	switch lt.def.Cardinality {
	case OneToOne:
		// Each end may hold a single link.
		stmts = append(stmts,
			uniqueIndexDDL(lt.fromIndex, lt.table, "from_id"),
			uniqueIndexDDL(lt.toIndex, lt.table, "to_id"))
	case OneToMany:
		// One fromID may bind to many toIDs; one toID belongs to a single
		// fromID.
		stmts = append(stmts, uniqueIndexDDL(lt.toIndex, lt.table, "to_id"))
	case ManyToMany:
		// There is no cardinality constraint, but the reverse-direction query
		// (to_id = ANY(...)) would fall to a table scan without an index.
		// Under OneToOne/OneToMany to_id already carries a unique index.
		stmts = append(stmts, fmt.Sprintf(
			`CREATE INDEX IF NOT EXISTS %s ON %s (to_id)`, lt.lookupIndex, lt.table))
	}
	return stmts
}

// uniqueIndexDDL builds the statement creating a unique index on the given
// column.
func uniqueIndexDDL(indexName, table, column string) string {
	return fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS %s ON %s (%s)`, indexName, table, column)
}

// definitions is the in-process registry of declared links.
//
// The registry does two jobs: (a) it catches a different definition under the
// same name without going to the database, and (b) it makes Create/Delete/List
// calls work only on DECLARED links — so the table name that reaches SQL
// always comes from a validated definition.
//
// An in-process registry is NOT ENOUGH: if another release/process declares
// the same name differently, only the durable ledger (see definitionsTable)
// catches it. The two work together; the copy here is the fast path.
type definitions struct {
	mu     sync.RWMutex
	byName map[string]*linkTable
}

// newDefinitions builds an empty registry.
func newDefinitions() *definitions {
	return &definitions{byName: make(map[string]*linkTable)}
}

// lookup returns the runtime information of the named link.
func (d *definitions) lookup(name string) (*linkTable, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	lt, ok := d.byName[name]
	return lt, ok
}

// put writes the link into the registry, overwriting an existing record.
func (d *definitions) put(lt *linkTable) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.byName[lt.def.Name] = lt
}

// names returns the declared link names sorted; it is used in error messages.
func (d *definitions) names() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return slices.Sorted(maps.Keys(d.byName))
}
