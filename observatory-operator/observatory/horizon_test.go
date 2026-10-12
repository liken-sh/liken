package observatory

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
)

func TestAProfileReadsItsPoints(t *testing.T) {
	cases := []struct {
		name, profile string
		want          Profile
	}{
		{"spaces", "0 10\n180 20\n", Profile{{0, 10}, {180, 20}}},
		{"a tab", "0\t10\n180\t20", Profile{{0, 10}, {180, 20}}},
		{"a comma", "0,10\n180, 20\n270 ,30", Profile{{0, 10}, {180, 20}, {270, 30}}},
		{"comments and blank lines", "# traced from the yard\n\n0 10\n  # the oak\n180 20\n\n", Profile{{0, 10}, {180, 20}}},
		{"Windows line ends", "0 10\r\n180 20\r\n", Profile{{0, 10}, {180, 20}}},
		{"signs and decimal points", "+.5 -2.\n359.75 +0.25", Profile{{0.5, -2}, {359.75, 0.25}}},
		{"points out of order", "270 30\n90 10\n180 20", Profile{{90, 10}, {180, 20}, {270, 30}}},
		{"one azimuth twice keeps the file's order", "90 40\n0 5\n90 10", Profile{{0, 5}, {90, 40}, {90, 10}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseProfile(c.profile)
			if err != nil || !slices.Equal(got, c.want) {
				t.Errorf("ParseProfile = %v, %v, want %v", got, err, c.want)
			}
		})
	}
}

func TestAProfileRefusesWhatNoProgramReads(t *testing.T) {
	cases := []struct{ name, profile, refusal string }{
		{"one point", "0 10\n", "at least two points"},
		{"only comments", "# nothing\n", "at least two points"},
		{"three numbers", "0 10 20\n90 10", "line 1"},
		{"a word", "0 10\nnorth 20", "line 2"},
		{"an exponent", "0 10\n1e2 20", "line 2"},
		{"a comment after a point", "0 10 # the oak\n90 10", "line 1"},
		{"two commas", "0,,10\n90 10", "line 1"},
		{"an azimuth past 360", "0 10\n360.5 20", "line 2: the azimuth 360.5 is not from 0 to 360 degrees"},
		{"a negative azimuth", "-1 10\n90 10", "line 1: the azimuth -1 is not from 0 to 360 degrees"},
		{"an altitude past 90", "0 10\n90 90.5", "line 2: the altitude 90.5 is not from -90 to 90 degrees"},
		{"too long", strings.Repeat("#", MaxProfileLength+1), "longer than"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseProfile(c.profile)
			if err == nil || !strings.Contains(err.Error(), c.refusal) {
				t.Errorf("ParseProfile error = %v, want %q", err, c.refusal)
			}
		})
	}
}

func TestAProfileAnswersTheAltitudeInEachDirection(t *testing.T) {
	square := Profile{{0, 10}, {90, 20}, {180, 30}, {270, 20}}
	cases := []struct {
		name    string
		profile Profile
		azimuth float64
		want    float64
	}{
		{"on a point", square, 90, 20},
		{"between two points", square, 45, 15},
		{"between the last point and 360", square, 315, 15},
		{"through 360 to the first point", Profile{{10, 10}, {350, 30}}, 0, 20},
		{"an azimuth past 360", square, 405, 15},
		{"a negative azimuth", square, -45, 15},
		{"a wall at one azimuth takes the higher altitude", Profile{{0, 5}, {90, 40}, {90, 10}, {180, 10}}, 90, 40},
		{"before a wall", Profile{{0, 0}, {90, 40}, {90, 10}, {180, 10}}, 45, 20},
		{"after a wall", Profile{{0, 0}, {90, 40}, {90, 10}, {180, 10}}, 135, 10},
		{"a point at 0 and at 360", Profile{{0, 10}, {180, 20}, {360, 30}}, 0, 30},
		{"just before 360", Profile{{0, 10}, {180, 20}, {360, 30}}, 270, 25},
		{"no points", nil, 120, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.profile.Altitude(c.azimuth); math.Abs(got-c.want) > 1e-9 {
				t.Errorf("Altitude(%v) = %v, want %v", c.azimuth, got, c.want)
			}
		})
	}
}

// longProfile answers a profile of exactly length characters: a point
// for every half degree, and a comment that fills the rest.
func longProfile(length int) string {
	var b strings.Builder
	for azimuth := 0.0; azimuth < 360; azimuth += 0.5 {
		fmt.Fprintf(&b, "%.1f %.2f\n", azimuth, 20*math.Sin(azimuth))
	}
	return "#" + strings.Repeat("-", length-b.Len()-2) + "\n" + b.String()
}

// astrophotography-operator reads a profile with ParseProfile, and the
// API server checks it with the CRD's rules. The two must agree, or
// the scheduler meets a Horizon that the API server accepted and the
// parser refuses. Each case runs through both.
func TestTheParserAcceptsWhatTheAPIServerAccepts(t *testing.T) {
	cases := []struct {
		name, profile string
		accepted      bool
	}{
		{"a file of N.I.N.A.", "0 12\n45 18.5\n90 25\n180 10\n270 15\n", true},
		{"commas and tabs", "0,12\n90\t25\n180 , 10", true},
		{"comments, blank lines, and indents", "# yard\n\n  # the oak\n  0 12\n90 25  \n", true},
		{"Windows line ends", "0 12\r\n90 25\r\n", true},
		{"the limits", "0 -90\n360 90", true},
		{"a negative zero", "-0 -0.0\n-.0 0", true},
		{"leading zeros", "0360 090\n000 -090.000", true},
		{"signs and bare decimal points", "+.5 -2.\n359. +.25", true},
		{"one point", "0 12", false},
		{"no points", "# only a comment\n", false},
		{"an empty profile", "", false},
		{"an azimuth past 360", "0 12\n360.01 10", false},
		{"an azimuth of 361", "0 12\n361 10", false},
		{"an azimuth of 1000", "0 12\n1000 10", false},
		{"a negative azimuth", "-0.5 12\n90 10", false},
		{"an altitude past 90", "0 90.01\n90 10", false},
		{"an altitude of 91", "0 91\n90 10", false},
		{"an altitude past -90", "0 -90.5\n90 10", false},
		{"an altitude of 100", "0 100\n90 10", false},
		{"three numbers", "0 12 5\n90 10", false},
		{"a word", "north 12\n90 10", false},
		{"an exponent", "1e1 12\n90 10", false},
		{"a sign alone", "- 12\n90 10", false},
		{"a decimal point alone", ". 12\n90 10", false},
		{"two commas", "0,,12\n90 10", false},
		{"a trailing comma", "0 12,\n90 10", false},
		{"a comment after a point", "0 12 # oak\n90 10", false},
		{"a semicolon", "0;12\n90 10", false},
		{"the longest profile", longProfile(MaxProfileLength), true},
		{"a profile one character too long", longProfile(MaxProfileLength + 1), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := validate(t, HorizonKind, resource(HorizonKind, map[string]any{"profile": c.profile}), nil)
			_, err := ParseProfile(c.profile)
			if apiAccepts, parserAccepts := len(errs) == 0, err == nil; apiAccepts != c.accepted || parserAccepts != c.accepted {
				t.Errorf("API server accepts = %v (%v), parser accepts = %v (%v), want %v",
					apiAccepts, errs.ToAggregate(), parserAccepts, err, c.accepted)
			}
		})
	}
}
