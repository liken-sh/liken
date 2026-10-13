package sky

// precession answers the rotation from the equinox of J2000.0 to the
// equinox of the Julian day jd, by the rigorous method of chapter 21.
// The three angles ζ, z, and θ are equations 21.3 with the starting
// epoch at J2000.0, where T is zero and only t remains. The rotation
// R3(-z)·R2(θ)·R3(-ζ) is the same transformation as equations 21.4,
// written as a matrix so that its transpose converts back.
func precession(jd float64) matrix {
	t := (jd - J2000) / 36525
	zeta := (2306.2181*t + 0.30188*t*t + 0.017998*t*t*t) / 3600
	z := (2306.2181*t + 1.09468*t*t + 0.018203*t*t*t) / 3600
	theta := (2004.3109*t - 0.42665*t*t - 0.041833*t*t*t) / 3600
	return aboutZ(-z).times(aboutY(theta)).times(aboutZ(-zeta))
}
