package sky

import "math"

// Nutation is the nutation of the Earth's axis at one moment, in
// degrees: the nutation in longitude Δψ and in obliquity Δε, and the
// mean obliquity of the ecliptic ε0.
type Nutation struct {
	Longitude     float64
	Obliquity     float64
	MeanObliquity float64
}

// TrueObliquity answers ε = ε0 + Δε.
func (n Nutation) TrueObliquity() float64 { return n.MeanObliquity + n.Obliquity }

// NutationAt answers the nutation at the Julian day jd. Δψ and Δε come
// from the abridged series of chapter 22, which is accurate to 0.5
// arcseconds in Δψ and 0.1 arcseconds in Δε. The full series of 63
// terms gains less than that, and centering cancels any error of the
// conversion (the package's comment). ε0 is equation 22.2.
func NutationAt(jd float64) Nutation {
	t := (jd - J2000) / 36525
	// The mean longitudes of the Sun and the Moon, and the longitude of
	// the ascending node of the Moon's orbit.
	sun := radians(280.4665 + 36000.7698*t)
	moon := radians(218.3165 + 481267.8813*t)
	node := radians(125.04452 - 1934.136261*t + 0.0020708*t*t + t*t*t/450000)
	longitude := -17.20*math.Sin(node) - 1.32*math.Sin(2*sun) - 0.23*math.Sin(2*moon) + 0.21*math.Sin(2*node)
	obliquity := 9.20*math.Cos(node) + 0.57*math.Cos(2*sun) + 0.10*math.Cos(2*moon) - 0.09*math.Cos(2*node)
	mean := 84381.448 - 46.8150*t - 0.00059*t*t + 0.001813*t*t*t
	return Nutation{Longitude: longitude / 3600, Obliquity: obliquity / 3600, MeanObliquity: mean / 3600}
}

// matrix answers the rotation from the mean equator and equinox of the
// date to the true ones: R1(-ε)·R3(-Δψ)·R1(ε0). To first order it moves
// a star by the Δα and Δδ of equation 23.1.
func (n Nutation) matrix() matrix {
	return aboutX(-n.TrueObliquity()).times(aboutZ(-n.Longitude)).times(aboutX(n.MeanObliquity))
}
