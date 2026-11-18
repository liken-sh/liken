// A Denon changes volume in steps of 0.5, so the driver keeps a volume as
// an integer count of half steps, which has no rounding error. It
// converts to digits or a decimal only for commands and the status.

package denon

import (
	"strconv"
)

// unknownHalves is the count of a volume that the receiver has not
// reported.
const unknownHalves = -1

// ParseHalfSteps converts the digits of a Denon volume message to a
// count of half steps. Two digits are a whole volume ("50" is 50), and a
// third digit is tenths ("505" is 50.5). It returns false for a third
// digit other than 0 or 5.
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

// FormatHalfSteps formats a count of half steps as the volume in the
// status, such as "50" or "50.5". A negative count gives "".
func FormatHalfSteps(halves int) string {
	if halves < 0 {
		return ""
	}
	if halves%2 == 0 {
		return strconv.Itoa(halves / 2)
	}
	return strconv.Itoa(halves/2) + ".5"
}

// HalfStepDigits formats a count of half steps as the digits of a
// volume command: two digits for a whole volume, three for a half.
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

// denonScaleTop is a volume of 98, the top of a Denon's scale.
// SetVolume clamps a higher count to it.
const denonScaleTop = 196
