package openapi

import (
	"encoding"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
)

// schemaFieldData is the name of the envelopes' record-carrying field (plan Section 8).
const schemaFieldData = "data"

// The names of the list envelope's pagination fields (plan Section 8).
//
// They are constants not to avoid repetition but to avoid DRIFT: the names
// appear both in the "properties" map and in the "required" list, and a name
// fixed in one and forgotten in the other would produce a field declared
// required and never written — a lie invisible without reading the schema line
// by line.
const (
	schemaFieldCount  = "count"
	schemaFieldOffset = "offset"
	schemaFieldLimit  = "limit"
)

// codeSchemaNameConflict reports that two DIFFERENT Go types want the same
// component name.
const codeSchemaNameConflict = "openapi_schema_name_conflict"

// The types that have to be recognized through reflection.
//
// They are kept at package level not for cost but because of the repetition
// itself: writing the [reflect.TypeOf] call out again in every field would stay
// SILENT when one of them named the wrong type.
var (
	// timeType is time.Time's reflect type.
	timeType = reflect.TypeOf(time.Time{})
	// rawJSONType is json.RawMessage's reflect type.
	rawJSONType = reflect.TypeOf(json.RawMessage(nil))
	// marshalerType is the reflect type of the json.Marshaler interface.
	marshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	// textMarshalerType is the reflect type of the encoding.TextMarshaler interface.
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

// reservedSchemaNames are the names of the core's own components.
//
// Were a derived schema to take one of them it would silently overwrite the
// schema of the SHARED error envelope or of the list envelope: every endpoint in
// the document refers to the same "Error" component, so one module's DTO named
// "Error" would describe EVERY error response wrongly.
var reservedSchemaNames = map[string]struct{}{
	schemaNameError: {},
	schemaNameList:  {},
}

// SchemaOf produces a JSON Schema from the Go TYPE of the given value.
//
// # Why reflection
//
// The reason is [Doc.Build]'s: a hand-written field list falls behind the day a
// field is added to the DTO and nobody notices. The only thing that knows what
// goes on the wire is encoding/json, so the schema is derived from its
// BEHAVIOR — the json tag, shadowing, omitempty and the unexported-field rules
// are imitated here exactly. Where the imitation is incomplete a schema is WORSE
// than no schema: the client sends a field name it believed was right.
//
// # Named structs are components
//
// A struct with a name is registered under components/schemas and only a "$ref"
// is returned here. There are two reasons. The first is RECURSION: a
// self-referencing type (a category tree, say) written inline would send the
// generator into an endless loop. A depth limit was an option too, but where it
// cut it would have to write "anything goes", and that turns a typed field into
// 'any' in a client generator — precisely the lie being avoided. A $ref breaks
// the cycle without any limit at all. The second is REPETITION: a DTO appearing
// at twenty endpoints shows up once in the document and the client generator
// produces a single class.
//
// # A known limit
//
// The ",string" option in a json tag (writing a number as a JSON STRING) is NOT
// imitated; such a field appears as a number in the schema. It is used nowhere
// in this repository and every extra branch is one more place that can be wrong.
// Writing the limit down is deliberate: knowing it is missing beats believing it
// is not.
func (d *Doc) SchemaOf(v any) map[string]any {
	return d.schemaOfType(reflect.TypeOf(v), map[reflect.Type]bool{})
}

// Schemas returns the derived component schemas.
//
// [Doc.Build] writes them under components/schemas; they are also exported
// because [Doc.SchemaOf] returns a "$ref" — a caller (and a test) wanting to see
// what the reference points at has to be able to read the map.
func (d *Doc) Schemas() map[string]any {
	duplicate := make(map[string]any, len(d.schemas))
	for name, schema := range d.schemas {
		duplicate[name] = schema
	}

	return duplicate
}

// Item produces the schema of the single-object response envelope from the
// RECORD type.
//
// The envelope shape is fixed in plan Section 8: {schemaFieldData: <record>}.
// Every module writing its own envelope meant the others going stale silently
// the day the shape changed.
func (d *Doc) Item(v any) map[string]any {
	return itemSchema(d.SchemaOf(v))
}

// List produces the schema of the list response envelope from the RECORD type.
//
// The envelope shape is fixed in plan Section 8:
// {schemaFieldData: [...], "count": N, "offset": N, "limit": N}. All four fields
// are REQUIRED; for the endpoint whose counter is optional there is
// [Doc.ListOptionalCount].
func (d *Doc) List(v any) map[string]any {
	oge := d.schemaOfType(listRecord(reflect.TypeOf(v)), map[reflect.Type]bool{})

	return listSchema(oge, true)
}

// ListOptionalCount produces the list envelope's schema with the "count" field
// as one that MAY BE ABSENT.
//
// The field stays in the schema and its type is still an integer; the only thing
// that changes is that it is NOT IN the required list. That is OpenAPI's word
// for "may not be there", and it turns the field into an optional one in the
// generated clients — that is, the caller has to ask whether the number exists
// before reading it.
//
// # Why a separate function
//
// Loosening [Doc.List] would make the documentation of the dozens of endpoints
// that ALWAYS write the counter a lie too: the client generator would produce an
// optional field there as well and make people write a check for a case that
// never happens. The document has to say what the endpoint really does; "the
// loosest schema for all" is easy and wrong.
//
// # Why NOT nullable
//
// Making the field "integer|null" was possible and was refused: with the counter
// off the field comes back NOT as null but not WRITTEN at all (see the product
// api listEnvelope) — writing nullable would describe a value that never appears
// in the body.
func (d *Doc) ListOptionalCount(v any) map[string]any {
	oge := d.schemaOfType(listRecord(reflect.TypeOf(v)), map[reflect.Type]bool{})

	return listSchema(oge, false)
}

// RequestBody produces a REQUIRED JSON request body definition from the given type.
func (d *Doc) RequestBody(v any) map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{"schema": d.SchemaOf(v)},
		},
	}
}

