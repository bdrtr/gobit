package scheduledpublish

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// fakePublisher answers a pass with scripted ids or an error.
type fakePublisher struct {
	ids      []string
	err      error
	gotLimit int64
}

// PublishDue records the limit and answers as scripted.
func (f *fakePublisher) PublishDue(_ context.Context, limit int64) ([]string, error) {
	f.gotLimit = limit
	return f.ids, f.err
}

// TestAPassPublishesWhatIsDueUnderTheBound verifies the pass asks with its
// bound and succeeds whether or not anything was due.
func TestAPassPublishesWhatIsDueUnderTheBound(t *testing.T) {
	for name, ids := range map[string][]string{
		"nothing due": nil,
		"two due":     {"prod_1", "prod_2"},
	} {
		t.Run(name, func(t *testing.T) {
			publisher := &fakePublisher{ids: ids}

			require.NoError(t, Definition(publisher, nil).Run(context.Background()))
			assert.Equal(t, int64(limit), publisher.gotLimit)
		})
	}
}

// TestAFailedPassKeepsItsKind verifies that a pass that could not publish fails
// the run with the store's kind, so the scheduler records it as failed.
func TestAFailedPassKeepsItsKind(t *testing.T) {
	publisher := &fakePublisher{err: coreerrors.Unavailable("db_down", "the database is gone")}

	err := Definition(publisher, nil).Run(context.Background())

	require.Error(t, err)
	assert.Equal(t, codePublishFailed, coreerrors.CodeOf(err))
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable), "error: %v", err)
}

// TestTheDefinitionIsValid verifies the job would be admitted by the registry.
func TestTheDefinitionIsValid(t *testing.T) {
	definition := Definition(&fakePublisher{}, nil)

	assert.Equal(t, Name, definition.Name)
	assert.Less(t, definition.MaxRun, definition.Every, "a pass has to end before the next is due")
}
