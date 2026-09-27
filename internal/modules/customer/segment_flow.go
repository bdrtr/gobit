package customer

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/api"
)

// SegmentFlowName is the container name of the segment flow's surface, where
// the preview runs (ADR 0217).
//
// The flow declares it as InteropName; this module cannot import that package
// (ADR 0006), so the string is repeated here. A typo does not stay silent: the
// preview fails closed on its first request, and internal/arch holds the two
// spellings to each other.
const SegmentFlowName = "workflows.segment.interop"

// segmentPreview resolves the segment flow ON FIRST USE.
//
// The flows are built after every module has registered, so the flow cannot be
// resolved in Register. The outcome of the first resolution is kept: a name
// that did not resolve then will not resolve on a later request either.
type segmentPreview struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	flow api.SegmentPreview
	err  error
}

var _ api.SegmentPreview = (*segmentPreview)(nil)

// PreviewSegmentJSON counts the customers a rule would take in.
func (p *segmentPreview) PreviewSegmentJSON(ctx context.Context, rule json.RawMessage) (json.RawMessage, error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return nil, p.err
	}

	return p.flow.PreviewSegmentJSON(ctx, rule)
}

// resolve looks the flow up in the container and remembers the outcome; a name
// that does not resolve is a setup failure, whatever the container said.
func (p *segmentPreview) resolve(ctx context.Context) {
	flow, err := container.Resolve[api.SegmentPreview](p.c, SegmentFlowName)
	if err != nil {
		p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the segment flow (%q)", ModuleName, SegmentFlowName)

		return
	}
	p.flow = flow
	p.log.InfoContext(ctx, "segment preview flow bound", "flow", SegmentFlowName)
}
