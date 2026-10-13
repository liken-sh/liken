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
