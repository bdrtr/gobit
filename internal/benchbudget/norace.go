//go:build !race

package benchbudget

// raceEnabled reports whether this binary was built with the race detector.
//
// See the race-tagged half of this pair for why the constant exists at all.
const raceEnabled = false
