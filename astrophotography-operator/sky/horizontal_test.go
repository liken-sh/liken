package sky

import (
	"testing"
	"time"
)

func TestSiderealTime(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		want float64
	}{
		// Example 12.a: 1987 April 10 at 0h UT. The book gives the mean
		// time 13h10m46.3668s and the apparent time 13h10m46.1351s.
		{"example 12.a", time.Date(1987, 4, 10, 0, 0, 0, 0, time.UTC), 15 * dms(13, 10, 46.1351)},
		// Example 12.b: 1987 April 10 at 19h21m UT, mean 128.7378734°.
		// The apparent time adds the nutation in right ascension of
		// example 12.a, -0.2317 seconds of time.
		{"example 12.b", time.Date(1987, 4, 10, 19, 21, 0, 0, time.UTC), 128.7378734 - 15*arcseconds(0.2317)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The abridged nutation holds Δψ to 0.5″, and the sidereal
			// time takes Δψ cos ε, so the limit is 0.5″.
			if got := SiderealTime(c.at); !near(got, c.want, arcseconds(0.5)) {
				t.Errorf("SiderealTime = %.7f°, want %.7f°", got, c.want)
			}
		})
	}
}

// Example 13.b: Venus from the United States Naval Observatory at
// Washington, 1987 April 10 at 19h21m UT. The book measures the
// azimuth from the south, and gives 68.0337°, which is 248.0337° from
// the north, and an altitude of 15.1249°.
func TestToHorizontal(t *testing.T) {
	venus := Position{RA: 15 * dms(23, 9, 16.641), Dec: -dms(6, 43, 11.61)}
	washington := Site{Latitude: dms(38, 55, 17), Longitude: -dms(77, 3, 56)}
	got := ToHorizontal(venus, washington, time.Date(1987, 4, 10, 19, 21, 0, 0, time.UTC))
	cases := []struct {
		name      string
		got, want float64
	}{
		{"azimuth", got.Azimuth, 248.0337},
		{"altitude", got.Altitude, 15.1249},
	}
	for _, c := range cases {
		if !near(c.got, c.want, 0.0002) {
			t.Errorf("%s = %.4f°, want %.4f°", c.name, c.got, c.want)
		}
	}
}

// The pole star stays near the latitude's altitude all night, due
// north, and a star at the celestial equator crosses the meridian due
// south at 90° minus the latitude.
func TestToHorizontalNorthAndSouth(t *testing.T) {
	site := Site{Latitude: 51.5, Longitude: 0}
	at := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	south := ToHorizontal(Position{RA: SiderealTime(at), Dec: 0}, site, at)
	pole := ToHorizontal(Position{RA: 0, Dec: 90}, site, at)
	cases := []struct {
		name      string
		got, want float64
	}{
		{"the equator's altitude", south.Altitude, 38.5},
		{"the equator's azimuth", south.Azimuth, 180},
		{"the pole's altitude", pole.Altitude, 51.5},
		{"the pole's azimuth", pole.Azimuth, 0},
	}
	for _, c := range cases {
		if !near(c.got, c.want, 1e-9) {
			t.Errorf("%s = %.12f°, want %g°", c.name, c.got, c.want)
		}
	}
}

// A star on the celestial equator rises due east, 6 sidereal hours
// before it crosses the meridian. From a site on the equator at
// longitude 0, at a moment whose sidereal time is 0, the star at right
// ascension 120° of the date rises when the sidereal time reaches 30°:
// 30° / 360.9856° of a day, 1h59m40s later.
func TestNextAboveFindsTheRise(t *testing.T) {
	midnight := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	from := midnight.Add(-time.Duration(SiderealTime(midnight) / siderealRate * float64(24*time.Hour)))
	star := FromDate(Position{RA: 120, Dec: 0}, from)
	rises, ok := NextAbove(star, Site{}, from, func(float64) float64 { return 0 })
	want := from.Add(time.Hour + 59*time.Minute + 40*time.Second)
	if !ok || rises.Before(want) || rises.Sub(want) > time.Minute {
		t.Errorf("rises at %s, %v; want the first minute after %s", rises, ok, want)
	}
}

// A star that never rises at the site, and a star behind a horizon
// that hides the whole sky, answer no time.
func TestNextAboveAnswersNoneForAStarThatNeverRises(t *testing.T) {
	cases := []struct {
		name    string
		star    Position
		horizon func(float64) float64
	}{
		{"below the pole's circle", Position{RA: 0, Dec: -60}, func(float64) float64 { return 0 }},
		{"behind a wall", Position{RA: 0, Dec: 60}, func(float64) float64 { return 90 }},
	}
	for _, c := range cases {
		rises, ok := NextAbove(c.star, Site{Latitude: 51.5}, time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC), c.horizon)
		if ok {
			t.Errorf("%s: rises at %s", c.name, rises)
		}
	}
}
