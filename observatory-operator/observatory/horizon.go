package observatory

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A Horizon is the part of the sky that trees, buildings, and roofs
// block from one place, such as the yard or a roof. It belongs to a
// place, not to the site, because two piers at one site see different
// treelines. A Telescope names the Horizon of where it stands in
// spec.horizon, so a portable telescope that moves takes another saved
// Horizon with one edit. Only a scheduler reads it: the operator
// starts nothing for a Horizon and writes no status to it.
//
// The profile keeps the text file that N.I.N.A. and Cartes du Ciel
// read and write, so a person pastes a traced horizon in and copies it
// out with no conversion. ParseProfile and Altitude are here, beside
// the type, so astrophotography-operator reads a horizon with the same
// code that these tests hold.

type Horizon = Object[HorizonSpec, HorizonStatus]

type HorizonSpec struct {
	// Profile is the horizon file: one point to a line, the azimuth
	// and then the altitude in degrees (ParseProfile).
	Profile string `json:"profile"`
}

// HorizonStatus is empty, because the operator writes nothing to a
// Horizon. Its CRD has no status, and the Object envelope omits a
// zero status.
type HorizonStatus struct{}

// ConditionHorizonFound is True when the Horizon that a Telescope's
// spec.horizon names exists. A Telescope with no spec.horizon has no
// such condition.
const ConditionHorizonFound = "HorizonFound"

// MaxProfileLength is the maxLength of spec.profile, in characters.
// The API server estimates the cost of the CRD's rules from it, and
// refuses a CRD whose estimate is over its budget. 16384 characters
// holds a point for every half degree, with comments.
const MaxProfileLength = 16384

// Point is one point of a profile, in degrees: the azimuth from north
// through east, and the altitude above the mathematical horizon.
type Point struct {
	Azimuth  float64
	Altitude float64
}

// Profile is a horizon's points, sorted by azimuth.
type Profile []Point

// The lines that a profile holds. These are the same expressions as
// the CRD's rules, and TestTheParserAcceptsWhatTheAPIServerAccepts
// holds the two to the same cases. A number has no exponent, because
// no horizon file writes one.
var (
	skippedLine = regexp.MustCompile(`^[ \t\r]*(#.*)?$`)
	pointLine   = regexp.MustCompile(`^[ \t]*([-+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+))(?:[ \t]*,[ \t]*|[ \t]+)([-+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+))[ \t\r]*$`)
)

// ParseProfile reads a horizon file. Each line is a comment that
// starts with #, a blank line, or a point: an azimuth from 0 to 360
// and an altitude from -90 to 90, in degrees, separated by spaces, a
// tab, or a comma. A profile needs at least two points, as N.I.N.A.
// does. ParseProfile sorts the points by azimuth and keeps the file's
// order of two points at one azimuth, which together draw a vertical
// edge, such as the side of a wall.
func ParseProfile(text string) (Profile, error) {
	if n := utf8.RuneCountInString(text); n > MaxProfileLength {
		return nil, fmt.Errorf("the profile holds %d characters, longer than %d", n, MaxProfileLength)
	}
	var profile Profile
	for i, line := range strings.Split(text, "\n") {
		if skippedLine.MatchString(line) {
			continue
		}
		numbers := pointLine.FindStringSubmatch(line)
		if numbers == nil {
			return nil, fmt.Errorf("line %d: %q is not a comment, a blank line, or an azimuth and an altitude", i+1, line)
		}
		// The expression admits only numbers that ParseFloat reads.
		azimuth, _ := strconv.ParseFloat(numbers[1], 64)
		altitude, _ := strconv.ParseFloat(numbers[2], 64)
		if azimuth < 0 || azimuth > 360 {
			return nil, fmt.Errorf("line %d: the azimuth %s is not from 0 to 360 degrees", i+1, numbers[1])
		}
		if altitude < -90 || altitude > 90 {
			return nil, fmt.Errorf("line %d: the altitude %s is not from -90 to 90 degrees", i+1, numbers[2])
		}
		profile = append(profile, Point{azimuth, altitude})
	}
	if len(profile) < 2 {
		return nil, fmt.Errorf("the profile holds %d points, and needs at least two points", len(profile))
	}
	slices.SortStableFunc(profile, func(a, b Point) int { return compareFloats(a.Azimuth, b.Azimuth) })
	return profile, nil
}

func compareFloats(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Altitude answers the horizon's altitude at an azimuth in degrees,
// which can be outside 0 to 360. Between two points, the altitude
// changes in a straight line with the azimuth, and the last point
// joins the first through 360 degrees, as N.I.N.A. interpolates. Where
// two points share an azimuth, the higher altitude counts, because
// the sky is blocked up to the top of the edge. A profile with no
// points is flat at 0 degrees.
func (p Profile) Altitude(azimuth float64) float64 {
	if len(p) == 0 {
		return 0
	}
	azimuth = math.Mod(azimuth, 360)
	if azimuth < 0 {
		azimuth += 360
	}
	// The last point, a turn earlier, and the first point, a turn
	// later, close the circle, so every azimuth from 0 to 360 falls on
	// a segment.
	first, last := p[0], p[len(p)-1]
	around := append(append(Profile{{last.Azimuth - 360, last.Altitude}}, p...), Point{first.Azimuth + 360, first.Altitude})
	altitude := math.Inf(-1)
	for i := 1; i < len(around); i++ {
		from, to := around[i-1], around[i]
		if azimuth < from.Azimuth || azimuth > to.Azimuth {
			continue
		}
		on := max(from.Altitude, to.Altitude)
		if to.Azimuth > from.Azimuth {
			on = from.Altitude + (to.Altitude-from.Altitude)*(azimuth-from.Azimuth)/(to.Azimuth-from.Azimuth)
		}
		altitude = max(altitude, on)
	}
	return altitude
}
