package sky

// Each case is a worked example from Jean Meeus, Astronomical
// Algorithms, second edition (1998), named by its chapter and letter.
// The book prints each result to a stated precision, and a case checks
// to that precision unless the case says why it checks to less.

import (
	"math"
	"testing"
	"time"
)

// arcseconds converts arcseconds to degrees.
func arcseconds(a float64) float64 { return a / 3600 }

// dms converts degrees, minutes, and seconds of arc to degrees.
func dms(d, m, s float64) float64 { return d + m/60 + s/3600 }

func near(got, want, tolerance float64) bool { return math.Abs(got-want) <= tolerance }

func TestJulianDay(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		want float64
	}{
		// J2000.0 itself, 2000 January 1.5.
		{"the epoch J2000.0", time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC), 2451545.0},
		// Example 7.a: 1957 October 4.81, the launch of Sputnik 1.
		{"example 7.a", time.Date(1957, 10, 4, 19, 26, 24, 0, time.UTC), 2436116.31},
		// The date of examples 21.b and 23.a: 2028 November 13.19.
		{"example 21.b", time.Date(2028, 11, 13, 4, 33, 36, 0, time.UTC), 2462088.69},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := JulianDay(c.at); !near(got, c.want, 1e-6) {
				t.Errorf("JulianDay = %.6f, want %.6f", got, c.want)
			}
		})
	}
}

// Example 21.b: θ Persei, at J2000.0 with its proper motion of 28.86705
// years applied, precessed to the equinox of 2028 November 13.19 TD by
// the rigorous method. The book prints the result to 0.000001°.
func TestPrecessFromJ2000(t *testing.T) {
	cases := []struct {
		name  string
		from  Position
		jd    float64
		want  Position
		limit float64
	}{
		{"example 21.b", Position{RA: 41.054063, Dec: 49.227750}, 2462088.69,
			Position{RA: 41.547214, Dec: 49.348483}, 0.000002},
		{"no time passes at J2000.0", Position{RA: 83.822083, Dec: -5.391111}, J2000,
			Position{RA: 83.822083, Dec: -5.391111}, 1e-12},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := precession(c.jd).apply(c.from.vector()).position()
			if !near(got.RA, c.want.RA, c.limit) || !near(got.Dec, c.want.Dec, c.limit) {
				t.Errorf("precessed to %+v, want %+v", got, c.want)
			}
		})
	}
}

// Example 22.a: 1987 April 10, 0h TD. The book computes the nutation
// from the full table of 63 terms. This package uses the abridged
// series of the same chapter, which Meeus gives as accurate to 0.5″ in
// longitude and 0.1″ in obliquity, so those are the limits here. The
// mean obliquity comes from equation 22.2, the same formula the book
// uses, so it checks to the book's 0.001″.
func TestNutation(t *testing.T) {
	n := NutationAt(2446895.5)
	cases := []struct {
		name       string
		got, want  float64
		limitInArc float64
	}{
		{"nutation in longitude", n.Longitude, arcseconds(-3.788), 0.5},
		{"nutation in obliquity", n.Obliquity, arcseconds(9.443), 0.1},
		{"mean obliquity", n.MeanObliquity, dms(23, 26, 27.407), 0.001},
		{"true obliquity", n.TrueObliquity(), dms(23, 26, 36.850), 0.1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !near(c.got, c.want, arcseconds(c.limitInArc)) {
				t.Errorf("%s = %.3f″, want %.3f″", c.name, c.got*3600, c.want*3600)
			}
		})
	}
}

// Example 23.a: θ Persei on 2028 November 13.19 TD, after precession.
// With the book's nutation, Δψ = +14.861″ and Δε = +2.705″, and the
// obliquity of 23.436°, the nutation moves the star by Δα = +15.843″
// and Δδ = +6.218″. The book takes them from the first-order formulas
// of equation 23.1, and this package rotates the vector, so the two
// agree to far less than the book's 0.001″.
func TestNutationMovesAStar(t *testing.T) {
	precessed := Position{RA: 41.547214, Dec: 49.348483}
	n := Nutation{Longitude: arcseconds(14.861), Obliquity: arcseconds(2.705), MeanObliquity: 23.436 - arcseconds(2.705)}
	got := n.matrix().apply(precessed.vector()).position()
	cases := []struct {
		name      string
		got, want float64
	}{
		{"in right ascension", (got.RA - precessed.RA) * 3600, 15.843},
		{"in declination", (got.Dec - precessed.Dec) * 3600, 6.218},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !near(c.got, c.want, 0.002) {
				t.Errorf("moved %.4f″, want %.3f″", c.got, c.want)
			}
		})
	}
}

// The conversion that a slew uses is precession and then nutation, and
// the way back undoes both. A round trip must return the start to far
// better than any limit the operator applies.
func TestToDateAndBack(t *testing.T) {
	at := time.Date(2026, 10, 12, 23, 21, 6, 0, time.UTC)
	cases := []struct {
		name string
		from Position
	}{
		{"M42", Position{RA: 83.822083, Dec: -5.391111}},
		{"near the celestial pole", Position{RA: 37.954, Dec: 89.264}},
		{"right ascension near zero", Position{RA: 0.0001, Dec: 10}},
		{"right ascension near 360", Position{RA: 359.9999, Dec: -45}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			back := FromDate(ToDate(c.from, at), at)
			if separation := Separation(back, c.from); separation > arcseconds(1e-6) {
				t.Errorf("the round trip ended %.2e″ from the start", separation*3600)
			}
		})
	}
}

// Plan 02 measured the cost of the epoch for M42 in 2026: the mount
// took the J2000 coordinates as JNow, and the frame's center was about
// 20 arcminutes from M42. The conversion must move M42 by that much.
func TestTheEpochMovesM42ByAboutTwentyArcminutes(t *testing.T) {
	m42 := Position{RA: 83.822083, Dec: -5.391111}
	moved := Separation(m42, ToDate(m42, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC))) * 60
	if moved < 19 || moved > 22 {
		t.Errorf("M42 moved %.1f′, want about 20′", moved)
	}
}

// Example 17.a: the angular distance from Arcturus to Spica.
func TestSeparation(t *testing.T) {
	cases := []struct {
		name string
		a, b Position
		want float64
	}{
		{"example 17.a", Position{RA: 213.9154, Dec: 19.1825}, Position{RA: 201.2983, Dec: -11.1614}, 32.7930},
		{"the same point", Position{RA: 83.8, Dec: -5.4}, Position{RA: 83.8, Dec: -5.4}, 0},
		{"two points one arcsecond apart", Position{RA: 10, Dec: 0}, Position{RA: 10 + arcseconds(1), Dec: 0}, arcseconds(1)},
		{"the two poles", Position{RA: 0, Dec: 90}, Position{RA: 180, Dec: -90}, 180},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Separation(c.a, c.b); !near(got, c.want, 0.0001) {
				t.Errorf("Separation = %.4f°, want %.4f°", got, c.want)
			}
		})
	}
}

func TestRAHours(t *testing.T) {
	if got := (Position{RA: 83.822083}).RAHours(); !near(got, 5.588139, 1e-6) {
		t.Errorf("RAHours = %f, want 5.588139", got)
	}
}
