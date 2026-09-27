//go:build integration

package customer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// segmentRule is a rule every account satisfies.
func segmentRule() models.SegmentRule {
	return models.SegmentRule{Conditions: []models.SegmentCondition{
		{Attribute: models.SegmentHasAccount, Operator: models.SegmentEq, Value: json.RawMessage(`true`)},
	}}
}

// newSegmentGroup opens a group no other test names.
func newSegmentGroup(ctx context.Context, t *testing.T, svc *service.Service) models.CustomerGroup {
	t.Helper()

	group, err := svc.CreateGroup(ctx, service.GroupInput{Name: fmt.Sprintf("segment %s", newEmail(t))})
	require.NoError(t, err)
	return group
}

// groupMembers reads a group's members from the table itself.
func groupMembers(ctx context.Context, t *testing.T, groupID string) []string {
	t.Helper()

	rows, err := testPool.Pool().Query(ctx,
		`SELECT customer_id FROM customer_group_customer WHERE customer_group_id = $1 ORDER BY customer_id`, groupID)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		out = append(out, id)
	}
	require.NoError(t, rows.Err())
	return out
}

// sortedAccounts opens n accounts and returns their ids in id order.
func sortedAccounts(ctx context.Context, t *testing.T, svc *service.Service, n int) []string {
	t.Helper()

	ids := make([]string, 0, n)
	for range n {
		ids = append(ids, newAccount(ctx, t, svc).ID)
	}
	slices.Sort(ids)
	return ids
}

// TestASegmentLivesOnTheRealSchema is ADR 0217 in the module: the rule is
// stored as normalized, hand edits are refused while it stands, a page writes
// its members only in its id range and only while its rule is the segment's,
// a deleted customer is never put in, and the schema refuses a half-set rule.
func TestASegmentLivesOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	group := newSegmentGroup(ctx, t, svc)
	ids := sortedAccounts(ctx, t, svc, 3)
	require.NoError(t, svc.AddToGroup(ctx, ids[2], group.ID), "a member put in by hand before the rule")

	set, err := svc.SetGroupSegment(ctx, group.ID, models.SegmentRule{Conditions: []models.SegmentCondition{
		{Attribute: models.SegmentCountryCode, Operator: models.SegmentIn, Values: []string{"tr", "TR", "de"}},
	}})
	require.NoError(t, err)
	require.NotNil(t, set.SegmentSetAt)
	setAt := *set.SegmentSetAt
	read, err := svc.GetGroup(ctx, group.ID)
	require.NoError(t, err)
	require.NotNil(t, read.Segment)
	assert.Equal(t, []string{"TR", "DE"}, read.Segment.Conditions[0].Values, "stored as normalized")
	assert.True(t, setAt.Equal(*read.SegmentSetAt))
	assert.Nil(t, read.SegmentEvaluatedAt)

	err = svc.AddToGroup(ctx, ids[0], group.ID)
	assert.Equal(t, models.CodeSegmentManaged, errors.CodeOf(err))
	err = svc.RemoveFromGroup(ctx, ids[2], group.ID)
	assert.Equal(t, models.CodeSegmentManaged, errors.CodeOf(err))

	added, removed, applied, err := svc.ApplySegmentPage(ctx, group.ID, setAt, "", ids[1], []string{ids[0]})
	require.NoError(t, err)
	assert.Equal(t, []any{1, 0, true}, []any{added, removed, applied})
	assert.Equal(t, []string{ids[0], ids[2]}, groupMembers(ctx, t, group.ID), "a member past the page stays")

	_, removed, _, err = svc.ApplySegmentPage(ctx, group.ID, setAt, ids[1], "", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.Equal(t, []string{ids[0]}, groupMembers(ctx, t, group.ID), "the last page reaches the end of the ids")

	require.NoError(t, svc.DeleteCustomer(ctx, ids[1]))
	added, _, _, err = svc.ApplySegmentPage(ctx, group.ID, setAt, ids[0], ids[1], []string{ids[1]})
	require.NoError(t, err)
	assert.Zero(t, added, "a deleted customer is not put in")

	_, _, applied, err = svc.ApplySegmentPage(ctx, group.ID, setAt.Add(-time.Second), "", "", nil)
	require.NoError(t, err)
	assert.False(t, applied, "a page read for another rule writes nothing")
	assert.Equal(t, []string{ids[0]}, groupMembers(ctx, t, group.ID))

	stale, err := svc.FinishSegment(ctx, group.ID, setAt.Add(-time.Second), time.Now())
	require.NoError(t, err)
	assert.False(t, stale)
	done, err := svc.FinishSegment(ctx, group.ID, setAt, time.Now())
	require.NoError(t, err)
	assert.True(t, done)
	read, err = svc.GetGroup(ctx, group.ID)
	require.NoError(t, err)
	assert.NotNil(t, read.SegmentEvaluatedAt)

	again, err := svc.SetGroupSegment(ctx, group.ID, segmentRule())
	require.NoError(t, err)
	assert.Nil(t, again.SegmentEvaluatedAt, "a new rule waits for its own pass")
	assert.True(t, again.SegmentSetAt.After(setAt))

	cleared, err := svc.ClearGroupSegment(ctx, group.ID)
	require.NoError(t, err)
	assert.Nil(t, cleared.Segment)
	require.NoError(t, svc.AddToGroup(ctx, ids[2], group.ID), "hand edits are taken again")
	assert.Equal(t, []string{ids[0], ids[2]}, groupMembers(ctx, t, group.ID), "the members the rule wrote stayed")

	for constraint, update := range map[string]string{
		"customer_group_segment_named":     `SET segment = '{}'`,
		"customer_group_segment_object":    `SET segment = '[]', segment_set_at = now()`,
		"customer_group_segment_evaluated": `SET segment_evaluated_at = now()`,
	} {
		_, err := testPool.Pool().Exec(ctx, `UPDATE customer_group `+update+` WHERE id = $1`, group.ID)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr, constraint)
		assert.Equal(t, constraint, pgErr.ConstraintName)
	}
}

