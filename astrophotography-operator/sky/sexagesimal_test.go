package sky

import (
	"testing"
)

func TestParseRA(t *testing.T) {
	cases := []struct {
		text string
		want float64
		err  bool
	}{
		{"05:35:17.3", 83.822083, false},
		{"00:00:00", 0, false},
		{"23:59:59.9", 359.999583, false},
		{"5:35:17", 83.820833, false},
		{"24:00:00", 0, true},
		{"05:60:00", 0, true},
		{"05:35", 0, true},
		{"-05:35:17", 0, true},
		{"05h35m17s", 0, true},
		{"05:3x:17", 0, true},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			got, err := ParseRA(c.text)
			if (err != nil) != c.err || !near(got, c.want, 1e-6) {
				t.Errorf("ParseRA = %f, %v; want %f, error %v", got, err, c.want, c.err)
			}
		})
	}
}

func TestParseDec(t *testing.T) {
	cases := []struct {
		text string
		want float64
		err  bool
	}{
		{"-05:23:28", -5.391111, false},
		{"+49:13:42.48", 49.228467, false},
		{"49:13:42.48", 49.228467, false},
		// The sign belongs to the whole angle, so a declination just south
		// of the equator keeps its sign with a degree of zero.
		{"-00:30:00", -0.5, false},
		{"+90:00:00", 90, false},
		{"-90:00:00", -90, false},
		{"+90:00:01", 0, true},
		{"10:61:00", 0, true},
		{"10:00", 0, true},
		{"", 0, true},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			got, err := ParseDec(c.text)
			if (err != nil) != c.err || !near(got, c.want, 1e-6) {
				t.Errorf("ParseDec = %f, %v; want %f, error %v", got, err, c.want, c.err)
			}
		})
	}
}

func TestFormatRA(t *testing.T) {
	cases := []struct {
		degrees float64
		want    string
	}{
		{83.822083, "05:35:17.3"},
		{0, "00:00:00.0"},
		// A value that rounds up to 24 hours is the same point as 0.
		{359.9999999, "00:00:00.0"},
		{-15, "23:00:00.0"},
		{29.6070, "01:58:25.7"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := FormatRA(c.degrees); got != c.want {
				t.Errorf("FormatRA(%f) = %q, want %q", c.degrees, got, c.want)
			}
		})
	}
}

func TestFormatDec(t *testing.T) {
	cases := []struct {
		degrees float64
		want    string
	}{
		{-5.391111, "-05:23:28.0"},
		{59.8193, "+59:49:09.5"},
		{-0.5, "-00:30:00.0"},
		{-0.00000001, "+00:00:00.0"},
		{90, "+90:00:00.0"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := FormatDec(c.degrees); got != c.want {
				t.Errorf("FormatDec(%f) = %q, want %q", c.degrees, got, c.want)
			}
		})
	}
}

// What FormatRA and FormatDec write, ParseRA and ParseDec read back.
func TestFormatReadsBack(t *testing.T) {
	ra, err := ParseRA(FormatRA(83.822083))
	if err != nil || !near(ra, 83.822083, 1.5/3600) {
		t.Errorf("ParseRA(FormatRA) = %f, %v", ra, err)
	}
	dec, err := ParseDec(FormatDec(-5.391111))
	if err != nil || !near(dec, -5.391111, 0.05/3600) {
		t.Errorf("ParseDec(FormatDec) = %f, %v", dec, err)
	}
}
