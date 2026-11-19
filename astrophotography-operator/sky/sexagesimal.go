package sky

// A Target's spec.coordinates use the format that SIMBAD and KStars
// print: right ascension in hours as HH:MM:SS.s, and declination in
// degrees as ±DD:MM:SS.s. The CRD validates the format. This package
// converts between that text and degrees. status.centering uses the
// same format, so a person can compare a solved center with
// spec.coordinates directly.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseRA converts a right ascension in hours, such as 05:35:17.3, to
// degrees.
func ParseRA(text string) (float64, error) {
	if strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
		return 0, fmt.Errorf("the right ascension %q has a sign, and a right ascension is unsigned", text)
	}
	hours, err := sexagesimal(text)
	if err != nil {
		return 0, fmt.Errorf("reading the right ascension %q: %w", text, err)
	}
	if hours >= 24 {
		return 0, fmt.Errorf("the right ascension %q is 24 hours or more, and must be below 24 hours", text)
	}
	return hours * 15, nil
}

// ParseDec converts a declination such as -05:23:28 to degrees. The
// sign is optional.
func ParseDec(text string) (float64, error) {
	sign, unsigned := 1.0, text
	switch {
	case strings.HasPrefix(text, "-"):
		sign, unsigned = -1, text[1:]
	case strings.HasPrefix(text, "+"):
		unsigned = text[1:]
	}
	degrees, err := sexagesimal(unsigned)
	if err != nil {
		return 0, fmt.Errorf("reading the declination %q: %w", text, err)
	}
	if degrees > 90 {
		return 0, fmt.Errorf("the declination %q is beyond 90 degrees", text)
	}
	return sign * degrees, nil
}

// sexagesimal reads an unsigned A:MM:SS.s as A + MM/60 + SS.s/3600.
func sexagesimal(text string) (float64, error) {
	parts := strings.Split(text, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("%q has %d parts separated by ':', and needs 3", text, len(parts))
	}
	var values [3]float64
	for i, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil || value < 0 || strings.ContainsAny(part, "+-eE") {
			return 0, fmt.Errorf("%q is not an unsigned decimal number", part)
		}
		values[i] = value
	}
	if values[1] >= 60 || values[2] >= 60 {
		return 0, fmt.Errorf("the minutes %g and the seconds %g must each be below 60", values[1], values[2])
	}
	return values[0] + values[1]/60 + values[2]/3600, nil
}

// FormatRA converts a right ascension in degrees to HH:MM:SS.s,
// rounded to a tenth of a second of time. That is 1.5 arcseconds of
// sky at most.
func FormatRA(degrees float64) string {
	const day = 24 * 36000
	tenths := int64(math.Round(degrees / 15 * 36000))
	tenths = (tenths%day + day) % day
	return sexagesimalText("", tenths)
}

// FormatDec converts a declination in degrees to ±DD:MM:SS.s, rounded
// to a tenth of an arcsecond.
func FormatDec(degrees float64) string {
	sign := "+"
	if degrees < 0 {
		sign = "-"
	}
	tenths := int64(math.Round(math.Abs(degrees) * 36000))
	if tenths == 0 {
		sign = "+"
	}
	return sexagesimalText(sign, tenths)
}

// sexagesimalText formats a count of tenths of a second as A:MM:SS.s.
func sexagesimalText(sign string, tenths int64) string {
	return fmt.Sprintf("%s%02d:%02d:%02d.%d", sign, tenths/36000, tenths/600%60, tenths/10%60, tenths%10)
}
