package indi

import (
	"math"
	"testing"
)

func TestParseNumber(t *testing.T) {
	cases := []struct {
		text string
		want float64
	}{
		{"50000", 50000},
		{"\n50000\n    ", 50000},
		{"-6", -6},
		{"8.7899999999999991473", 8.7899999999999991473},
		{"1e-06", 1e-06},
		{"-3.4028234663852885981e+38", -3.4028234663852885981e+38},
		{"12:30:00", 12.5},
		{"12:30", 12.5},
		{"-0:30:00", -0.5},
		{"-12:30:36", -12.51},
		{"5:35:17.5", 5 + 35.0/60 + 17.5/3600},
		{" 12 : 30 ", 12.5},
		{"12;30;00", 12.5},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			got, err := ParseNumber(c.text)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(got-c.want) > 1e-12*math.Max(1, math.Abs(c.want)) {
				t.Errorf("ParseNumber(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}

func TestParseNumberRefuses(t *testing.T) {
	for _, text := range []string{"", "  ", "On", "1:2:3:4", "1:x:3", ":"} {
		t.Run(text, func(t *testing.T) {
			if got, err := ParseNumber(text); err == nil {
				t.Errorf("ParseNumber(%q) = %v, want an error", text, got)
			}
		})
	}
}

func TestFormatNumber(t *testing.T) {
	cases := []struct {
		format string
		value  float64
		want   string
	}{
		{"%010.6m", 12.5, "  12:30:00"},
		{"%010.6m", -5.39, "  -5:23:24"},
		{"%010.6m", -0.5, "  -0:30:00"},
		{"%012.8m", 5.588, "   5:35:16.8"},
		{"%10.6m", 359.999999, " 360:00:00"},
		{"%7.3m", 1.5, "   1:30"},
		{"%8.5m", 1.51, "  1:30.6"},
		{"%11.9m", 1.51, " 1:30:36.00"},
		{"%.f", 50000, "50000"},
		{"%.2f", 1.005, "1.00"},
		{"%6.2f", 30, " 30.00"},
		{"%g", 0.1, "0.1"},
		{"%d", 42.7, "42"},
		{"%i", 42, "42"},
		{"", 8.79, "8.79"},
		{"%s", 8.79, "8.79"},
	}
	for _, c := range cases {
		t.Run(c.format, func(t *testing.T) {
			if got := FormatNumber(c.format, c.value); got != c.want {
				t.Errorf("FormatNumber(%q, %v) = %q, want %q", c.format, c.value, got, c.want)
			}
		})
	}
}
