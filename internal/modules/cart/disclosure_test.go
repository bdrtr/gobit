package cart_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/cart"
)

// These tests need no database either. What they check is the MODULE type — the
// surface the coordinator finds by type assertion — rather than the answer
// itself, which is the service's and is tested in service/disclosure_test.go
// against a fake store and in disclosure_integration_test.go against a real one.

// TestTheModuleOffersTheDisclosureCapability pins the type assertion the
// coordinator makes.
//
// The interface is optional and is found at runtime, so a method renamed or a
// signature changed would compile perfectly and cost only this: the module drops
// out of every dossier in silence, and a person is told the cart module holds
// nothing about her. module.go pins the same thing at compile time; this test
// says out loud what the compile-time line is protecting.
func TestTheModuleOffersTheDisclosureCapability(t *testing.T) {
	var module any = cart.New(cart.Options{})

	_, ok := module.(personaldata.Discloser)
	assert.True(t, ok, "the cart module has to be findable as a discloser")
}

// TestAnUnregisteredModuleRefusesToDisclose proves the module fails LOUDLY when
// its service was never wired.
//
// The alternative — an empty [personaldata.Disclosure] — is the worst answer
// available: it reads as "the cart module holds nothing about you", a controller
// repeats it to the person, and nothing anywhere says a search never happened.
// The same reasoning makes [cart.Module.Erase] refuse; a missing route fails
// visibly, a missing answer to a data subject does not.
func TestAnUnregisteredModuleRefusesToDisclose(t *testing.T) {
	disclosure, err := cart.New(cart.Options{}).PersonalDataOf(
		context.Background(), personaldata.Subject{CustomerID: "cust_ANY"})

	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Empty(t, disclosure.State,
		"an unwired module answers with an error and NOT with a state a dossier could record")
}
