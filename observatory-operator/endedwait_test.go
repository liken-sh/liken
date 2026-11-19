package main

// While a pod that the kubelet ended waits for its replacement, the
// rest of the operator does not wait with it: a reservation ends at
// once, the observatory's lock stays free, and a removed device does
// not get its pod back.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// rejectUntilTheWaitIsLong makes the kubelet reject each pod of the
// name, ends the pod that runs, and waits until the operator has
// replaced it five times. The pod of count 5 then waits 5 minutes
// before its replacement.
func (w *world) rejectUntilTheWaitIsLong(name string) {
	w.t.Helper()
	w.api.rejectPods(name)
	w.api.endPod(name, "Failed")
	w.until(10*time.Minute, "pod "+name+" was not replaced five times", func() bool {
		return len(podCreates(w.api, name)) >= 6
	})
	time.Sleep(10 * time.Second)
	synctest.Wait()
}

// A pod that the kubelet rejected young waits 10 seconds from its
// creation before its replacement, because it has a count of zero.
func TestAPodThatTheKubeletEndsYoungIsReplacedTenSecondsAfterItsCreation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.holdPending(mainCamera.name)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.until(10*time.Minute, "the camera's pod was not created", func() bool {
			_, phase := podState(w.api, mainCamera.name)
			return phase == "Pending"
		})

		w.api.endPod(mainCamera.name, "Failed")
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)

		creates := podCreates(w.api, mainCamera.name)
		if len(creates) != 2 || creates[1].Sub(creates[0]) != 10*time.Second {
			t.Errorf("the camera's pods were created at %v, want two, 10 seconds apart", creates)
		}
	})
}

// A reservation that is deleted while one of its pods waits for its
// replacement is released at once: its runner does not wait for the
// pod's deadline before the release parks the mount and closes the
// dome. The case of the activation deletes the reservation while a
// step waits for the pod.
func TestAReservationEndsWithoutWaitingForAPodsReplacement(t *testing.T) {
	cases := []struct {
		name  string
		start func(w *world)
	}{
		{"while Ready", func(w *world) {
			w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
			w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		}},
		{"during activation", func(w *world) {
			w.api.holdPending(mainCamera.name)
			w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
			w.until(10*time.Minute, "the camera's pod was not created", func() bool {
				_, phase := podState(w.api, mainCamera.name)
				return phase == "Pending"
			})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.start(w)
				w.rejectUntilTheWaitIsLong(mainCamera.name)

				w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
				w.until(time.Minute, "the Reservation east-tonight was not released", func() bool {
					_, held := w.reservation("east-tonight")
					return !held
				})
			})
		})
	}
}

// Two telescopes share the observatory's server. While the server's pod
// waits for its replacement, west-tonight ends: its release does not
// wait for the observatory's lock, and no device of the observatory
// records a fault from a wait for its locks.
func TestTheObservatoryIsNotHeldWhileItsPodWaitsForItsReplacement(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.readyTogether("east-tonight", "west-tonight")
		w.rejectUntilTheWaitIsLong("lab-observatory")

		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "west-tonight")
		w.until(time.Minute, "the Reservation west-tonight was not released", func() bool {
			_, held := w.reservation("west-tonight")
			return !held
		})

		time.Sleep(3 * time.Minute)
		synctest.Wait()
		for _, d := range []struct {
			kind observatory.Kind
			name string
		}{{observatory.DomeKind, "lab"}, {observatory.WeatherStationKind, "lab"}} {
			faults := slices.DeleteFunc(readyMessages(w.api, kindCollection(d.kind), d.name), func(m string) bool { return !strings.HasPrefix(m, "Failed:") })
			if len(faults) > 0 {
				t.Errorf("%s %s recorded %q", d.kind.Name, d.name, faults)
			}
		}
	})
}

// A device that a person deletes while an activation step waits for its
// pod does not get its pod back after the kubelet ends the pod.
func TestADeletedDeviceDoesNotGetItsEndedPodBack(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.holdPending(mainCamera.name)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.until(10*time.Minute, "the camera's pod was not created", func() bool {
			_, phase := podState(w.api, mainCamera.name)
			return phase == "Pending"
		})

		w.api.deleteNamed(kindCollection(observatory.CameraKind), "east-main")
		w.api.endPod(mainCamera.name, "Failed")
		time.Sleep(5 * time.Minute)
		synctest.Wait()

		if creates := podCreates(w.api, mainCamera.name); len(creates) != 1 {
			t.Errorf("the camera's pod was created at %v, want once", creates)
		}
	})
}
