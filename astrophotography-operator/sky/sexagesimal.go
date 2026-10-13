package sky

// A Target states its coordinates the way SIMBAD and KStars print them:
// the right ascension in hours as HH:MM:SS.s and the declination in
// degrees as ±DD:MM:SS.s (plan 01). The CRD checks the form, and these
// read it. A Target's status.centering records a solved center in the
// same form, so a person compares it with spec.coordinates by eye.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseRA reads a right ascension in hours, such as 05:35:17.3, and
// answers it in degrees.
func ParseRA(text string) (float64, error) {
	if strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
		return 0, fmt.Errorf("right ascension %q has a sign", text)
	}
	hours, err := sexagesimal(text)
	if err != nil {
		return 0, fmt.Errorf("right ascension %q: %w", text, err)
	}
	if hours >= 24 {
		return 0, fmt.Errorf("right ascension %q is 24 hours or more", text)
	}
	return hours * 15, nil
}

// ParseDec reads a declination in degrees, such as -05:23:28, and
// answers it in degrees.
func ParseDec(text string) (float64, error) {
	sign := 1.0
	switch {
	case strings.HasPrefix(text, "-"):
		sign, text = -1, text[1:]
	case strings.HasPrefix(text, "+"):
		text = text[1:]
	}
	degrees, err := sexagesimal(text)
	if err != nil {
		return 0, fmt.Errorf("declination %q: %w", text, err)
	}
	if degrees > 90 {
		return 0, fmt.Errorf("declination %q is beyond a pole", text)
	}
	return sign * degrees, nil
}

// sexagesimal reads an unsigned A:MM:SS.s as A + MM/60 + SS.s/3600.
func sexagesimal(text string) (float64, error) {
	parts := strings.Split(text, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("want three parts separated by ':'")
	}
	var values [3]float64
	for i, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil || value < 0 || strings.ContainsAny(part, "+-eE") {
			return 0, fmt.Errorf("%q is not a number", part)
		}
		values[i] = value
	}
	if values[1] >= 60 || values[2] >= 60 {
		return 0, fmt.Errorf("minutes and seconds are each less than 60")
	}
	return values[0] + values[1]/60 + values[2]/3600, nil
}

// FormatRA writes a right ascension in degrees as hours, HH:MM:SS.s,
// rounded to a tenth of a second of time, 1.5 arcseconds at most.
func FormatRA(degrees float64) string {
	const day = 24 * 36000
	tenths := int64(math.Round(degrees / 15 * 36000))
	tenths = (tenths%day + day) % day
	return sexagesimalText("", tenths)
}

// FormatDec writes a declination in degrees as ±DD:MM:SS.s, rounded to
// a tenth of an arcsecond.
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

// sexagesimalText writes a count of tenths of a second as A:MM:SS.s.
func sexagesimalText(sign string, tenths int64) string {
	return fmt.Sprintf("%s%02d:%02d:%02d.%d", sign, tenths/36000, tenths/600%60, tenths/10%60, tenths%10)
}
