package arch_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/internal/modules/customer"
	customermodels "github.com/bdrtr/gobit/internal/modules/customer/models"
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	segmentwf "github.com/bdrtr/gobit/internal/workflows/segment"
)

// TestTheSegmentVocabularyIsOneVocabulary holds the two spellings of ADR 0217's
// rule words to each other.
//
// The customer module admits a rule and the segment flow evaluates it, and
// neither can import the other. An attribute the module admitted and the flow
// did not know would make every pass report the segment unreadable; an operator
// the flow knew and the module never admitted would be dead code that looks
// like a feature.
func TestTheSegmentVocabularyIsOneVocabulary(t *testing.T) {
	t.Parallel()

	assert.Equal(t, customermodels.SegmentOperators, segmentwf.Operators)
	assert.Equal(t, segmentwf.InteropName, customer.SegmentFlowName,
		"the preview resolves the flow by this name")
}

// TestTheSegmentPageFitsBothModules: a page the flow reads is one the customer
// module pages and the order module totals in one call.
func TestTheSegmentPageFitsBothModules(t *testing.T) {
	t.Parallel()

	assert.LessOrEqual(t, segmentwf.PageSize, customersvc.MaxSegmentFactsPage)
	assert.LessOrEqual(t, segmentwf.PageSize, ordersvc.MaxCustomerTotals)
}
