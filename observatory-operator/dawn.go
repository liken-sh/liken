package main

// Dawn at an observatory. A person's SetUp lasts until dawn, and a
// reservation that still waits for its requirements at dawn ends, so
// the operator computes when the sun rises at the Observatory's
// spec.location.
//
// Dawn here is astronomical dawn: the sun rises through 18 degrees
// below the horizon. That is the moment the sky stops being dark
// enough to image the faintest targets, so it ends an imaging night,
// and it comes about an hour and a half before sunrise at mid
// latitudes. Civil or nautical dawn would keep a confirmation, and a
// waiting reservation, until the sky is already too bright to use. A
// person who works past it writes an --until of their own.

import (
	"math"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// dawnAltitude is the sun's altitude at astronomical dawn, in degrees.
const dawnAltitude = -18.0

// The sun moves at most about a quarter of a degree a minute in
// altitude, so a scan in steps of five minutes finds every crossing of
// an altitude that the sun stays beyond for longer than that, and
// bisection then finds the second.
const (
	sunScanStep = 5 * time.Minute
	sunScanSpan = 48 * time.Hour
)

// dawnAfter answers the first astronomical dawn after a time at a
// place. Where the sun never rises through 18 degrees below the horizon
// in the next two days, the night has no astronomical dawn: in the
// white nights of a high-latitude summer the sun stays above that
// altitude, and dawn is the time of its lowest point, after which the
// sky only brightens; in a polar night it stays below, and dawn is the
// time of its highest point.
func dawnAfter(after time.Time, at observatory.Location) time.Time {
	return riseThrough(after, at, dawnAltitude)
}

// riseThrough answers the first time after a time that the sun rises
// through an altitude, to the second, or the end of the night when it
// does not (dawnAfter).
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

// crossing bisects the time between below, when the sun is below an
// altitude, and above, when it is at or above it, to one second.
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

// sunAltitude answers the altitude of the sun's center above the
// horizon in degrees, with no refraction, from the low-precision
// formulas of the Astronomical Almanac that the US Naval Observatory
// publishes as "Approximate Solar Coordinates" and "Approximate
// Sidereal Time". They hold the sun's position to about 0.01 degrees
// from 1950 to 2050, which puts dawn within a few seconds.
func sunAltitude(t time.Time, at observatory.Location) float64 {
	// n is the days since the J2000.0 epoch, 2000-01-01 12:00 TT. The
	// difference between TT and UTC moves the sun by a thousandth of a
	// degree, so UTC stands in for it.
	n := float64(t.UnixNano())/float64(24*time.Hour) + 2440587.5 - 2451545.0
	meanAnomaly := radians(357.529 + 0.98560028*n)
	meanLongitude := 280.459 + 0.98564736*n
	longitude := radians(meanLongitude + 1.915*math.Sin(meanAnomaly) + 0.020*math.Sin(2*meanAnomaly))
	obliquity := radians(23.439 - 0.00000036*n)
	rightAscension := math.Atan2(math.Cos(obliquity)*math.Sin(longitude), math.Cos(longitude))
	declination := math.Asin(math.Sin(obliquity) * math.Sin(longitude))
	// The Greenwich mean sidereal time in hours, then the local hour
	// angle of the sun.
	sidereal := 18.697374558 + 24.06570982441908*n
	hourAngle := radians(sidereal*15+at.Longitude) - rightAscension
	latitude := radians(at.Latitude)
	sine := math.Sin(latitude)*math.Sin(declination) + math.Cos(latitude)*math.Cos(declination)*math.Cos(hourAngle)
	return degrees(math.Asin(math.Max(-1, math.Min(1, sine))))
}

func radians(d float64) float64 { return d * math.Pi / 180 }

func degrees(r float64) float64 { return r * 180 / math.Pi }