// TestTheSegmentFactsAreTheCustomersRecords: the page names each live customer
// with their account and the country of their DEFAULT shipping address only.
func TestTheSegmentFactsAreTheCustomersRecords(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	shopper := newAccount(ctx, t, svc)
	home, err := svc.CreateAddress(ctx, shopper.ID, validAddress())
	require.NoError(t, err)
	_, err = svc.SetDefaultShippingAddress(ctx, shopper.ID, home.ID)
	require.NoError(t, err)
	abroad := validAddress()
	abroad.CountryCode = "de"
	_, err = svc.CreateAddress(ctx, shopper.ID, abroad)
	require.NoError(t, err)
	guest, err := svc.RegisterGuest(ctx, service.CustomerInput{Email: newEmail(t)})
	require.NoError(t, err)
	gone := newAccount(ctx, t, svc)
	require.NoError(t, svc.DeleteCustomer(ctx, gone.ID))

	facts := map[string]map[string]any{}
	after := ""
	for {
		raw, err := svc.SegmentFactsJSON(ctx, after, service.MaxSegmentFactsPage)
		require.NoError(t, err)
		var page []map[string]any
		require.NoError(t, json.Unmarshal(raw, &page))
		for _, row := range page {
			id, _ := row["customer_id"].(string)
			require.Greater(t, id, after, "the page is in id order after its key")
			facts[id] = row
		}
		if len(page) < service.MaxSegmentFactsPage {
			break
		}
		after, _ = page[len(page)-1]["customer_id"].(string)
	}

	require.Contains(t, facts, shopper.ID)
	assert.Equal(t, true, facts[shopper.ID]["has_account"])
	assert.Equal(t, "TR", facts[shopper.ID]["country_code"], "the default shipping address, not the other one")
	require.Contains(t, facts, guest.ID)
	assert.Equal(t, false, facts[guest.ID]["has_account"])
	assert.NotContains(t, facts[guest.ID], "country_code", "no address, no country")
	assert.NotContains(t, facts, gone.ID, "a deleted customer is not read")
}

// TestTheSegmentCountWaitsForAWriterThatHasNotCommitted: a second transaction
// takes the count's lock as the repository does, turns the fiftieth group into a
// segment and holds its commit. A writer asking for one more meanwhile has to
// count after that commit, so it is refused and the segments stay fifty; a
// writer that counted without the lock would see forty-nine and make fifty-one.
func TestTheSegmentCountWaitsForAWriterThatHasNotCommitted(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	var existing int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM customer_group WHERE segment IS NOT NULL AND deleted_at IS NULL`).Scan(&existing))
	var made []string
	t.Cleanup(func() {
		for _, id := range made {
			_, _ = svc.ClearGroupSegment(context.Background(), id)
		}
	})
	for range models.MaxSegments - existing - 1 {
		g := newSegmentGroup(ctx, t, svc)
		_, err := svc.SetGroupSegment(ctx, g.ID, segmentRule())
		require.NoError(t, err)
		made = append(made, g.ID)
	}
	held, late := newSegmentGroup(ctx, t, svc), newSegmentGroup(ctx, t, svc)
	made = append(made, held.ID, late.ID)
	rule, err := json.Marshal(segmentRule())
	require.NoError(t, err)

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, repository.SegmentCountLockKey)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE customer_group SET segment = $2, segment_set_at = now() WHERE id = $1`,
		held.ID, rule)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := svc.SetGroupSegment(ctx, late.ID, segmentRule())
		done <- err
	}()
	var lateErr error
	finished := false
	select {
	case lateErr = <-done:
		finished = true
	case <-time.After(500 * time.Millisecond):
	}
	require.NoError(t, tx.Commit(ctx))
	if !finished {
		lateErr = <-done
	}

	assert.Equal(t, models.CodeSegmentLimit, errors.CodeOf(lateErr), "the late writer counted the held one")
	var total int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM customer_group WHERE segment IS NOT NULL AND deleted_at IS NULL`).Scan(&total))
	assert.Equal(t, models.MaxSegments, total)
}
