package main

// Conversions between a value in the units a person reads, such as the
// level of status.session.volumeAsk, and the integer steps a driver sends
// and reports.

import (
	"math"
	"strconv"
)

// formatSteps converts a driver's step count to the text that the
// status shows. resolution is the number of steps in one display unit,
// so 139 steps at a resolution of 2 is "69.5". A negative step count or
// a resolution of zero or less returns "".
func formatSteps(steps, resolution int) string {
	if steps < 0 || resolution <= 0 {
		return ""
	}
	return strconv.FormatFloat(float64(steps)/float64(resolution), 'f', -1, 64)
}

// stepsFromScale converts a value in display units to the nearest whole
// number of driver steps. A resolution of zero or less returns 0.
func stepsFromScale(value float64, resolution int) int {
	if resolution <= 0 {
		return 0
	}
	return int(math.Round(value * float64(resolution)))
}