// Response produces a JSON response definition with the given schema.
//
// It is used together with [Doc.Item] and [Doc.List]; it stands apart so the
// same envelope can be described with different status codes (200/201).
func Response(description string, schema map[string]any) map[string]any {
	return map[string]any{
		schemaDescription: description,
		"content": map[string]any{
			"application/json": map[string]any{"schema": schema},
		},
	}
}

// listRecord returns the RECORD type of the value given to [Doc.List].
//
// Both List(Product{}) and List([]Product{}) say the same thing. Used as it is,
// the second form would wrap the slice schema in another array and the document
// would carry an "array of arrays"; nobody would notice that without reading the
// schema line by line. A byte slice is OUTSIDE this: encoding/json writes it as
// a base64 STRING, not as an array.
func listRecord(t reflect.Type) reflect.Type {
	if t == nil {
		return nil
	}

	if k := t.Kind(); (k == reflect.Slice || k == reflect.Array) && !isByteSlice(t) {
		return t.Elem()
	}

	return t
}

// schemaOfType produces the schema of a single type.
//
// seen holds the types on the CURRENT recursion path and is needed only for
// self-referencing types that cannot go into a $ref (non-structs); struct cycles
// are already broken by the component registration (see [Doc.SchemaOf]).
func (d *Doc) schemaOfType(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	if t == nil {
		// There is nothing true to say about a value with no type (a nil
		// interface); a free schema is the honest way of saying "I do not know".
		return map[string]any{}
	}

	// A pointer is handled FIRST. The encoder check below would catch the
	// pointer too and the result would be WRONG: time.Time's MarshalJSON has a
	// VALUE receiver, so *time.Time carries it as well and we would say "the
	// shape is unknown" and write a free schema. But on the wire *time.Time is
	// either an RFC 3339 string or null, and both can be stated.
	if t.Kind() == reflect.Pointer {
		return d.pointerSchema(t, seen)
	}

	switch {
	case t == timeType:
		// time.Time's fields are unexported; reflection would take it for an
		// EMPTY object. On the wire it is an RFC 3339 string.
		return map[string]any{schemaType: typeString, schemaFormat: formatDateTime}
	case t == rawJSONType:
		// The shape of raw JSON is unknown by definition.
		return map[string]any{}
	case t.Implements(marshalerType):
		// If a type carries its own encoder, reading its fields would be a LIE:
		// MarshalJSON can write whatever it likes.
		//
		// A MarshalJSON with a POINTER receiver is not looked for on a value type;
		// encoding/json does not call it on an unaddressable value either (the
		// classic trap), so the schema stays the same as the behavior on the wire.
		return map[string]any{}
	case t.Implements(textMarshalerType):
		// A type carrying TextMarshaler is written to JSON as a STRING.
		return map[string]any{schemaType: typeString}
	}

	if k := t.Kind(); k == reflect.Slice || k == reflect.Array || k == reflect.Map {
		if seen[t] {
			// A self-referencing named slice/map cannot go into a component
			// (components are for structs) and written inline it would recurse
			// forever. A free schema is the most honest way of saying "from here
			// on it repeats itself".
			return map[string]any{}
		}

		seen[t] = true
		defer delete(seen, t)
	}

	return d.schemaOfKind(t, seen)
}

