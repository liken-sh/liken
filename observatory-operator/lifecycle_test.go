package main

// A reservation's whole life against the example's simulators: the
// steps in order, each pod and Service, the INDI changes the devices
// received, and the end of the reservation.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func stepStates(r observatory.Reservation) []string {
	var out []string
	for _, s := range r.Status.Steps {
		out = append(out, string(s.Name)+"="+string(s.State))
	}
	return out
}

func TestAReservationActivatesTheTelescopeAndReleasesIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		want := []string{"Wait=Done", "StartSite=Done", "PowerOn=Done", "StartDevices=Done", "Connect=Done", "Configure=Done", "Prepare=Done"}
		if got := stepStates(r); !slices.Equal(got, want) {
			t.Errorf("steps = %v, want %v", got, want)
		}
		for _, s := range r.Status.Steps {
			t.Logf("%s: %s", s.Name, s.Message)
		}
		if r.Status.Endpoint == nil || r.Status.Endpoint.Host != "telescope-east.observatory.svc" || r.Status.Endpoint.Port != 7624 {
			t.Errorf("endpoint = %+v", r.Status.Endpoint)
		}
		if !slices.Contains(r.Metadata.Finalizers, observatory.ReservationFinalizer) {
			t.Errorf("finalizers = %v", r.Metadata.Finalizers)
		}
		pods := w.api.names(podsCollection)
		slices.Sort(pods)
		wantPods := []string{
			"camera-east-guide", "camera-east-main", "dome-dome", "dustcap-east-cap", "filterwheel-east-wheel",
			"flatpanel-east-flat", "focuser-east-focuser", "gps-east-gps", "mount-east-mount", "observatory-lab",
			"polaraligner-east-pac", "receiver-east-radio", "rotator-east-rotator", "skyqualitymeter-sky",
			"switch-east-power", "telescope-east", "weatherstation-weather",
		}
		if !slices.Equal(pods, wantPods) {
			t.Errorf("pods = %v\nwant %v", pods, wantPods)
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
			t.Errorf("pods after the release: %v", pods)
		}
		if services := w.api.names(servicesCollection); len(services) != 0 {
			t.Errorf("Services after the release: %v", services)
		}
		reasons := strings.Join(w.api.eventReasons(), " ")
		if !strings.Contains(reasons, "Ready") || !strings.Contains(reasons, "Released") {
			t.Errorf("events = %s", reasons)
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

func conditionOf(conditions []observatory.Condition, kind string) observatory.Condition {
	for _, c := range conditions {
		if c.Type == kind {
			return c
		}
	}
	return observatory.Condition{}
}

// At spec.end the reservation deactivates, and it stays, Released, with
// every step in its record.
func TestAReservationDeactivatesAtItsEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": end})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(time.Hour)
		r := w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		want := []string{
			"Wait=Done", "StartSite=Done", "PowerOn=Done", "StartDevices=Done", "Connect=Done", "Configure=Done", "Prepare=Done",
			"Abort=Skipped", "Secure=Done", "Disconnect=Done", "StopDevices=Done", "PowerOff=Done", "StopSite=Done",
		}
		if got := stepStates(r); !slices.Equal(got, want) {
			t.Errorf("steps = %v, want %v", got, want)
		}
		for _, s := range r.Status.Steps {
			t.Logf("%s: %s", s.Name, s.Message)
		}
		if c := conditionOf(r.Status.Conditions, observatory.ConditionSafeToPowerOff); c.Status != observatory.ConditionTrue {
			t.Errorf("SafeToPowerOff = %+v", c)
		}
		if c := conditionOf(r.Status.Conditions, observatory.ConditionReady); c.Status != observatory.ConditionFalse {
			t.Errorf("Ready = %+v", c)
		}
		if slices.Contains(r.Metadata.Finalizers, observatory.ReservationFinalizer) {
			t.Errorf("the finalizer stays: %v", r.Metadata.Finalizers)
		}
		// Secure closes the cap, parks the mount, and warms the camera
		// before Disconnect, and the outputs switch off after the pods
		// stop. The flat panel's light was off, and the transcript's
		// cooler reports itself off, so neither receives a change.
		order := []string{
			"telescope-east Dust Cover Simulator.CAP_PARK PARK=On",
			"telescope-east Telescope Simulator.TELESCOPE_PARK PARK=On",
			"telescope-east CCD Simulator.CCD_TEMPERATURE CCD_TEMPERATURE_VALUE=5",
			"telescope-east CCD Simulator.CONNECTION CONNECT=Off",
			"telescope-east Telescope Simulator.CONNECTION CONNECT=Off",
			"telescope-east Simulator IO.DIGITAL_OUTPUT_1 OFF=On",
			"telescope-east Simulator IO.CONNECTION CONNECT=Off",
			"observatory-lab Dome Simulator.DOME_PARK PARK=On",
		}
		changes := w.indi.changes()
		at := 0
		for _, want := range order {
			found := slices.IndexFunc(changes[at:], func(c string) bool { return strings.HasPrefix(c, want) })
			if found < 0 {
				t.Errorf("no change %q after change %d of %v", want, at, changes)
				break
			}
			at += found + 1
		}
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("pods after the release: %v", pods)
		}
	})
}
