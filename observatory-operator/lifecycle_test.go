package main

// A whole reservation against the example's simulators: the steps in
// order, the pods and Services that the operator creates, the INDI
// changes that the devices receive, and the end of the reservation.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// sentBefore reports whether the INDI changes include one that begins
// with first and, after it, one that begins with then.
func (w *indiWorld) sentBefore(first, then string) bool {
	changes := w.changes()
	a := slices.IndexFunc(changes, func(c string) bool { return strings.HasPrefix(c, first) })
	b := slices.IndexFunc(changes, func(c string) bool { return strings.HasPrefix(c, then) })
	return a >= 0 && b >= 0 && a < b
}

func stepStates(r observatory.Reservation) []string {
	var out []string
	for _, s := range r.Status.Steps {
		out = append(out, string(s.Name)+"="+string(s.State))
	}
	return out
}

func TestAReservationActivatesTheTelescopeAndReleasesIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		want := []string{"Wait=Done", "StartSite=Done", "Requires=Skipped", "PowerOn=Done", "StartDevices=Done", "Connect=Done", "Configure=Done", "Activation=Done", "StartGuider=Done"}
		if got := stepStates(r); !slices.Equal(got, want) {
			t.Errorf("the steps are %v, want %v", got, want)
		}
		for _, s := range r.Status.Steps {
			t.Logf("%s: %s", s.Name, s.Summary)
		}
		if r.Status.Endpoint == nil || r.Status.Endpoint.Host != "east-telescope.observatory.svc" || r.Status.Endpoint.Port != 7624 {
			t.Errorf("the endpoint is %+v, want east-telescope.observatory.svc port 7624", r.Status.Endpoint)
		}
		if !slices.Contains(r.Metadata.Finalizers, observatory.Finalizer) {
			t.Errorf("the finalizers are %v, want %s", r.Metadata.Finalizers, observatory.Finalizer)
		}
		pods := w.api.names(podsCollection)
		slices.Sort(pods)
		wantPods := []string{
			"east-dustcap", "east-flatpanel", "east-focuser", "east-gps", "east-guide-camera",
			"east-guider", "east-main-camera", "east-mount", "east-polaraligner", "east-receiver", "east-rotator", "east-switch",
			"east-telescope", "lab-dome", "lab-observatory", "lab-weatherstation",
		}
		if !slices.Equal(pods, wantPods) {
			t.Errorf("the pods are %v, want %v", pods, wantPods)
		}
		for _, change := range w.indi.changes() {
			t.Log(change)
		}

		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the Reservation is still there", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("the pods after the release are %v, want none", pods)
		}
		if services := w.api.names(servicesCollection); len(services) != 0 {
			t.Errorf("the Services after the release are %v, want none", services)
		}
		reasons := strings.Join(w.api.eventReasons(), " ")
		if !strings.Contains(reasons, "Ready") || !strings.Contains(reasons, "Released") {
			t.Errorf("the Event reasons are %q, want Ready and Released", reasons)
		}
	})
}

// The west mount starts unparked, and the dome simulator starts with
// its shutter closed, so the dome's activation opens it. The west
// camera has a cooler and no cool action in spec.activation, and the
// Activation step reports that.
func TestTheActivationStepNotesACameraThatNothingCools(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
		r := w.phase("west-tonight", observatory.ReservationReady, 10*time.Minute)
		want := "Ran the activation of Observatory lab, Dome lab, Mount west; Camera west-main has a cooler and no cool action in spec.activation\n" +
			exampleJobDone + "\n" +
			"Dome lab state: Unparked Done: Found Dome lab unparked and opened its shutter\n" +
			"Mount west state: Unparked Done: Found Mount west unparked"
		if got := stepText(stepOf(r, observatory.StepActivation)); got != want {
			t.Errorf("the Activation step message is\n%s\nwant\n%s", got, want)
		}
	})
}