// pointerSchema produces the schema of a pointer type.
//
// When the encoder is ONLY on the pointer (a MarshalJSON with a pointer
// receiver), descending to the element would be a lie: on such a field
// encoding/json really does call that encoder and we cannot know its wire shape
// from here.
func (d *Doc) pointerSchema(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	oge := t.Elem()

	if hasCustomEncoder(t) && !hasCustomEncoder(oge) {
		return map[string]any{}
	}

	if seen[t] {
		return map[string]any{}
	}

	seen[t] = true
	defer delete(seen, t)

	return nullable(d.schemaOfType(oge, seen))
}

// hasCustomEncoder reports whether the type decides its own JSON shape.
func hasCustomEncoder(t reflect.Type) bool {
	return t.Implements(marshalerType) || t.Implements(textMarshalerType)
}

// schemaOfKind produces a type's schema from its Go Kind.
func (d *Doc) schemaOfKind(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{schemaType: typeBoolean}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return integerSchema(t)
	case reflect.Float32:
		return map[string]any{schemaType: typeNumber, schemaFormat: formatFloat}
	case reflect.Float64:
		return map[string]any{schemaType: typeNumber, schemaFormat: formatDouble}
	case reflect.String:
		return map[string]any{schemaType: typeString}
	case reflect.Slice, reflect.Array:
		if isByteSlice(t) {
			// encoding/json writes a byte slice as a base64 STRING; saying "array"
			// would have the client expect a list of numbers.
			return map[string]any{schemaType: typeString, schemaFormat: formatByte}
		}

		return map[string]any{schemaType: typeArray, schemaItems: d.schemaOfType(t.Elem(), seen)}
	case reflect.Map:
		// The key type does not enter the schema: a JSON object key is always a
		// string and encoding/json turns numeric/TextMarshaler keys into strings too.
		return map[string]any{
			schemaType:                 typeObject,
			schemaAdditionalProperties: d.schemaOfType(t.Elem(), seen),
		}
	case reflect.Struct:
		return d.structSchemaOrRef(t, seen)
	case reflect.Pointer:
		return nullable(d.schemaOfType(t.Elem(), seen))
	case reflect.Interface:
		return map[string]any{}
	case reflect.Invalid, reflect.Complex64, reflect.Complex128, reflect.Chan,
		reflect.Func, reflect.UnsafePointer:
		// encoding/json cannot encode these types; the only true thing a schema
		// can do is CLAIM NOTHING.
		return map[string]any{}
	default:
		return map[string]any{}
	}
}

// integerSchema returns an integer type's schema along with its width.
//
// The width ("format") is not decoration: an int64 is a JSON number and
// JavaScript breaks it SILENTLY past 2^53. Seeing the format, client generators
// pick a 64-bit type (long, BigInt).
func integerSchema(t reflect.Type) map[string]any {
	schema := map[string]any{schemaType: typeInteger}

	switch t.Bits() {
	case 32:
		schema[schemaFormat] = formatInt32
	case 64:
		schema[schemaFormat] = formatInt64
	}

	return schema
}

// isByteSlice reports whether the type is a byte slice, which encoding/json
// writes as a base64 STRING.
func isByteSlice(t reflect.Type) bool {
	if t.Kind() != reflect.Slice {
		return false
	}

	oge := t.Elem()
	if oge.Kind() != reflect.Uint8 {
		return false
	}

	// If the element type has its own encoder, encoding/json does not fall to
	// base64 and writes the array element by element.
	return !oge.Implements(marshalerType) && !oge.Implements(textMarshalerType)
}

