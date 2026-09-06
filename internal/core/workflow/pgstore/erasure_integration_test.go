//go:build integration

// The claims in this file can be proven ONLY against a real database, and the
// reason is the statement itself: the erasure is one UPDATE built out of jsonb
// operators — `||`, `->>`, `jsonb_typeof` and `IS DISTINCT FROM` — and none of
// them exists in Go. A unit test against a fake store would assert that a map
// was edited, which is a claim about the fake.
//
// The file is INSIDE the pgstore package because the eraser is a method on the
// unexported store type.
package pgstore

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// planWithPerson is a stand-in for the one input this store ever holds.
//
// It carries the field names the real checkout plan uses rather than a
// simplified shape, because the statement matches on `customer_id` and `email`
// by NAME and empties `shipping_address` and `billing_address` by NAME: a test
// written against invented keys would pass while the production keys went
// untouched.
func planWithPerson(t *testing.T, customerID, email string) json.RawMessage {
	t.Helper()

	plan := map[string]any{
		"cart_id":     "cart_1",
		"customer_id": customerID,
		"email":       email,
		"amount":      12000,
		"shipping_address": map[string]any{
			"first_name": "A",
			"last_name":  "B",
			"address_1":  "a street",
			"phone":      "+900000000",
		},
		"billing_address": map[string]any{
			"first_name": "A",
			"last_name":  "B",
			"address_1":  "a street",
		},
		"lines": []any{map[string]any{"variant_id": "var_1", "quantity": 1}},
	}

	raw, err := json.Marshal(plan)
	require.NoError(t, err)

	return raw
}

// seedPlanExecution writes one execution carrying the given plan and returns its id.
func seedPlanExecution(t *testing.T, customerID, email string) string {
	t.Helper()

	exec := &workflow.Execution{
		Workflow: "complete_cart",
		Status:   workflow.StatusCompleted,
		Input:    planWithPerson(t, customerID, email),
	}

	require.NoError(t, newStore().Create(context.Background(), exec))

	return exec.ID
}

// readPlanInput reads an execution's stored input back as a map.
func readPlanInput(t *testing.T, id string) map[string]any {
	t.Helper()

	exec, err := newStore().Get(context.Background(), id)
	require.NoError(t, err)

	if exec.Input == nil {
		return nil
	}

	var out map[string]any
	require.NoError(t, json.Unmarshal(exec.Input, &out))

	return out
}

// planEraser returns the store as an eraser.
func planEraser(t *testing.T) personaldata.Eraser {
	t.Helper()

	e, ok := newStore().(personaldata.Eraser)
	require.True(t, ok, "the store no longer offers the erasure capability")

	return e
}

// TestErasureEmptiesThePersonAndKeepsThePlan is the claim the whole design
// rests on.
//
// The point is not that the personal fields go — it is that everything else
// STAYS. `gobit recover` rebuilds an abandoned saga's compensation chain from
// this very JSON and refuses a plan with no cart id, so an erasure that emptied
// the column would trade a privacy answer for an unrecoverable checkout.
func TestErasureEmptiesThePersonAndKeepsThePlan(t *testing.T) {
	ctx := context.Background()
	id := seedPlanExecution(t, "cus_keep", "keep@example.test")

	result, err := planEraser(t).Erase(ctx, personaldata.Subject{CustomerID: "cus_keep"})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Anonymized, result.Outcome)
	assert.Equal(t, 1, result.Rows)

	got := readPlanInput(t, id)

	assert.Equal(t, "", got["email"], "the shopper's address must be emptied")
	assert.Nil(t, got["shipping_address"], "the shipping address must be emptied")
	assert.Nil(t, got["billing_address"], "the billing address must be emptied")

	// The half that makes recovery survive.
	assert.Equal(t, "cart_1", got["cart_id"], "the cart id is what the recovery rebuild refuses to run without")
	assert.EqualValues(t, 12000, got["amount"], "the amount is read by the capture compensation")
	assert.NotNil(t, got["lines"], "the lines are what the reservation compensation releases")

	// The key stays, emptied, rather than disappearing: "redacted" and "never
	// had one" are different facts and the row should be able to say which.
	_, hasEmail := got["email"]
	assert.True(t, hasEmail, "the email key should remain, emptied")
	_, hasShipping := got["shipping_address"]
	assert.True(t, hasShipping, "the shipping_address key should remain, nulled")
}

// TestErasureFindsTheSubjectByEitherHandle covers the two ways in.
//
// A guest checkout writes an empty customer_id and a real e-mail, so a sweep
// that only matched on the id would walk past every guest — which is most of
// the rows this store holds for a shop without accounts.
func TestErasureFindsTheSubjectByEitherHandle(t *testing.T) {
	ctx := context.Background()

	byID := seedPlanExecution(t, "cus_byid", "byid@example.test")
	byEmail := seedPlanExecution(t, "", "byemail@example.test")

	_, err := planEraser(t).Erase(ctx, personaldata.Subject{CustomerID: "cus_byid"})
	require.NoError(t, err)

	_, err = planEraser(t).Erase(ctx, personaldata.Subject{Email: "byemail@example.test"})
	require.NoError(t, err)

	assert.Equal(t, "", readPlanInput(t, byID)["email"])
	assert.Equal(t, "", readPlanInput(t, byEmail)["email"], "a guest's record is reachable only by address")
}