func stepOf(r observatory.Reservation, name observatory.StepName) observatory.Step {
	for _, s := range r.Status.Steps {
		if s.Name == name {
			return s
		}
	}
	return observatory.Step{}
}

// stepText returns a step's summary and then each of its actions, one
// per line, such as "Camera east-main cool: -10 °C within 0.5 °C Done:
// Cooled Camera east-main to -10 °C".
func stepText(s observatory.Step) string {
	lines := []string{s.Summary}
	for _, a := range s.Actions {
		lines = append(lines, fmt.Sprintf("%s %s %s %s: %s", a.Kind, a.Name, a.Action, a.State, a.Summary))
	}
	return strings.Join(lines, "\n")
}

// At spec.end the reservation deactivates and stays in the Released
// phase, with every step in its status.
func TestAReservationDeactivatesAtItsEnd(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": end})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(time.Hour)
		r := w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		want := []string{
			"Wait=Done", "StartSite=Done", "Requires=Skipped", "PowerOn=Done", "StartDevices=Done", "Connect=Done", "Configure=Done", "Activation=Done", "StartGuider=Done",
			"StopWork=Skipped", "Deactivation=Done", "StopGuider=Done", "Disconnect=Done", "StopDevices=Done", "PowerOff=Done", "StopSite=Done",
		}
		if got := stepStates(r); !slices.Equal(got, want) {
			t.Errorf("the steps are %v, want %v", got, want)
		}
		for _, s := range r.Status.Steps {
			t.Logf("%s: %s", s.Name, s.Summary)
		}
		if c := conditionOf(r.Status.Conditions, observatory.ConditionSafeToPowerOff); c.Status != observatory.ConditionTrue {
			t.Errorf("the SafeToPowerOff condition is %+v, want True", c)
		}
		if c := conditionOf(r.Status.Conditions, observatory.ConditionReady); c.Status != observatory.ConditionFalse {
			t.Errorf("the Ready condition is %+v, want False", c)
		}
		if slices.Contains(r.Metadata.Finalizers, observatory.Finalizer) {
			t.Errorf("the finalizers are %v, want no %s", r.Metadata.Finalizers, observatory.Finalizer)
		}
		// The Deactivation step closes the dust cap and warms the
		// camera, then parks the mount, then parks the dome. The
		// devices disconnect after that, and the switch's outputs turn
		// off after the mount disconnects. The flat panel's light was
		// already off and the transcript's cooler already reports off,
		// so neither receives a change.
		order := [][2]string{
			{"east-telescope Dust Cover Simulator.CAP_PARK PARK=On", "east-telescope Telescope Simulator.TELESCOPE_PARK PARK=On"},
			{"east-telescope CCD Simulator.CCD_TEMPERATURE CCD_TEMPERATURE_VALUE=5", "east-telescope Telescope Simulator.TELESCOPE_PARK PARK=On"},
			{"east-telescope Telescope Simulator.TELESCOPE_PARK PARK=On", "lab-observatory Dome Simulator.DOME_PARK PARK=On"},
			{"lab-observatory Dome Simulator.DOME_PARK PARK=On", "east-telescope CCD Simulator.CONNECTION CONNECT=Off"},
			{"east-telescope CCD Simulator.CONNECTION CONNECT=Off", "east-telescope Telescope Simulator.CONNECTION CONNECT=Off"},
			{"east-telescope Telescope Simulator.CONNECTION CONNECT=Off", "east-telescope Simulator IO.DIGITAL_OUTPUT_1 OFF=On"},
			{"east-telescope Simulator IO.DIGITAL_OUTPUT_1 OFF=On", "east-telescope Simulator IO.CONNECTION CONNECT=Off"},
		}
		for _, pair := range order {
			if !w.indi.sentBefore(pair[0], pair[1]) {
				t.Errorf("%q was not sent before %q: %v", pair[0], pair[1], w.indi.changes())
			}
		}
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("the pods after the release are %v, want none", pods)
		}
	})
}