// nullable adds JSON null to the schema.
//
// A pointer type being able to be nil is not an accident but the author's
// choice: the only reason to make a field a pointer is to say "it may be
// absent". Slices and maps are DELIBERATELY not made nullable; their nilness is
// an accident of Go's zero value and the API contract counts them as EMPTY. The
// trade is accepted openly: a slice field left nil comes out as null on the wire
// and the schema does not say so — in exchange we do not make every client write
// a "any list may be null" branch.
func nullable(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		// A free schema already accepts null.
		return schema
	}

	if _, isRef := schema[schemaRef]; isRef {
		// A type written next to a "$ref" is evaluated TOGETHER with the $ref in
		// JSON Schema 2020-12: null would have to match both the target schema and
		// the type, and no value could pass. The right form is anyOf.
		return map[string]any{schemaAny: []any{schema, map[string]any{schemaType: typeNull}}}
	}

	switch typ := schema[schemaType].(type) {
	case string:
		schema[schemaType] = []any{typ, typeNull}
	case []any:
		for _, existing := range typ {
			if existing == typeNull {
				return schema
			}
		}

		schema[schemaType] = append(typ, typeNull)
	}

	return schema
}

// componentName turns a Go type name into the PUBLISHED schema component name.
//
// # Why the Go name cannot be used as it is
//
// A component name is NOT AN INTERNAL DETAIL but a PUBLISHED CONTRACT: client
// generators produce class names from it, and once a client has been generated
// changing the name is breaking. Used as it is, the contract would depend on
// Go's export rule and on a package's naming habits: "StoreProduct" (from the
// models package, exported) would stand next to "cartDTO" (from the api package,
// unexported) in the same document. In the generated client one class would be
// StoreProduct and the other cartDTO — two different naming schemes for one API.
//
// Two normalizations are made and both are LOSSLESS:
//
//   - The first letter is upper-cased: being unexported is a Go concept, not an
//     HTTP contract one.
//   - A trailing "DTO" is dropped: being a transfer object is a Go concept too.
//     A client wants "Cart", not "cartDTO".
//
// There is a clash risk (were there both a "cartDTO" and a "Cart" type, both
// would want "Cart") and it is NOT SILENT: [Doc.reportClash] reports that two
// types want the same name and building the document returns an error. A silent
// overwrite would mean one DTO's schema describing another type.
func componentName(goTypeName string) string {
	if goTypeName == "" {
		return ""
	}

	name := strings.TrimSuffix(goTypeName, "DTO")
	if name == "" {
		// A type named just "DTO"; trimming would erase it.
		name = goTypeName
	}

	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])

	return string(r)
}

// structSchemaOrRef registers a named struct as a component and returns a "$ref".
func (d *Doc) structSchemaOrRef(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	name := componentName(t.Name())
	if name == "" {
		// An anonymous struct has no name and cannot go into a component — but it
		// cannot reference ITSELF either, so writing it inline is safe.
		return d.structSchema(t, seen)
	}

	if _, reserved := reservedSchemaNames[name]; reserved {
		d.reportClash(name + " belongs to the core's shared component; " + t.PkgPath() + " cannot use this name")

		return map[string]any{}
	}

	if owner, registered := d.schemaOwners[name]; registered {
		if owner != t {
			d.reportClash("two types want the name " + name + ": " + owner.PkgPath() + " and " + t.PkgPath())

			return map[string]any{}
		}

		return refSchema(name)
	}

	// The registration happens BEFORE descending into the fields: when a
	// self-referencing type arrives back here it finds the name and returns a
	// $ref. In the other order the recursion would never end.
	d.schemaOwners[name] = t
	d.schemas[name] = map[string]any{}
	d.schemas[name] = d.structSchema(t, seen)
	// The component set is the document's second input; a document already built
	// is now incomplete (see [Doc.Handler]).
	d.describeVersion++

	return refSchema(name)
}

