package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// TestAStackedRateIsNotMadeTheDefault is gap D248: a rate standing on another
// is refused as the region's default on an update, as it is on a create
// (ADR 0095), and the refused flag is not written. The base under it, and a
// rate in no stack, are still made the default.
func TestAStackedRateIsNotMadeTheDefault(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateA, trRegionID, 1800)
	repo.seedStackedRate(rateB, trRegionID, rateA, 200, false)
	repo.seedRuledRate(rateC, trRegionID, 800)

	yes, no := true, false
	_, err := svc.UpdateTaxRate(ctx, rateB, UpdateTaxRateInput{IsDefault: &yes})
	require.Error(t, err, "a stacked rate made the default")
	assert.Equal(t, CodeStackNotAllowed, errors.CodeOf(err), "%v", err)
	assert.True(t, errors.IsConflict(err), "the stack is stored, not in the request: %v", err)
	assert.False(t, repo.rates[rateB].IsDefault, "a refused flag is not written")
	value := int32(300)
	_, err = svc.UpdateTaxRate(ctx, rateB, UpdateTaxRateInput{IsDefault: &yes, RateBps: &value})
	require.Error(t, err, "a stacked rate made the default with a new value")
	assert.Equal(t, CodeStackNotAllowed, errors.CodeOf(err), "%v", err)
	assert.False(t, repo.rates[rateB].IsDefault, "a refused flag is not written")
	assert.Equal(t, int32(200), repo.rates[rateB].RateBps, "nor the value beside it")

	_, err = svc.UpdateTaxRate(ctx, rateB, UpdateTaxRateInput{IsDefault: &no})
	require.NoError(t, err, "a stacked rate is not the default and may say so")

	_, err = svc.UpdateTaxRate(ctx, rateA, UpdateTaxRateInput{IsDefault: &yes})
	require.NoError(t, err, "the base of a stack may be the default")
	_, err = svc.UpdateTaxRate(ctx, rateA, UpdateTaxRateInput{IsDefault: &no})
	require.NoError(t, err)
	_, err = svc.UpdateTaxRate(ctx, rateC, UpdateTaxRateInput{IsDefault: &yes})
	require.NoError(t, err, "a rate in no stack may be the default")
}

// TestAStackedDefaultIsNotWrittenUnread holds D248's check to the read it
// rests on: a rate that could not be read is not made the default.
func TestAStackedDefaultIsNotWrittenUnread(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateA, trRegionID, 1800)
	repo.seedStackedRate(rateB, trRegionID, rateA, 200, false)
	repo.failOn["GetTaxRate"] = errors.Unavailable("db_down", "the database is unreachable")

	yes := true
	_, err := svc.UpdateTaxRate(context.Background(), rateB, UpdateTaxRateInput{IsDefault: &yes})
	require.Error(t, err)
	assert.Equal(t, "db_down", errors.CodeOf(err), "%v", err)
	assert.False(t, repo.rates[rateB].IsDefault, "an unread rate is not made the default")
}
