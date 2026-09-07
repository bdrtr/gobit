package openapi

// This file holds the imitation of encoding/json's own field resolution:
// walking embedded structs level by level, reading the json tag, and deciding
// which of several fields wanting the same name is actually written. None of
// it knows anything about OpenAPI: it answers one question — "what does
// encoding/json put on the wire for this struct" — and that is the only thing
// the schema derivation asks it. Keeping it apart keeps the answer checkable
// against the standard library's rules rather than against the document being
// built.

import (
	"reflect"
	"sort"
	"strings"
	"unicode"
)

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
