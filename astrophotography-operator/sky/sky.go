// Package sky converts positions on the sky between J2000 and JNow.
// A Target's coordinates and a plate solver's results are J2000, the
// equinox that star catalogs use. An INDI mount takes and reports
// EQUATORIAL_EOD_COORD at the equinox of the current date, which is
// JNow. The Earth's axis has precessed and nutated since 2000, so the
// two differ by about 20 arcminutes for M42 in 2026. A slew that sends
// J2000 numbers to the mount misses by that much.
//
// The formulas are from Jean Meeus, Astronomical Algorithms, second
// edition: chapter 7 for the Julian day, chapter 17 for the angle
// between two points, chapter 21 for precession, and chapter 22 for
// nutation. The tests check the book's worked examples.
//
// The runner centers a target by syncing the mount and slewing again,
// and both convert with ToDate, so a conversion error cancels out.
// That is why the abridged nutation series of chapter 22 is accurate
// enough, and why the conversion omits aberration (chapter 23). Aberration shifts a
// star by up to 20.5 arcseconds, so the first slew can miss by that
// much, and the first sync removes the error.
package sky

import (
	"math"
	"time"
)

// J2000 is the Julian day of the epoch J2000.0, 2000 January 1.5.
const J2000 = 2451545.0

// Position is a point on the sky in equatorial coordinates, in
// degrees: right ascension from 0 up to 360, and declination from -90
// to 90. The worker and the solver send it as JSON.
type Position struct {
	RA  float64 `json:"ra"`
	Dec float64 `json:"dec"`
}

// RAHours returns the right ascension in hours, the unit of INDI's
// EQUATORIAL_EOD_COORD.
func (p Position) RAHours() float64 { return p.RA / 15 }

// JulianDay returns the Julian day of a time (chapter 7). The formulas
// of chapters 21 and 22 take Terrestrial Time, which is about 69
// seconds ahead of UTC in 2026. The equinox moves less than 0.0002
// arcseconds in 69 seconds, so the operator passes UTC.
func JulianDay(t time.Time) float64 {
	return float64(t.UnixNano())/float64(24*time.Hour) + 2440587.5
}

// ToDate converts a J2000 position to JNow at the given time, the
// coordinates that an INDI mount takes in EQUATORIAL_EOD_COORD.
func ToDate(p Position, t time.Time) Position {
	jd := JulianDay(t)
	return NutationAt(jd).matrix().times(precession(jd)).apply(p.vector()).position()
}

// FromDate converts a JNow position at the given time back to J2000.
// Precession and nutation are rotations, so the inverse is the
// transpose of the combined rotation.
func FromDate(p Position, t time.Time) Position {
	jd := JulianDay(t)
	return NutationAt(jd).matrix().times(precession(jd)).transpose().apply(p.vector()).position()
}

// Separation returns the angle between two positions, in degrees. It
// takes the atan2 of the cross and dot products, because the cosine
// formula of equation 17.1 loses precision for points close together.
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

// vector is a unit vector toward a point on the sky. The x axis points
// at the equinox and the z axis at the celestial pole.
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

// times returns the product m·n, the rotation that applies n first and
// then m.
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

// aboutX, aboutY, and aboutZ rotate the coordinate axes about x, y, and
// z by an angle in degrees, as the IAU's R1, R2, and R3 do. A positive
// angle turns the axes counterclockwise as seen from the positive end
// of the axis.
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
