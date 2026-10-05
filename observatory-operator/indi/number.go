package indi

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ParseNumber reads a number as INDI writes it: a decimal number, or a
// sexagesimal one such as "12:30:00" or "-0:30", with ':' or ';'
// between the parts. A sexagesimal number is degrees or hours,
// minutes, and seconds, and its sign applies to the whole value. It
// ignores whitespace anywhere in the text, as f_scansexa in libindi
// does, because a driver writes each value on a line of its own.
func ParseNumber(text string) (float64, error) {
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
	if value, err := strconv.ParseFloat(compact, 64); err == nil {
		return value, nil
	}
	negative := strings.HasPrefix(compact, "-")
	parts := strings.FieldsFunc(strings.TrimPrefix(compact, "-"), func(r rune) bool {
		return r == ':' || r == ';'
	})
	if len(parts) == 0 || len(parts) > 3 {
		return 0, fmt.Errorf("%q is not an INDI number", text)
	}
	value := 0.0
	for i, part := range parts {
		n, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not an INDI number", text)
		}
		value += n / []float64{1, 60, 3600}[i]
	}
	if negative {
		value = -value
	}
	return value, nil
}

// sexagesimal matches INDI's own number format, %<w>.<f>m. libindi
// reads it with sscanf("%%%d.%d%c"), so a 0 flag is part of the width.
var sexagesimal = regexp.MustCompile(`^%(\d+)\.(\d+)m$`)

// printf matches the printf formats that drivers use for numbers.
var printf = regexp.MustCompile(`^%[-+ 0#]*\d*(\.\d*)?([diefgEG])$`)

// FormatNumber prints a value in a member's format, the way libindi's
// numberFormat prints it for a person. A format %<w>.<f>m prints a
// sexagesimal value <w> characters wide, and <f> chooses its parts:
// 3 gives d:mm, 5 gives d:mm.m, 6 gives d:mm:ss, 8 gives d:mm:ss.s,
// and 9 gives d:mm:ss.ss. A printf format prints as printf does. Any
// other format, or none, prints the shortest decimal that reads back
// as the same value.
func FormatNumber(format string, value float64) string {
	if match := sexagesimal.FindStringSubmatch(format); match != nil {
		width, _ := strconv.Atoi(match[1])
		fraction, _ := strconv.Atoi(match[2])
		return formatSexagesimal(value, width-fraction, fraction)
	}
	if match := printf.FindStringSubmatch(format); match != nil {
		switch match[2] {
		case "d":
			return fmt.Sprintf(format, int64(value))
		case "i":
			return fmt.Sprintf(strings.TrimSuffix(format, "i")+"d", int64(value))
		}
		return fmt.Sprintf(format, value)
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// formatSexagesimal follows fs_sexa in libindi: it rounds the value to
// the smallest part that the format prints, and prints the whole part
// width characters wide.
func formatSexagesimal(value float64, width, fraction int) string {
	base := map[int]int64{9: 360000, 8: 36000, 6: 3600, 5: 600}[fraction]
	if base == 0 {
		base = 60
	}
	negative := value < 0
	if negative {
		value = -value
	}
	n := int64(value*float64(base) + 0.5)
	whole, rest := n/base, n%base
	var out string
	if negative {
		out = fmt.Sprintf("%*s", width, "-"+strconv.FormatInt(whole, 10))
	} else {
		out = fmt.Sprintf("%*d", width, whole)
	}
	perMinute := base / 60
	minutes, seconds := rest/perMinute, rest%perMinute
	switch base {
	case 60:
		return out + fmt.Sprintf(":%02d", minutes)
	case 600:
		return out + fmt.Sprintf(":%02d.%d", rest/10, rest%10)
	case 3600:
		return out + fmt.Sprintf(":%02d:%02d", minutes, seconds)
	case 36000:
		return out + fmt.Sprintf(":%02d:%02d.%d", minutes, seconds/10, seconds%10)
	}
	return out + fmt.Sprintf(":%02d:%02d.%02d", minutes, seconds/100, seconds%100)
}
