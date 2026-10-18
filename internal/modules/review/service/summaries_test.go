package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// TestASummaryReadIsBounded refuses what ADR 0449's read does not take: no
// product, an empty id, more than a listing's page.
func TestASummaryReadIsBounded(t *testing.T) {
	t.Parallel()

	svc, _ := newService()
	many := make([]string, service.MaxSummaryProducts+1)
	for i := range many {
		many[i] = fmt.Sprintf("prod_%d", i)
	}
	for name, ids := range map[string][]string{
		"none": nil, "an empty id": {"prod_1", " "}, "too many": many,
	} {
		_, err := svc.SummarizeProducts(context.Background(), ids)
		assert.Equalf(t, errors.KindInvalid, errors.KindOf(err), "%s", name)
	}

	got, err := svc.SummarizeProducts(context.Background(), many[:service.MaxSummaryProducts])
	require.NoError(t, err)
	assert.Len(t, got, service.MaxSummaryProducts, "a full page is answered")
}
