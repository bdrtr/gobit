package personaldata_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/core/personaldata"
)

// declaration is three holdings: one an erasure empties and two it keeps.
func declaration() personaldata.Declaration {
	return personaldata.Declaration{Holdings: []personaldata.Holding{
		{Table: "orders", Column: "email", Kind: personaldata.Named, OnErasure: personaldata.Emptied},
		{Table: "orders", Column: "metadata", Kind: personaldata.Open, OnErasure: personaldata.Kept},
		{Table: "order_addresses", Column: "country_code", Kind: personaldata.Named, OnErasure: personaldata.Kept},
	}}
}

// TestKeptOnErasureIsWhatAnErasureLeaves reads the kept holdings off the
// declaration, in its order (ADR 0172).
func TestKeptOnErasureIsWhatAnErasureLeaves(t *testing.T) {
	assert.Equal(t, []string{"orders.metadata", "order_addresses.country_code"},
		declaration().KeptOnErasure())
}

// TestPathsIsEverythingARefusalKeeps names every holding, in order.
func TestPathsIsEverythingARefusalKeeps(t *testing.T) {
	assert.Equal(t, []string{"orders.email", "orders.metadata", "order_addresses.country_code"},
		declaration().Paths())
}

// TestAnEmptyDeclarationKeepsNothing is the notification log's shape: a holder
// that declares no column keeps none.
func TestAnEmptyDeclarationKeepsNothing(t *testing.T) {
	assert.Empty(t, personaldata.Declaration{}.KeptOnErasure())
	assert.Empty(t, personaldata.Declaration{}.Paths())
}
