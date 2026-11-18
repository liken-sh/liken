package main

// The bubble's clock starts at 2000-01-01 00:00 UTC. At that time,
// from lab at 51.5° north and 75° west, M42 is rising in the
// east-southeast at about 14° altitude.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const (
	telescopesPath = "/apis/observatory.liken.sh/v1alpha1/telescopes"
	horizonsPath   = "/apis/observatory.liken.sh/v1alpha1/horizons"
)

// lab is the site of newWorld's Observatory.
var lab = sky.Site{Latitude: 51.5, Longitude: -75}

// at returns a target at a right ascension and declination in degrees,
// with spec.object set to the upper-case name.
func at(name string, ra, dec float64, sequences ...astrophotography.Sequence) *astrophotography.Target {
	t := target(name, sequences...)
	t.Spec.Object = strings.ToUpper(name)
	t.Spec.Coordinates = astrophotography.Coordinates{RA: sky.FormatRA(ra), Dec: sky.FormatDec(dec)}
	return t
}

// onTheEquatorAt returns a target on the celestial equator at an hour
// angle in degrees, at the bubble's start time. A star on the equator
// rises at an hour angle of -90° and sets at 90°.
func onTheEquatorAt(name string, hourAngle float64, sequences ...astrophotography.Sequence) *astrophotography.Target {
	ra := sky.SiderealTime(time.Now()) + lab.Longitude - hourAngle
	return at(name, ra, 0, sequences...)
}

// setHorizon creates the telescope east with spec.horizon set to name,
// and a Horizon of that name with the profile. An empty profile creates
// no Horizon.
func (w *runnerWorld) setHorizon(name, profile string) {
	w.put(telescopesPath, &observatory.Telescope{Metadata: meta("east"),
		Spec: observatory.TelescopeSpec{Observatory: "lab", Horizon: name}})
	if profile != "" {
		w.put(horizonsPath, &observatory.Horizon{Metadata: meta(name), Spec: observatory.HorizonSpec{Profile: profile}})
	}
}

func (w *runnerWorld) slewed() bool {
	return slices.ContainsFunc(w.indi.Changes(), func(c string) bool {
		return strings.HasPrefix(c, mountINDI+".EQUATORIAL_EOD_COORD")
	})
}

// A target below the horizon is not claimed, the mount does not move,
// and the runner writes the reason to the Imaging condition once.
func TestATargetBelowTheHorizonIsNotClaimed(t *testing.T) {
	cases := []struct {
		name             string
		horizon, profile string
		target           *astrophotography.Target
		messages         []string
	}{
		{"a target that never rises, with no Horizon", "", "",
			at("southern", 0, -60, lights(1)),
			[]string{"below the horizon of telescope east, which is at 0.0° there, and does not rise above it within a day", "Telescope east has no spec.horizon"}},
		{"M42 behind the trees", "yard", "0 30\n180 30\n360 30",
			target("m42", lights(1)),
			[]string{"M42 is at 14.", "below the Horizon yard of telescope east, which is at 30.0° there, and rises above it at 2000-01-01T"}},
		// The CRD rejects such a profile, so only a Horizon created before
		// that rule can have one.
		{"a Horizon that does not read", "yard", "trees",
			at("southern", 0, -60, lights(1)),
			[]string{"The Horizon yard that telescope east refers to does not parse", "0° at every azimuth"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				w.setHorizon(c.horizon, c.profile)
				w.put(targetsPath, c.target)
				w.put(reservationsPath, reservationIn(observatory.ReservationReady))
				w.start(t)
				time.Sleep(time.Minute)
				synctest.Wait()
				name := c.target.Metadata.Name
				if got := w.imaging(t, name); got != "False BelowHorizon" || w.targetNow(t, name).Status.Reservation != "" || w.slewed() {
					t.Errorf("Imaging is %q, the reservation is %q, and slewed is %v, want False BelowHorizon, no reservation, and no slew",
						got, w.targetNow(t, name).Status.Reservation, w.slewed())
				}
				for _, want := range c.messages {
					if message := w.imagingMessage(t, name); !strings.Contains(message, want) {
						t.Errorf("the Imaging message is %q, want it to contain %q", message, want)
					}
				}
				if got := w.eventReasons("Target", name); !slices.Equal(got, []string{astrophotography.ReasonBelowHorizon}) {
					t.Errorf("the Event reasons are %q, want one BelowHorizon", got)
				}
			})
		})
	}
}

// When the API server refuses the write of BelowHorizon, the runner
// writes it again after its next wait.
func TestARefusedBelowHorizonIsWrittenAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, at("southern", 0, -60, lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		refused := 0
		w.api.beforeStatus = func(_, _ string, body map[string]any) bool {
			if refused == 2 || !strings.Contains(imagingIn(body), "below the horizon") {
				return false
			}
			refused++
			return true
		}
		w.start(t)
		time.Sleep(2*retryWait + time.Minute)
		synctest.Wait()
		if got := w.imaging(t, "southern"); refused != 2 || got != "False BelowHorizon" {
			t.Errorf("Imaging is %q after %d refused writes, want False BelowHorizon after 2", got, refused)
		}
	})
}

// A Telescope whose spec.horizon names a missing Horizon has a flat
// horizon at 0°, so M42 at 14° is imaged.
func TestAMissingHorizonLimitsAtZero(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.setHorizon("gone", "")
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
	})
}

// The idle runner wakes at the computed rise time and claims the
// target, with no other change to wake it.
func TestATargetIsClaimedWhenItRises(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, onTheEquatorAt("rising", -120, lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		start := time.Now()
		w.start(t)
		time.Sleep(90 * time.Minute)
		synctest.Wait()
		before := w.imaging(t, "rising")
		waitUntil(t, func() bool { return w.imaging(t, "rising") == "False SequencesDone" })
		// 30° of hour angle is 1h59m40s of the clock.
		if before != "False BelowHorizon" || time.Since(start) < 2*time.Hour {
			t.Errorf("Imaging is %q 90 minutes in and SequencesDone after %s, want False BelowHorizon and SequencesDone after the rise", before, time.Since(start))
		}
	})
}

// A target that sets during its sequences is released before the next
// frame, and the frames exposed before it set still count.
func TestATargetThatSetsIsReleasedBeforeTheNextFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		// 0.75° of hour angle before it sets is 3 minutes.
		w.put(targetsPath, onTheEquatorAt("setting", 89.25, lights(100)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "setting") == "False BelowHorizon" })
		status := w.targetNow(t, "setting").Status
		if pending := status.Sequences[0].Pending; pending < 1 || pending > 20 || status.Reservation != "" {
			t.Errorf("%d frames are pending and the reservation is %q, want the frames of 3 minutes and no reservation", pending, status.Reservation)
		}
		want := []string{astrophotography.ReasonSlewing, astrophotography.ReasonExposing, astrophotography.ReasonBelowHorizon}
		if got := w.eventReasons("Target", "setting"); !slices.Equal(got, want) {
			t.Errorf("the Event reasons are %q, want %q", got, want)
		}
	})
}