// TestErasureIsIdempotent proves a second sweep is a no-op rather than an error.
//
// A controller who runs a sweep twice — because the first report was mislaid,
// or a second request arrived — must get the same outcome. The count is what
// carries the difference: zero rows means there was nothing left to empty, not
// that the person was never here.
func TestErasureIsIdempotent(t *testing.T) {
	ctx := context.Background()
	id := seedPlanExecution(t, "cus_twice", "twice@example.test")

	first, err := planEraser(t).Erase(ctx, personaldata.Subject{CustomerID: "cus_twice"})
	require.NoError(t, err)
	require.Equal(t, 1, first.Rows)

	before := readPlanInput(t, id)

	second, err := planEraser(t).Erase(ctx, personaldata.Subject{CustomerID: "cus_twice"})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Anonymized, second.Outcome, "a repeat is still an anonymization, not an error")
	assert.Equal(t, 0, second.Rows, "the second pass must not rewrite an already-empty record")

	assert.Equal(t, before, readPlanInput(t, id), "the second pass changed the row")
}

// TestErasureLeavesOtherPeopleAlone is the predicate's other half.
//
// A WHERE clause that matched too widely would be the worst possible defect
// here: it would erase strangers' records while reporting a successful answer
// to one person's request, and nothing downstream would notice.
func TestErasureLeavesOtherPeopleAlone(t *testing.T) {
	ctx := context.Background()

	mine := seedPlanExecution(t, "cus_mine", "mine@example.test")
	theirs := seedPlanExecution(t, "cus_theirs", "theirs@example.test")

	_, err := planEraser(t).Erase(ctx, personaldata.Subject{CustomerID: "cus_mine", Email: "mine@example.test"})
	require.NoError(t, err)

	assert.Equal(t, "", readPlanInput(t, mine)["email"])

	other := readPlanInput(t, theirs)
	assert.Equal(t, "theirs@example.test", other["email"], "another person's record was touched")
	assert.NotNil(t, other["shipping_address"], "another person's address was emptied")
}

// TestErasureIsUnmovedByAMalformedInput pins what protects the statement from a
// row whose input is not an object.
//
// `input` is nullable and nothing constrains it to an object, so the obvious
// answer was a jsonb_typeof guard. Measured, that guard could not fire and the
// reason given for it was false — `||` does NOT raise on a scalar in
// PostgreSQL 16. What protects the statement is the predicate: `->>` on a
// non-object returns SQL NULL, so neither arm matches and the row is never
// selected. This test is what keeps that true if the predicate is rewritten.
func TestErasureIsUnmovedByAMalformedInput(t *testing.T) {
	ctx := context.Background()

	exec := &workflow.Execution{Workflow: "complete_cart", Status: workflow.StatusCompleted}
	require.NoError(t, newStore().Create(ctx, exec))

	_, err := testPool.Pool().Exec(ctx,
		`UPDATE workflow_executions SET input = '"a string"'::jsonb WHERE id = $1`, exec.ID)
	require.NoError(t, err)

	ordinary := seedPlanExecution(t, "cus_ok", "ok@example.test")

	result, err := planEraser(t).Erase(ctx, personaldata.Subject{CustomerID: "cus_ok"})
	require.NoError(t, err, "a record whose input is not an object must not fail the sweep")
	assert.Equal(t, 1, result.Rows)
	assert.Equal(t, "", readPlanInput(t, ordinary)["email"])
}

// TestErasureWithoutASubjectTouchesNothing holds the line that erasing
// "everyone" is not an erasure request.
//
// The coordinator refuses an empty subject before any holder is asked, but a
// holder reached directly must refuse it too: with both parameters empty the
// predicate's two arms are false and the statement matches nothing, and this
// test is what keeps that true if the arms are ever rewritten.
func TestErasureWithoutASubjectTouchesNothing(t *testing.T) {
	ctx := context.Background()
	id := seedPlanExecution(t, "cus_untouched", "untouched@example.test")

	result, err := planEraser(t).Erase(ctx, personaldata.Subject{})
	require.NoError(t, err)
	assert.Equal(t, 0, result.Rows)

	assert.Equal(t, "untouched@example.test", readPlanInput(t, id)["email"])
}

// TestTheErasureResultAlwaysSaysWhatItKept holds the contract's own rule.
//
// core/personaldata documents that an Anonymized result names the free-form columns
// it did not rewrite, because otherwise the word "anonymized" covers a field
// nobody looked at. The two failure columns are those fields here.
func TestTheErasureResultAlwaysSaysWhatItKept(t *testing.T) {
	ctx := context.Background()
	seedPlanExecution(t, "cus_kept", "kept@example.test")

	result, err := planEraser(t).Erase(ctx, personaldata.Subject{CustomerID: "cus_kept"})
	require.NoError(t, err)

	assert.NotEmpty(t, result.Why)
	assert.Contains(t, result.Kept, "workflow_executions.failure")
	assert.Contains(t, result.Kept, "workflow_execution_steps.failure")

	for _, kept := range result.Kept {
		assert.NotContains(t, kept, ".output",
			"an output column is named as kept; measured, the outputs hold identifiers and amounts")
	}
}
