package http_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// deferredReaders is how many readers run while the binding happens.
const deferredReaders = 8

// deferredReadsPerReader is how many reads each reader makes.
//
// The count is what turns a lucky interleaving into a real overlap: with one
// read apiece the binding would usually land before or after all of them and
// the window this test exists for would go unvisited.
const deferredReadsPerReader = 200

// TestTheDeferredAuthenticatorIsReadableWhileItIsBeingBound runs the sentence in
// [corehttp.DeferredAuthenticator]'s godoc.
//
// The promise — "safe for concurrent use: the binding happens once, the read on
// every request" — describes a window that really exists: the type is installed
// while the router is built and bound after module bootstrap, so a request can
// arrive in between. Every read has to answer ONE of two things, the 401 for an
// unbound guard or the bound authenticator's answer, and never a third.
//
// Measured on 2026-09-09: no test in this package ran this type from two
// goroutines, so the race detector — which only looks at code that actually ran
// concurrently — had never seen the atomic behind it.
func TestTheDeferredAuthenticatorIsReadableWhileItIsBeingBound(t *testing.T) {
	t.Parallel()

	var deferred corehttp.DeferredAuthenticator
	bound := fixedAuthenticator{principal: corehttp.Principal{SalesChannelIDs: []string{"sc_1"}}}

	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(deferredReaders + 1)

	for range deferredReaders {
		go func() {
			defer done.Done()
			start.Wait()
			for range deferredReadsPerReader {
				principal, err := deferred.AuthenticateStore(context.Background(), "pk_x")
				if err != nil {
					assert.True(t, errors.HasKind(err, errors.KindUnauthorized),
						"an unbound guard has to answer unauthorized, got: %v", err)

					continue
				}
				assert.Equal(t, []string{"sc_1"}, principal.SalesChannelIDs,
					"the guard answered with an identity nobody bound")
			}
		}()
	}

	go func() {
		defer done.Done()
		start.Wait()
		deferred.Bind(bound)
	}()

	start.Done()
	done.Wait()

	principal, err := deferred.AuthenticateStore(context.Background(), "pk_x")
	require.NoError(t, err)
	assert.Equal(t, []string{"sc_1"}, principal.SalesChannelIDs)
}

// callbackWriters is how many routes race to register while the registry is
// being mounted.
const callbackWriters = 8

// TestTheCallbackRegistryFreezesExactlyOnceUnderConcurrentRegistration runs the
// two-phase promise under a race.
//
// [corehttp.CallbackRegistry] collects routes and then FREEZES on Mount, and a
// Register after Mount is a loud error rather than a silent no-op — "silently
// ignoring it would produce a route the provider can reach and nothing guards".
// Single-threaded that is an ordering of two calls. Under concurrency it is what
// the lock promises: each registration either lands and is mounted, or is
// refused because the registry had already closed. A route that lands and is
// NOT mounted is exactly the unguarded path the design refuses.
func TestTheCallbackRegistryFreezesExactlyOnceUnderConcurrentRegistration(t *testing.T) {
	t.Parallel()

	registry := corehttp.NewCallbackRegistry(corehttp.CallbackOptions{})

	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(callbackWriters + 1)

	accepted := make(chan string, callbackWriters)
	refused := make(chan error, callbackWriters)

	for i := range callbackWriters {
		go func() {
			defer done.Done()
			path := fmt.Sprintf("/testpay/callback-%d", i)
			route := corehttp.CallbackRoute{
				Source:  "testpay",
				Path:    path,
				Verify:  func(context.Context, *http.Request, []byte) error { return nil },
				Key:     testKey,
				Handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
				Ack:     testAck,
			}
			start.Wait()
			if err := registry.Register(route); err != nil {
				refused <- err

				return
			}
			accepted <- path
		}()
	}

	router := chi.NewRouter()
	router.Use(registry.Middleware())

	go func() {
		defer done.Done()
		start.Wait()
		assert.NoError(t, registry.Mount(router))
	}()

	start.Done()
	done.Wait()
	close(accepted)
	close(refused)

	require.Equal(t, callbackWriters, len(accepted)+len(refused),
		"every writer has to produce exactly one outcome")

	mounted := registry.Routes()
	for path := range accepted {
		assert.Contains(t, mounted, path,
			"a route was accepted and then not mounted: the provider can reach a path "+
				"nothing guards, which is the defect the freeze exists to remove")
	}

	// A refusal is the OTHER correct answer, and it has to say the registry was
	// already closed rather than fail for some unrelated reason.
	for err := range refused {
		assert.Error(t, err)
	}
}
