package stockalert

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
)

// fakePasser answers with scripted counts.
type fakePasser struct {
	armed, mailed int
	err           error
	passes        int
}

func (f *fakePasser) Pass(context.Context) (armed, mailed int, err error) {
	f.passes++

	return f.armed, f.mailed, f.err
}

// TestARunMakesOnePassAndSaysSo: the operator's line counts the mails and the
// arming.
func TestARunMakesOnePassAndSaysSo(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(context.Background())
	fake := &fakePasser{armed: 2, mailed: 3}

	require.NoError(t, Definition(fake, nil).Run(ctx))

	assert.Equal(t, 1, fake.passes)
	assert.Equal(t, "mailed 3 stock alerts; armed 2 marks that ran out", jobreport.Detail(ctx))
}

// TestAFailedRunReportsWhatItMailed: the mails that went, went.
func TestAFailedRunReportsWhatItMailed(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(context.Background())
	fake := &fakePasser{mailed: 1, err: coreerrors.Unavailable("mail_down", "the gateway did not answer")}

	err := Definition(fake, nil).Run(ctx)

	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable))
	assert.Equal(t, "mailed 1 stock alerts; armed 0 marks that ran out", jobreport.Detail(ctx))
}

// TestTheDefinitionFitsItsInterval: a run cannot outlast the gap to the next.
func TestTheDefinitionFitsItsInterval(t *testing.T) {
	t.Parallel()

	definition := Definition(&fakePasser{}, nil)

	assert.Equal(t, Name, definition.Name)
	assert.Less(t, definition.MaxRun, definition.Every)
}
