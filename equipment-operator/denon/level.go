// The half-step arithmetic between a display unit and the receiver's
// own count. A Denon moves in halves and an integer count of them never
// rounds, so the driver carries volume in half steps and only a person
// or the bus sees display units.

package denon

import (
	"strconv"
)

// unknownHalves is a value the receiver has not reported yet.
const unknownHalves = -1

// ParseHalfSteps reads the digits a Denon sends. Two digits are whole
// steps and three digits are tenths, and the receiver only ever sends a
// tenth of five.
func ParseHalfSteps(digits string) (int, bool) {
	value, err := strconv.Atoi(digits)
	if err != nil || value < 0 {
		return unknownHalves, false
	}
	switch len(digits) {
	case 2:
		return value * 2, true
	case 3:
		if value%5 != 0 {
			return unknownHalves, false
		}
		return value / 5, true
	}
	return unknownHalves, false
}

// FormatHalfSteps writes a count the way a person reads it, which is
// what the status carries.
func FormatHalfSteps(halves int) string {
	if halves < 0 {
		return ""
	}
	if halves%2 == 0 {
		return strconv.Itoa(halves / 2)
	}
	return strconv.Itoa(halves/2) + ".5"
}

// HalfStepDigits writes the same count in the digits a set command
// carries: two for a whole step, three for a half.
func HalfStepDigits(halves int) string {
	whole := halves / 2
	digits := strconv.Itoa(whole)
	if whole < 10 {
		digits = "0" + digits
	}
	if halves%2 == 1 {
		digits += "5"
	}
	return digits
}

// denonScaleTop is the top of a Denon's own scale, 98, which no
// declared ceiling may exceed.
const denonScaleTop = 196
