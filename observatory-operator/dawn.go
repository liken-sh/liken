package main

// Astronomical dawn at an Observatory's spec.location. A SetUp with no
// --until expires at dawn, and a reservation that is still waiting for
// its requirements at dawn gives up.
//
// Astronomical dawn is when the sun rises through 18 degrees below the
// horizon. After that the sky is too bright to image faint targets.

import (
	"math"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// dawnAltitude is the sun's altitude at astronomical dawn, in degrees.
const dawnAltitude = -18.0

// The sun's altitude changes by at most about 0.25 degrees a minute, so
// a scan in five-minute steps finds every crossing of an altitude
// unless the sun crosses it and comes back within one step. crossing
// then narrows the step that contains the crossing to one second.
const (
	sunScanStep = 5 * time.Minute
	sunScanSpan = 48 * time.Hour
)

// dawnAfter returns the first astronomical dawn after the given time.
// A place with no astronomical dawn still gets an end to its night. In
// a high-latitude summer the sun stays above -18 degrees all night, and
// the night ends at the sun's lowest point, when the sky stops getting
// darker. In a polar winter the sun stays below -18 degrees, and the
// night ends at the sun's highest point, when it comes closest to dawn.
func dawnAfter(after time.Time, at observatory.Location) time.Time {
	return riseThrough(after, at, dawnAltitude)
}

// riseThrough returns the first time after the given time that the sun
// rises through altitude, to the second. When that does not happen
// within sunScanSpan, it returns the time of the sun's lowest point in
// the next 24 hours if the sun stays above altitude, or of its highest
// point if not.
func riseThrough(after time.Time, at observatory.Location, altitude float64) time.Time {
	before, low := after, sunAltitude(after, at)
	for t := after.Add(sunScanStep); !t.After(after.Add(sunScanSpan)); t = t.Add(sunScanStep) {
		high := sunAltitude(t, at)
		if low < altitude && high >= altitude {
			return crossing(before, t, at, altitude)
		}
		before, low = t, high
	}
	lowest, highest := after, after
	least, most := math.Inf(1), math.Inf(-1)
	for t := after; !t.After(after.Add(24 * time.Hour)); t = t.Add(sunScanStep) {
		a := sunAltitude(t, at)
		if a < least {
			least, lowest = a, t
		}
		if a > most {
			most, highest = a, t
		}
	}
	if least >= altitude {
		return lowest.UTC().Truncate(time.Second)
	}
	return highest.UTC().Truncate(time.Second)
}

// crossing bisects the interval from below, when the sun is under
// altitude, to above, when it is at or over altitude, until the
// interval is one second long. It returns the end at or over altitude.
func crossing(below, above time.Time, at observatory.Location, altitude float64) time.Time {
	for above.Sub(below) > time.Second {
		middle := below.Add(above.Sub(below) / 2)
		if sunAltitude(middle, at) < altitude {
			below = middle
		} else {
			above = middle
		}
	}
	return above.UTC().Truncate(time.Second)
}

// sunAltitude returns the altitude of the sun's center above the
// horizon in degrees, without atmospheric refraction. It uses the
// low-precision formulas that the US Naval Observatory publishes as
// "Approximate Solar Coordinates" and "Approximate Sidereal Time". They
// are accurate to about 0.01 degrees from 1950 to 2050.
func sunAltitude(t time.Time, at observatory.Location) float64 {
	// n is the number of days since the J2000.0 epoch, 2000-01-01
	// 12:00 TT. The difference between TT and UTC moves the sun by about
	// a thousandth of a degree, so the code uses UTC.
	n := float64(t.UnixNano())/float64(24*time.Hour) + 2440587.5 - 2451545.0
	meanAnomaly := radians(357.529 + 0.98560028*n)
	meanLongitude := 280.459 + 0.98564736*n
	longitude := radians(meanLongitude + 1.915*math.Sin(meanAnomaly) + 0.020*math.Sin(2*meanAnomaly))
	obliquity := radians(23.439 - 0.00000036*n)
	rightAscension := math.Atan2(math.Cos(obliquity)*math.Sin(longitude), math.Cos(longitude))
	declination := math.Asin(math.Sin(obliquity) * math.Sin(longitude))
	// Greenwich mean sidereal time in hours, then the sun's hour angle
	// at the site.
	sidereal := 18.697374558 + 24.06570982441908*n
	hourAngle := radians(sidereal*15+at.Longitude) - rightAscension
	latitude := radians(at.Latitude)
	sine := math.Sin(latitude)*math.Sin(declination) + math.Cos(latitude)*math.Cos(declination)*math.Cos(hourAngle)
	return degrees(math.Asin(math.Max(-1, math.Min(1, sine))))
}

func radians(d float64) float64 { return d * math.Pi / 180 }

func degrees(r float64) float64 { return r * 180 / math.Pi }
