package webhookout

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// TestANarrowingIsNormalized is ADR 0218: values trimmed and listed once,
// field lists in the order given and listed once, nothing given nothing kept.
func TestANarrowingIsNormalized(t *testing.T) {
	t.Parallel()

	filters, fields, err := validateNarrowing(
		[]string{topicCartCreated, topicOrderPlaced},
		topicFilters{topicCartCreated: {"region_id": {" reg_1 ", "reg_2", "reg_1"}}},
		topicFields{topicOrderPlaced: {"total", " order_id", "total"}},
	)

	require.NoError(t, err)
	assert.Equal(t, topicFilters{topicCartCreated: {"region_id": {"reg_1", "reg_2"}}}, filters)
	assert.Equal(t, topicFields{topicOrderPlaced: {"total", "order_id"}}, fields)

	filters, fields, err = validateNarrowing([]string{topicCartCreated}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, topicFilters{}, filters, "stored as an empty object, not null")
	assert.Equal(t, topicFields{}, fields)
}

// TestANarrowingThatWouldMatchNothingIsRefused: every refusal is invalid input
// that names what to write instead.
func TestANarrowingThatWouldMatchNothingIsRefused(t *testing.T) {
	t.Parallel()

	topics := []string{topicCartCreated, topicOrderPlaced}
	many := make([]string, maxFilterValues+1)
	for i := range many {
		many[i] = strings.Repeat("r", i+1)
	}
	for name, c := range map[string]struct {
		filters topicFilters
		fields  topicFields
		says    string
	}{
		"a filter on a topic not taken":      {filters: topicFilters{topicProductCreated: {"product_id": {"p"}}}, says: "not among"},
		"a field list for a topic not taken": {fields: topicFields{topicProductCreated: {"product_id"}}, says: "not among"},
		"a filter on a field not carried":    {filters: topicFilters{topicCartCreated: {"total": {"1"}}}, says: "carries"},
		"a filter on the redacted field":     {filters: topicFilters{topicOrderPlaced: {"customer_id": {"cus_1"}}}, says: "carries"},
		"a field not carried":                {fields: topicFields{topicOrderPlaced: {"cart_id"}}, says: "carries"},
		"the redacted field asked for":       {fields: topicFields{topicOrderPlaced: {"customer_id"}}, says: "carries"},
		"a filter with no field":             {filters: topicFilters{topicCartCreated: {}}, says: "name no field"},
		"a field list with no field":         {fields: topicFields{topicOrderPlaced: {}}, says: "name no field"},
		"a filter with no value":             {filters: topicFilters{topicCartCreated: {"region_id": {}}}, says: "values"},
		"a filter with too many values":      {filters: topicFilters{topicCartCreated: {"region_id": many}}, says: "values"},
		"an empty value":                     {filters: topicFilters{topicCartCreated: {"region_id": {" "}}}, says: "empty"},
		"a value too long": {
			filters: topicFilters{topicCartCreated: {"region_id": {strings.Repeat("x", maxFilterValueLen+1)}}}, says: "longer",
		},
	} {
		_, _, err := validateNarrowing(topics, c.filters, c.fields)
		require.Error(t, err, name)
		assert.True(t, coreerrors.IsInvalid(err), name)
		assert.Contains(t, err.Error(), c.says, name)
	}

	_, _, err := validateNarrowing(topics,
		topicFilters{topicCartCreated: {"region_id": many[:maxFilterValues]}}, nil)
	require.NoError(t, err, "the most values a filter lists are taken")
	_, _, err = validateNarrowing(topics,
		topicFilters{topicCartCreated: {"region_id": {strings.Repeat("x", maxFilterValueLen)}}}, nil)
	require.NoError(t, err, "the longest value is taken")
}
