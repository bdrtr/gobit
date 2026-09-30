package ordercancel

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// shelfAct is one of the two acts that put a line's written-off units back: a
// write-off growing what was canceled, or a parcel canceled shrinking what is
// committed to leave.
type shelfAct struct {
	writtenOff int64
	released   int64
}

// TestTheShelfReachesTheSameTargetInAnyOrder is ADR 0249 on D82: each act
// raises the line's units on the shelf up to targetOnShelf of the state it
// finds, and whatever order the bus delivers the acts in, and however often it
// delivers one again, the shelf ends at the target of the final state. That
// holds because each move can only raise the target, so the target is also
// never below zero, never above what was written off and never above what was
// bought and will not leave.
func TestTheShelfReachesTheSameTargetInAnyOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		bought := rapid.Int64Range(0, 20).Draw(t, "bought")
		committed := rapid.Int64Range(0, bought+2).Draw(t, "committed")
		var acts []shelfAct
		for range rapid.IntRange(0, 6).Draw(t, "acts") {
			if rapid.Bool().Draw(t, "a write-off") {
				acts = append(acts, shelfAct{writtenOff: rapid.Int64Range(1, 5).Draw(t, "written off")})
			} else {
				acts = append(acts, shelfAct{released: rapid.Int64Range(1, 5).Draw(t, "released")})
			}
		}

		play := func(order []shelfAct) (shelf, canceled, stillCommitted int64) {
			stillCommitted = committed
			for _, act := range order {
				canceled += act.writtenOff
				stillCommitted = max(stillCommitted-act.released, 0)
				target := targetOnShelf(bought, canceled, stillCommitted)
				require.GreaterOrEqual(t, target, int64(0))
				require.LessOrEqual(t, target, canceled)
				require.LessOrEqual(t, target, max(bought-stillCommitted, 0))
				require.GreaterOrEqual(t, target, shelf, "a move never lowers the target")
				shelf = max(shelf, target)
				// A redelivery finds the same state and the same target.
				require.Equal(t, target, targetOnShelf(bought, canceled, stillCommitted))
			}
			return shelf, canceled, stillCommitted
		}

		shelf, canceled, stillCommitted := play(acts)
		require.Equal(t, targetOnShelf(bought, canceled, stillCommitted), shelf, "the shelf ends at the final target")
		reordered, _, _ := play(rapid.Permutation(acts).Draw(t, "acts reordered"))
		require.Equal(t, shelf, reordered, "the order the acts arrive in changes nothing")
	})
}
