package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// TestACartAnswersTheChannelItIsPricedIn holds the conversion every cart
// response goes through: the channel the cart records reaches the body under
// `sales_channel_id`, and a cart in none leaves the field out (ADR 0397).
//
// It is in the internal package because [toCartDTO] is unexported, and the
// OpenAPI tests build their cart by hand rather than through it.
func TestACartAnswersTheChannelItIsPricedIn(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(toCartDTO(models.Cart{ID: "cart_1", SalesChannelID: "sc_A"}))
	require.NoError(t, err)
	assert.Contains(t, string(body), `"sales_channel_id":"sc_A"`)

	body, err = json.Marshal(toCartDTO(models.Cart{ID: "cart_1"}))
	require.NoError(t, err)
	assert.NotContains(t, string(body), `"sales_channel_id"`, "a cart in none names none")
}