// structSchema builds the object schema from the struct's fields.
func (d *Doc) structSchema(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	properties := map[string]any{}

	var required []string

	for _, a := range jsonFields(t) {
		properties[a.name] = d.schemaOfType(a.typ, seen)

		if !a.optional {
			required = append(required, a.name)
		}
	}

	schema := map[string]any{schemaType: typeObject, schemaProperties: properties}

	if len(required) > 0 {
		sort.Strings(required)
		schema[schemaRequired] = required
	}

	return schema
}

// reportClash records a component name clash.
//
// A clash makes [Doc.Build] FAIL; overwriting silently would mean one of two
// endpoints having the wrong schema, and that being noticed only when a client
// sent the wrong field.
func (d *Doc) reportClash(message string) {
	for _, existing := range d.schemaClashes {
		if existing == message {
			return
		}
	}

	d.schemaClashes = append(d.schemaClashes, message)
	// A clash makes the document unbuildable; the sound document in the cache is
	// now INVALID (see [Doc.Handler]).
	d.describeVersion++
}

// refSchema produces a schema referring to a component.
func refSchema(name string) map[string]any {
	return map[string]any{schemaRef: "#/components/schemas/" + name}
}

// field is what schema generation needs to know about a struct field.
type field struct {
	// name is the field's name in JSON.
	name string
	// typ is the field's Go type (the pointer wrapper is KEPT; nullable derives
	// from it).
	typ reflect.Type
	// depth is how deeply the field is embedded; the shallower wins on shadowing.
	depth int
	// tagged reports that the field was NAMED by a json tag; at equal depth a
	// tagged field beats an untagged one.
	tagged bool
	// optional reports that the field may drop out of the JSON (omitempty/omitzero).
	optional bool
}

// jsonFields returns the field set encoding/json WOULD PRODUCE for a struct.
//
// The implementation follows encoding/json's typeFields algorithm and carries
// two of its rules exactly:
//
//   - The fields of EMBEDDED structs are flattened (an embedded field named by a
//     json tag is a plain field and is not flattened).
//   - SHADOWING: of two fields with the same name the SHALLOWER wins; at equal
//     depth a single tagged one wins, otherwise they ALL DROP.
//
// The second rule touches this package's reason to exist: service.StoreProduct
// shadows the embedded Product's Variants field and encoding/json writes only
// the shadowing one. Were the schema to write the shadowed field, a client
// generator would produce the product variants with the WRONG type.
func jsonFields(t reflect.Type) []field {
	collector := &fieldCollector{nextCount: map[reflect.Type]int{}}

	seen := map[reflect.Type]bool{}
	valid := []reflect.Type{}
	next := []reflect.Type{t}

	// count is declared WITHOUT an assignment: the loop's first turn swaps it
	// with the collector's map, so any value put here would be thrown away
	// unread. The declaration is assignment-free in encoding/json's own
	// algorithm too.
	var count map[reflect.Type]int

	for depth := 0; len(next) > 0; depth++ {
		valid, next = next, valid[:0]
		count, collector.nextCount = collector.nextCount, map[reflect.Type]int{}
		collector.next = next

		for _, typ := range valid {
			if seen[typ] {
				continue
			}

			seen[typ] = true
			collector.walk(typ, depth, count[typ])
		}

		next = collector.next
	}

	return unshadowed(collector.found)
}

// fieldCollector is the state of one level of the embedded-struct walk.
type fieldCollector struct {
	// found are the fields gathered so far (before shadowing is applied).
	found []field
	// next are the embedded struct types to walk at the next level.
	next []reflect.Type
	// nextCount holds how many paths from this level reach an embedded type.
	nextCount map[reflect.Type]int
}

