package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// Error codes of the price list trial (ADR 0220), shared by the endpoint and
// the panel's surface (ADR 0395).
const (
	// CodeListTrialInvalidPeriod reports a from or to that is missing,
	// malformed or in the future.
	CodeListTrialInvalidPeriod = "pricing_trial_invalid_period"
	// CodeListTrialUnavailable reports that the flow the trial runs on is not
	// bound.
	CodeListTrialUnavailable = "pricing_trial_unavailable"
)

// ListTrialFlow is the surface of the flow that prices a price list against
// past orders (ADR 0220), as the panel's surface asks it (ADR 0395). It is the
// endpoint's PriceListTrial, declared again here because this package cannot
// import the api package that declares it.
type ListTrialFlow interface {
	// TrialPriceListJSON prices the list against the orders placed in
	// [from, to); it writes nothing.
	TrialPriceListJSON(ctx context.Context, listID string, from, to time.Time) (json.RawMessage, error)
}

// WithTrial binds the flow the panel's price list trial runs on and returns
// the surface; the module hands over the wrapper its endpoint uses.
func (a *AdminSurface) WithTrial(trial ListTrialFlow) *AdminSurface {
	a.trial = trial

	return a
}

// TrialPriceListJSON is the price list trial the panel asks for (ADR 0395):
// the flow's report as it is, after the endpoint's own refusals of a future
// end and of an unbound flow.
func (a *AdminSurface) TrialPriceListJSON(
	ctx context.Context, listID string, from, to time.Time,
) (json.RawMessage, error) {
	if a == nil || a.trial == nil {
		return nil, errors.Internal(CodeListTrialUnavailable,
			"the price list trial flow is not bound; no order can be read")
	}
	if to.After(time.Now()) {
		return nil, errors.Invalid(CodeListTrialInvalidPeriod,
			"a trial reads orders already placed; \"to\" is in the future: %s", to.Format(time.RFC3339))
	}

	return a.trial.TrialPriceListJSON(ctx, listID, from, to)
}
