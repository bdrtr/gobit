package models

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bdrtr/gobit/core/errors"
)

// The bounds of a line's properties (ADR 0223).
const (
	// MaxLineProperties is how many properties one line carries.
	MaxLineProperties = 10
	// MaxPropertyNameLength and MaxPropertyValueLength are in characters.
	MaxPropertyNameLength  = 64
	MaxPropertyValueLength = 500
)

// CodePropertiesInvalid reports line properties out of their bounds.
const CodePropertiesInvalid = "cart_line_properties_invalid"

// NormalizeLineProperties checks what a shopper wrote on a line and returns it
// with each name and value trimmed; an empty set is nil.
//
// A name is 1 to [MaxPropertyNameLength] characters and a value 1 to
// [MaxPropertyValueLength], neither with a control character; two names that
// are the same once trimmed are refused rather than one of them dropped.
func NormalizeLineProperties(properties map[string]string) (map[string]string, error) {
	if len(properties) == 0 {
		return nil, nil
	}
	if len(properties) > MaxLineProperties {
		return nil, errors.Invalid(CodePropertiesInvalid,
			"a line carries at most %d properties, %d given", MaxLineProperties, len(properties))
	}
	out := make(map[string]string, len(properties))
	for name, value := range properties {
		cleanName, cleanValue := strings.TrimSpace(name), strings.TrimSpace(value)
		if err := checkProperty("name", cleanName, MaxPropertyNameLength); err != nil {
			return nil, err
		}
		if err := checkProperty("value of "+cleanName, cleanValue, MaxPropertyValueLength); err != nil {
			return nil, err
		}
		if _, taken := out[cleanName]; taken {
			return nil, errors.Invalid(CodePropertiesInvalid, "the property %q is named twice", cleanName)
		}
		out[cleanName] = cleanValue
	}
	return out, nil
}

// checkProperty bounds one name or value.
func checkProperty(what, text string, limit int) error {
	length := utf8.RuneCountInString(text)
	if length == 0 || length > limit {
		return errors.Invalid(CodePropertiesInvalid,
			"a property's %s is 1 to %d characters, %d given", what, limit, length)
	}
	if strings.IndexFunc(text, unicode.IsControl) >= 0 {
		return errors.Invalid(CodePropertiesInvalid, "a property's %s holds a control character", what)
	}
	return nil
}