// walk gathers one struct's fields and queues its embedded ones.
//
// repeats is how many separate paths reached this type from the PREVIOUS level.
func (c *fieldCollector) walk(t reflect.Type, depth, repeats int) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)

		embeddedType := sf.Type
		if embeddedType.Kind() == reflect.Pointer {
			embeddedType = embeddedType.Elem()
		}

		if !fieldVisible(sf, embeddedType) {
			continue
		}

		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}

		name, options, _ := strings.Cut(tag, ",")
		if !isValidTagName(name) {
			name = ""
		}

		// An embedded field with a name, or one that is NOT a struct, is a plain field.
		if name != "" || !sf.Anonymous || embeddedType.Kind() != reflect.Struct {
			tagged := name != ""
			if name == "" {
				name = sf.Name
			}

			c.found = append(c.found, field{
				name:     name,
				typ:      sf.Type,
				depth:    depth,
				tagged:   tagged,
				optional: hasOption(options, "omitempty") || hasOption(options, "omitzero"),
			})

			// If the same embedded type was reached by several paths at this level
			// the field shows up several times too; adding the copy lets the
			// dropping rule (see [unshadowed]) see the ambiguity.
			if repeats > 1 {
				c.found = append(c.found, c.found[len(c.found)-1])
			}

			continue
		}

		c.nextCount[embeddedType]++
		if c.nextCount[embeddedType] == 1 {
			c.next = append(c.next, embeddedType)
		}
	}
}

// fieldVisible reports whether encoding/json handles the field at all.
//
// Unexported fields are not written — the ONLY exception is the EMBEDDED form of
// an unexported type: encoding/json writes the EXPORTED fields inside it, so the
// schema has to as well.
func fieldVisible(sf reflect.StructField, embeddedType reflect.Type) bool {
	if sf.Anonymous {
		return sf.IsExported() || embeddedType.Kind() == reflect.Struct
	}

	return sf.IsExported()
}

// unshadowed picks the winner among fields wanting the same name.
func unshadowed(found []field) []field {
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]

		if a.name != b.name {
			return a.name < b.name
		}

		if a.depth != b.depth {
			return a.depth < b.depth
		}

		return a.tagged && !b.tagged
	})

	var result []field

	for i := 0; i < len(found); {
		j := i + 1
		for j < len(found) && found[j].name == found[i].name {
			j++
		}

		if winner, ok := dominantField(found[i:j]); ok {
			result = append(result, winner)
		}

		i = j
	}

	return result
}

// dominantField returns the winner among candidates with the same name.
//
// The candidates are sorted shallow to deep, and at equal depth the tagged ones
// first. If the first two candidates tie on both depth and taggedness there is
// NO winner: encoding/json writes such an ambiguous field NOT AT ALL, and the
// schema must not either.
func dominantField(candidates []field) (field, bool) {
	if len(candidates) > 1 &&
		candidates[0].depth == candidates[1].depth &&
		candidates[0].tagged == candidates[1].tagged {
		return field{}, false
	}

	return candidates[0], true
}

// isValidTagName reports whether the name in a json tag is accepted by
// encoding/json.
//
// A name that is not accepted is IGNORED and the field is written under its Go
// name; the schema has to do the same.
func isValidTagName(s string) bool {
	if s == "" {
		return false
	}

	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
			// The punctuation encoding/json explicitly allows.
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}

	return true
}

// hasOption reports whether an option is present in the option list of a json
// tag.
func hasOption(options, name string) bool {
	for options != "" {
		var s string

		s, options, _ = strings.Cut(options, ",")
		if s == name {
			return true
		}
	}

	return false
}

// itemSchema builds the single-object response envelope with the given record schema.
func itemSchema(rec map[string]any) map[string]any {
	return map[string]any{
		schemaType:       typeObject,
		schemaRequired:   []string{schemaFieldData},
		schemaProperties: map[string]any{schemaFieldData: rec},
	}
}

// listSchema builds the list response envelope with the given item schema.
//
// With countRequired false, "count" only drops out of the required list; nothing
// changes in the properties or in its type (see [Doc.ListOptionalCount]).
func listSchema(oge map[string]any, countRequired bool) map[string]any {
	required := []string{schemaFieldData}
	if countRequired {
		required = append(required, schemaFieldCount)
	}

	required = append(required, schemaFieldOffset, schemaFieldLimit)

	return map[string]any{
		schemaType:     typeObject,
		schemaRequired: required,
		schemaProperties: map[string]any{
			schemaFieldData:   map[string]any{schemaType: typeArray, schemaItems: oge},
			schemaFieldCount:  map[string]any{schemaType: typeInteger},
			schemaFieldOffset: map[string]any{schemaType: typeInteger},
			schemaFieldLimit:  map[string]any{schemaType: typeInteger},
		},
	}
}
