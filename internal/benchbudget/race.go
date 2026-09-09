//go:build race

package benchbudget

// raceEnabled reports whether this binary was built with the race detector.
//
// It is a build tag on a constant rather than a tag on the budgets themselves:
// the budgets run in the ordinary test lane, and a lane that skipped them under
// `-race` would be a lane that measured nothing on the run this repository takes
// most seriously.
const raceEnabled = true
