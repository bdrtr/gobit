package customersegment

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
)

// fakePasser answers with a scripted line.
type fakePasser struct {
	line   string
	err    error
	passes int
}

func (f *fakePasser) PassReport(context.Context) (string, error) {
	f.passes++

	return f.line, f.err
}

// TestARunMakesOnePassAndSaysSo: the operator's line is the flow's report.
func TestARunMakesOnePassAndSaysSo(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(context.Background())
	fake := &fakePasser{line: "wrote 2 segments"}

	require.NoError(t, Definition(fake, nil).Run(ctx))

	assert.Equal(t, 1, fake.passes)
	assert.Equal(t, "wrote 2 segments", jobreport.Detail(ctx))
}

// TestAFailedRunReportsWhatItWrote: the pages that were written stay.
func TestAFailedRunReportsWhatItWrote(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(context.Background())
	fake := &fakePasser{line: "wrote 1 segments", err: coreerrors.Unavailable("db_down", "the database is away")}

	err := Definition(fake, nil).Run(ctx)

	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable))
	assert.Equal(t, codePassFailed, coreerrors.CodeOf(err))
	assert.Equal(t, "wrote 1 segments", jobreport.Detail(ctx))
}

// TestTheDefinitionFitsItsInterval: a run cannot outlast the gap to the next.
func TestTheDefinitionFitsItsInterval(t *testing.T) {
	t.Parallel()

	definition := Definition(&fakePasser{}, nil)

	assert.Equal(t, Name, definition.Name)
	assert.Equal(t, Every, definition.Every)
	assert.Less(t, definition.MaxRun, definition.Every)
}
