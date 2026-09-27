package webhookout

import (
	"slices"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The bounds of what a receiver narrows (ADR 0218).
const (
	// maxFilterValues is how many values one filtered field lists.
	maxFilterValues = 50
	// maxFilterValueLen is the longest value, in bytes: an id or a code.
	maxFilterValueLen = 128
)

// validateNarrowing checks a receiver's filters and fields against its topics
// and against the fields each topic carries, and returns them normalized:
// values trimmed and listed once, field lists in the order given and listed
// once. Nil comes back as empty.
//
// A field the topic does not carry is refused rather than kept. A filter on it
// would match no event and the receiver would hear nothing, with no error
// anywhere; a field list naming it would send a body without it.
func validateNarrowing(topics []string, filters topicFilters, fields topicFields) (topicFilters, topicFields, error) {
	outFilters := topicFilters{}
	for topic, byField := range filters {
		carried, err := narrowedTopic(topics, topic, "filters")
		if err != nil {
			return nil, nil, err
		}
		if len(byField) == 0 {
			return nil, nil, coreerrors.Invalid(codeInvalidRequest,
				"filters for %q name no field; leave the topic out to take all of its events", topic)
		}
		outFilters[topic] = map[string][]string{}
		for field, values := range byField {
			if !slices.Contains(carried, field) {
				return nil, nil, unknownField(topic, field, carried)
			}
			normalized, err := filterValues(topic, field, values)
			if err != nil {
				return nil, nil, err
			}
			outFilters[topic][field] = normalized
		}
	}

	outFields := topicFields{}
	for topic, names := range fields {
		carried, err := narrowedTopic(topics, topic, "fields")
		if err != nil {
			return nil, nil, err
		}
		if len(names) == 0 {
			return nil, nil, coreerrors.Invalid(codeInvalidRequest,
				"fields for %q name no field; leave the topic out to be sent all of it", topic)
		}
		var kept []string
		for _, name := range names {
			name = strings.TrimSpace(name)
			if !slices.Contains(carried, name) {
				return nil, nil, unknownField(topic, name, carried)
			}
			if !slices.Contains(kept, name) {
				kept = append(kept, name)
			}
		}
		outFields[topic] = kept
	}

	return outFilters, outFields, nil
}

// narrowedTopic returns the fields a narrowed topic carries, refusing a topic
// the receiver does not take.
func narrowedTopic(topics []string, topic, what string) ([]string, error) {
	if !slices.Contains(topics, topic) {
		return nil, coreerrors.Invalid(codeInvalidRequest,
			"%s name %q, which is not among the receiver's topics (%s)", what, topic, strings.Join(topics, ", "))
	}

	return TopicFields[topic], nil
}

// filterValues checks and normalizes one filtered field's values.
func filterValues(topic, field string, values []string) ([]string, error) {
	if len(values) == 0 || len(values) > maxFilterValues {
		return nil, coreerrors.Invalid(codeInvalidRequest,
			"the filter on %s's %s lists 1 to %d values, %d given", topic, field, maxFilterValues, len(values))
	}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > maxFilterValueLen {
			return nil, coreerrors.Invalid(codeInvalidRequest,
				"a value of the filter on %s's %s is empty or longer than %d bytes", topic, field, maxFilterValueLen)
		}
		if !slices.Contains(out, value) {
			out = append(out, value)
		}
	}

	return out, nil
}

// unknownField refuses a field the topic does not carry, naming those it does.
func unknownField(topic, field string, carried []string) error {
	return coreerrors.Invalid(codeInvalidRequest,
		"%q is not a field %s carries to a receiver; it carries: %s", field, topic, strings.Join(carried, ", "))
}
