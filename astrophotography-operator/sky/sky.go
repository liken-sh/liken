// Package sky converts a position on the sky between the two
// equinoxes that the operator meets. A Target's coordinates and a
// solver's answer are J2000, the equinox of every catalog. An INDI
// mount takes and reports EQUATORIAL_EOD_COORD at the equinox of the
// date, called JNow. The two differ by the precession of the Earth's
// axis since 2000 and by its nutation, about 20 arcminutes for M42 in
// 2026, so a slew to J2000 numbers misses by that much.
//
// The formulas are from Jean Meeus, Astronomical Algorithms, second
// edition: chapter 7 for the Julian day, chapter 17 for the angle
// between two points, chapter 21 for precession, and chapter 22 for
// nutation. The tests hold the book's worked examples. Centering
// converts with the same functions on both sides of a sync (plan 02),
// so an error of the conversion cancels, and the abridged nutation of
// chapter 22 is enough. For the same reason the conversion leaves out
// aberration (chapter 23), a shift of up to 20.5 arcseconds that the
// Earth's motion gives every star: the first slew can miss by that
// much, and the first sync removes it.
package sky

import (
	"math"
	"time"
)

// J2000 is the Julian day of the epoch J2000.0, 2000 January 1.5.
const J2000 = 2451545.0

// Position is a point on the sky in equatorial coordinates, in
// degrees: the right ascension from 0 up to 360, and the declination
// from -90 to 90. The worker's and the solver's answers carry it in
// JSON.
type Position struct {
	RA  float64 `json:"ra"`
	Dec float64 `json:"dec"`
}

// RAHours answers the right ascension in hours, the unit of INDI's
// EQUATORIAL_EOD_COORD.
func (p Position) RAHours() float64 { return p.RA / 15 }

// JulianDay answers the Julian day of a time (chapter 7). The formulas
// of chapters 21 and 22 take Terrestrial Time, which runs about 69
// seconds ahead of UTC in 2026. In 69 seconds the equinox moves by
// less than 0.0002 arcseconds, so the operator passes UTC.
func JulianDay(t time.Time) float64 {
	return float64(t.UnixNano())/float64(24*time.Hour) + 2440587.5
}

// ToDate converts a J2000 position to the equinox of the time: JNow,
// the coordinates an INDI mount takes in EQUATORIAL_EOD_COORD.
func ToDate(p Position, t time.Time) Position {
	jd := JulianDay(t)
	return NutationAt(jd).matrix().times(precession(jd)).apply(p.vector()).position()
}

// FromDate converts a position at the equinox of the time back to
// J2000. Precession and nutation are rotations, so the way back is
// each rotation's transpose in the reverse order.
func FromDate(p Position, t time.Time) Position {
	jd := JulianDay(t)
	return NutationAt(jd).matrix().times(precession(jd)).transpose().apply(p.vector()).position()
}

// Separation answers the angle between two positions, in degrees. It
// takes the angle from the vectors' cross and dot products, which is
// exact at every distance, where the cosine formula of equation 17.1
// loses precision for two points close together, as Meeus warns.
func Separation(a, b Position) float64 {
	u, v := a.vector(), b.vector()
	cross := vector{
		u[1]*v[2] - u[2]*v[1],
		u[2]*v[0] - u[0]*v[2],
		u[0]*v[1] - u[1]*v[0],
	}
	sin := math.Sqrt(cross[0]*cross[0] + cross[1]*cross[1] + cross[2]*cross[2])
	cos := u[0]*v[0] + u[1]*v[1] + u[2]*v[2]
	return degrees(math.Atan2(sin, cos))
}

// vector is a unit vector toward a point on the sky: x toward the
// equinox, z toward the celestial pole.
type vector [3]float64

func (p Position) vector() vector {
	ra, dec := radians(p.RA), radians(p.Dec)
	return vector{math.Cos(dec) * math.Cos(ra), math.Cos(dec) * math.Sin(ra), math.Sin(dec)}
}

func (v vector) position() Position {
	ra := degrees(math.Atan2(v[1], v[0]))
	if ra < 0 {
		ra += 360
	}
	return Position{RA: ra, Dec: degrees(math.Asin(math.Max(-1, math.Min(1, v[2]))))}
}

// matrix is a rotation of the coordinate axes.
type matrix [3][3]float64

func (m matrix) apply(v vector) vector {
	var out vector
	for i := range 3 {
		out[i] = m[i][0]*v[0] + m[i][1]*v[1] + m[i][2]*v[2]
	}
	return out
}

// times answers the rotation m after n.
func (m matrix) times(n matrix) matrix {
	var out matrix
	for i := range 3 {
		for j := range 3 {
			out[i][j] = m[i][0]*n[0][j] + m[i][1]*n[1][j] + m[i][2]*n[2][j]
		}
	}
	return out
}

func (m matrix) transpose() matrix {
	var out matrix
	for i := range 3 {
		for j := range 3 {
			out[i][j] = m[j][i]
		}
	}
	return out
}

// The rotations of the axes about x, y, and z by an angle in degrees,
// in the sense of the IAU's R1, R2, and R3: a positive angle turns the
// axes counterclockwise as seen from the positive end of the axis.
func aboutX(angle float64) matrix {
	s, c := math.Sincos(radians(angle))
	return matrix{{1, 0, 0}, {0, c, s}, {0, -s, c}}
}

func aboutY(angle float64) matrix {
	s, c := math.Sincos(radians(angle))
	return matrix{{c, 0, -s}, {0, 1, 0}, {s, 0, c}}
}

func aboutZ(angle float64) matrix {
	s, c := math.Sincos(radians(angle))
	return matrix{{c, s, 0}, {-s, c, 0}, {0, 0, 1}}
}

func radians(d float64) float64 { return d * math.Pi / 180 }
func degrees(r float64) float64 { return r * 180 / math.Pi }
