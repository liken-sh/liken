package sky

// The local sky. The operator images a target only while the target
// stands above the telescope's horizon, so it needs the target's
// altitude and azimuth at the site: chapter 12 for the sidereal time,
// and chapter 13 for the conversion. The altitude is geometric, with
// no refraction: refraction lifts a star at the horizon by about half
// a degree, so a target counts as below its horizon a little before it
// disappears behind it.

import (
	"math"
	"time"
)

// Site is a place on the Earth, in degrees: the latitude north, and
// the longitude east, as an Observatory's spec.location gives them.
type Site struct {
	Latitude  float64
	Longitude float64
}

// Horizontal is a position in the local sky, in degrees: the altitude
// above the mathematical horizon, and the azimuth from north through
// east, the way a horizon file measures it. Meeus measures the azimuth
// from the south.
type Horizontal struct {
	Altitude float64
	Azimuth  float64
}

// SiderealTime answers the apparent sidereal time at Greenwich, in
// degrees from 0 up to 360: the mean sidereal time of equation 12.4,
// plus the nutation in right ascension, Δψ cos ε.
func SiderealTime(t time.Time) float64 {
	jd := JulianDay(t)
	c := (jd - J2000) / 36525
	mean := 280.46061837 + 360.98564736629*(jd-J2000) + 0.000387933*c*c - c*c*c/38710000
	n := NutationAt(jd)
	return normalize(mean + n.Longitude*math.Cos(radians(n.TrueObliquity())))
}

// ToHorizontal converts a position at the equinox of the date, as
// ToDate answers it, to the local sky of a site at a time, with
// equations 13.5 and 13.6.
func ToHorizontal(jnow Position, site Site, t time.Time) Horizontal {
	return horizontalAt(jnow, site, SiderealTime(t))
}

// horizontalAt converts with a sidereal time already computed, so a
// search through a night computes the position's precession and
// nutation once.
func horizontalAt(jnow Position, site Site, sidereal float64) Horizontal {
	hourAngle := radians(sidereal + site.Longitude - jnow.RA)
	latitude, dec := radians(site.Latitude), radians(jnow.Dec)
	south := math.Atan2(math.Sin(hourAngle), math.Cos(hourAngle)*math.Sin(latitude)-math.Tan(dec)*math.Cos(latitude))
	altitude := math.Asin(math.Sin(latitude)*math.Sin(dec) + math.Cos(latitude)*math.Cos(dec)*math.Cos(hourAngle))
	return Horizontal{Altitude: degrees(altitude), Azimuth: normalize(degrees(south) + 180)}
}

// siderealRate is the turn of the sidereal time in one day of UT, in
// degrees, from equation 12.4.
const siderealRate = 360.98564736629

// NextAbove answers the first minute after from at which a J2000
// position stands above a horizon at the site, and false when it does
// not within a day, such as a star that never rises there. horizon
// answers the horizon's altitude at an azimuth. A horizon can hide and
// show a target more than once in a night, so the search steps through
// the day instead of solving for one crossing. In one day, precession
// and nutation move a star by less than 0.2″, so the search converts
// the position to the equinox of the date once.
func NextAbove(p Position, site Site, from time.Time, horizon func(azimuth float64) float64) (time.Time, bool) {
	jnow := ToDate(p, from)
	sidereal := SiderealTime(from)
	for minute := 1; minute <= 24*60; minute++ {
		at := horizontalAt(jnow, site, sidereal+siderealRate*float64(minute)/(24*60))
		if at.Altitude > horizon(at.Azimuth) {
			return from.Add(time.Duration(minute) * time.Minute), true
		}
	}
	return time.Time{}, false
}

// normalize answers an angle in degrees from 0 up to 360.
func normalize(angle float64) float64 {
	angle = math.Mod(angle, 360)
	if angle < 0 {
		angle += 360
	}
	return angle
}
