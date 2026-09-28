package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
)

// AddOn is one add-on a line carries, as its identity reads it: the variant
// and the words the shopper wrote on it (ADR 0229).
type AddOn struct {
	VariantID  string
	Properties map[string]string
}

// AddOnKey is the digest of the add-ons a line standing on its own carries,
// which is part of what the line is: the same ring with an engraving and the
// same ring without one are two lines, and the same ring with the same
// engraving added twice is one line of two. The add-ons are read in no order,
// and none is the empty key every line without add-ons carries.
//
// The properties have to be normalized first ([NormalizeLineProperties]), so
// two spellings of one engraving digest alike.
func AddOnKey(addOns []AddOn) string {
	if len(addOns) == 0 {
		return ""
	}
	entries := make([]string, 0, len(addOns))
	for _, addOn := range addOns {
		// JSON prints a map's keys sorted, so equal properties print alike.
		words, _ := json.Marshal(addOn.Properties)
		entries = append(entries, addOn.VariantID+"\x00"+string(words))
	}
	slices.Sort(entries)
	sum := sha256.Sum256([]byte(strings.Join(entries, "\x01")))
	return hex.EncodeToString(sum[:])
}
