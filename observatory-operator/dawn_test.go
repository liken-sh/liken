package main

import (
	"math"
	"testing"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// spencerAltitude is a second formulation of the sun's altitude, from
// the Fourier series of J. W. Spencer (1971) for the declination and
// the equation of time, as NOAA's solar calculator uses them. It
// shares no formula with sunAltitude, so a wrong sign or a wrong
// longitude in either one shows as a difference of degrees. Its
// declination is a fit over many years, and in 2026 it is up to half a
// degree off the almanac's equinoxes, which the next test holds
// sunAltitude to.
func spencerAltitude(t time.Time, at observatory.Location) float64 {
	t = t.UTC()
	hours := float64(t.Hour()) + float64(t.Minute())/60 + float64(t.Second())/3600
	gamma := 2 * math.Pi / 365 * (float64(t.YearDay()-1) + (hours-12)/24)
	equation := 229.18 * (0.000075 + 0.001868*math.Cos(gamma) - 0.032077*math.Sin(gamma) -
		0.014615*math.Cos(2*gamma) - 0.040849*math.Sin(2*gamma))
	declination := 0.006918 - 0.399912*math.Cos(gamma) + 0.070257*math.Sin(gamma) -
		0.006758*math.Cos(2*gamma) + 0.000907*math.Sin(2*gamma) -
		0.002697*math.Cos(3*gamma) + 0.00148*math.Sin(3*gamma)
	solarMinutes := hours*60 + equation + 4*at.Longitude
	hourAngle := radians(solarMinutes/4 - 180)
	latitude := radians(at.Latitude)
	return degrees(math.Asin(math.Sin(latitude)*math.Sin(declination) + math.Cos(latitude)*math.Cos(declination)*math.Cos(hourAngle)))
}

var (
	greenwich   = observatory.Location{Latitude: 51.4769, Longitude: -0.0005}
	equator     = observatory.Location{Latitude: 0, Longitude: 0}
	cerroTololo = observatory.Location{Latitude: -30.169, Longitude: -70.806, Elevation: 2207}
)

// The two formulations agree to half a degree at every hour of a year,
// at three latitudes.
func TestTheSunAgreesWithASecondFormulation(t *testing.T) {
	for _, at := range []observatory.Location{greenwich, equator, cerroTololo} {
		worst := 0.0
		for when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); when.Year() == 2026; when = when.Add(time.Hour) {
			worst = math.Max(worst, math.Abs(sunAltitude(when, at)-spencerAltitude(when, at)))
		}
		if worst > 0.5 {
			t.Errorf("at %+v the formulations differ by up to %.3f degrees", at, worst)
		}
	}
}

// At the north pole the sun's altitude is its declination: zero at an
// equinox, and the obliquity of the ecliptic, 23.44 degrees, at the
// June solstice. The instants are the US Naval Observatory's for 2026.
func TestTheSunCrossesTheEquatorAtTheEquinoxes(t *testing.T) {
	pole := observatory.Location{Latitude: 90}
	cases := []struct {
		name string
		at   time.Time
		want float64
	}{
		{"the March equinox", time.Date(2026, 3, 20, 14, 46, 0, 0, time.UTC), 0},
		{"the June solstice", time.Date(2026, 6, 21, 8, 24, 0, 0, time.UTC), 23.44},
		{"the September equinox", time.Date(2026, 9, 23, 0, 5, 0, 0, time.UTC), 0},
		{"the December solstice", time.Date(2026, 12, 21, 20, 50, 0, 0, time.UTC), -23.44},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sunAltitude(c.at, pole); math.Abs(got-c.want) > 0.02 {
				t.Errorf("the sun's declination is %.4f degrees, want %.2f", got, c.want)
			}
		})
	}
}

// Each case is a fact that follows from the geometry or that the
// almanacs publish. On the equator at an equinox the sun moves 15
// degrees an hour straight up, so it reaches 18 degrees below the
// horizon 7 hours 12 minutes before noon, and noon at Greenwich's
// meridian on 20 March is 12:07 UTC by the equation of time. Sunrise
// in London is 04:43 BST on the summer solstice and 08:04 GMT on the
// winter solstice, with the sun's upper limb on a horizon that
// refraction lifts, 0.833 degrees above its center.
func TestTheSunRisesWhenTheAlmanacsSay(t *testing.T) {
	cases := []struct {
		name     string
		after    time.Time
		at       observatory.Location
		altitude float64
		want     time.Time
	}{
		{"astronomical dawn on the equator at the March equinox",
			time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC), equator, dawnAltitude,
			time.Date(2026, 3, 20, 4, 55, 20, 0, time.UTC)},
		{"sunrise in London at the June solstice",
			time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC), greenwich, -0.833,
			time.Date(2026, 6, 21, 3, 43, 0, 0, time.UTC)},
		{"sunrise in London at the December solstice",
			time.Date(2026, 12, 21, 0, 0, 0, 0, time.UTC), greenwich, -0.833,
			time.Date(2026, 12, 21, 8, 4, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := riseThrough(c.after, c.at, c.altitude)
			if off := got.Sub(c.want); off < -2*time.Minute || off > 2*time.Minute {
				t.Errorf("riseThrough = %s, want %s within two minutes", got.Format(time.RFC3339), c.want.Format(time.RFC3339))
			}
		})
	}
}

// Dawn comes after the time it is asked from, and the next one comes a
// day later.
func TestDawnIsTheNextOne(t *testing.T) {
	first := dawnAfter(time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC), cerroTololo)
	second := dawnAfter(first.Add(time.Second), cerroTololo)
	if !first.After(time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)) {
		t.Errorf("dawn %s is not after the time asked", first)
	}
	if gap := second.Sub(first); gap < 23*time.Hour || gap > 25*time.Hour {
		t.Errorf("dawns %s and %s are %s apart, want about a day", first, second, gap)
	}
	if got := sunAltitude(first, cerroTololo); math.Abs(got-dawnAltitude) > 0.01 {
		t.Errorf("the sun is at %.3f degrees at dawn", got)
	}
}

// A night with no astronomical dawn ends at the sun's lowest point in
// a white night, and at its highest in a polar night. At 60 degrees
// north on the June solstice the sun sinks only to about 6.6 degrees
// below the horizon, at local midnight; at 89 degrees north on the
// December solstice it rises only to about 22.4 degrees below, at
// local noon.
func TestANightWithNoDawnEnds(t *testing.T) {
	cases := []struct {
		name  string
		after time.Time
		at    observatory.Location
		want  time.Time
	}{
		{"a white night", time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC),
			observatory.Location{Latitude: 60, Longitude: 0}, time.Date(2026, 6, 22, 0, 2, 0, 0, time.UTC)},
		{"a polar night", time.Date(2026, 12, 21, 0, 0, 0, 0, time.UTC),
			observatory.Location{Latitude: 89, Longitude: 0}, time.Date(2026, 12, 21, 11, 58, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dawnAfter(c.after, c.at)
			if off := got.Sub(c.want); off < -10*time.Minute || off > 10*time.Minute {
				t.Errorf("dawnAfter = %s, want %s within ten minutes", got.Format(time.RFC3339), c.want.Format(time.RFC3339))
			}
		})
	}
}
