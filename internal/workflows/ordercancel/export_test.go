package ordercancel

import (
	"slices"
	"time"
)

// SetBusyWaits replaces the pauses between asks while a parcel of the order is
// being opened, so a test need not wait the default's budget. It is a method on
// the flow rather than a package variable because the tests run in parallel.
func (w *Workflow) SetBusyWaits(waits []time.Duration) {
	w.busyWaits = waits
}

// SetDraw replaces the source that spreads each pause, so a test's pauses are
// the ones it names.
func (w *Workflow) SetDraw(draw func(n int64) int64) {
	w.draw = draw
}

// DefaultBusyWaits answers a copy of the pauses a flow built by New uses.
func DefaultBusyWaits() []time.Duration {
	return slices.Clone(defaultBusyWaits)
}

// BusyBudget is [busyBudget].
func BusyBudget() time.Duration {
	return busyBudget()
}

// Jittered is [jittered].
func Jittered(wait time.Duration, draw func(n int64) int64) time.Duration {
	return jittered(wait, draw)
}
