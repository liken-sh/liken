package main

// The arithmetic between a figure in a receiver's own scale, such as
// the level of status.session.volumeAsk, and a driver's smallest steps.

import (
	"math"
	"strconv"
)

// formatSteps writes a driver's step count the way a person reads it,
// which is what the status carries. The resolution is the driver's
// number of steps in one display unit, so two half steps read as one.
func formatSteps(steps, resolution int) string {
	if steps < 0 || resolution <= 0 {
		return ""
	}
	return strconv.FormatFloat(float64(steps)/float64(resolution), 'f', -1, 64)
}

// stepsFromScale reads a figure in the driver's own scale as a count of
// the driver's smallest steps.
func stepsFromScale(value float64, resolution int) int {
	if resolution <= 0 {
		return 0
	}
	return int(math.Round(value * float64(resolution)))
}
