package main

import (
	"testing"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestEachStepEndIsOneLogLine(t *testing.T) {
	start := time.Date(2026, 10, 5, 21, 0, 0, 0, time.UTC)
	stop := start.Add(19 * time.Second)
	cases := []struct {
		name string
		step observatory.Step
		want string
	}{
		{"a step that is done", observatory.Step{Name: observatory.StepPrepare, State: observatory.StepDone, StartTime: &start, StopTime: &stop,
			Summary: "Cooled Camera east-main to -10 °C"}, "Prepare done in 19 s: cooled Camera east-main to -10 °C"},
		{"a step with nothing to do", observatory.Step{Name: observatory.StepAbort, State: observatory.StepSkipped, StartTime: &start, StopTime: &start,
			Summary: "Found no exposure or slew to abort"}, "Abort skipped in 0 s: found no exposure or slew to abort"},
		{"a step that failed", observatory.Step{Name: observatory.StepConnect, State: observatory.StepFailed, StartTime: &start, StopTime: &stop,
			Summary: "Failed: Mount east: indi: Telescope Simulator.CONNECTION is Alert"}, "Connect failed: Mount east: indi: Telescope Simulator.CONNECTION is Alert"},
		{"a step that timed out", observatory.Step{Name: observatory.StepPrepare, State: observatory.StepFailed, StartTime: &start, StopTime: &stop,
			Summary: "Timed out after 20 min: cooling Camera east-main to -10 °C"}, "Prepare timed out after 20 min: cooling Camera east-main to -10 °C"},
		{"a summary that starts with an acronym", observatory.Step{Name: observatory.StepConfigure, State: observatory.StepDone, StartTime: &start, StopTime: &stop,
			Summary: "INDI wrote nothing"}, "Configure done in 19 s: INDI wrote nothing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stepLine(&c.step); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestEachPhaseChangeIsOneLogLine(t *testing.T) {
	cases := []struct {
		name   string
		status observatory.ReservationStatus
		want   string
	}{
		{"activating", observatory.ReservationStatus{Phase: observatory.ReservationActivating}, "Activating"},
		{"ready", observatory.ReservationStatus{Phase: observatory.ReservationReady, Conditions: []observatory.Condition{
			{Type: observatory.ConditionReady, Message: "Ready at east-telescope.observatory.svc:7624"}}}, "Ready at east-telescope.observatory.svc:7624"},
		{"failed", observatory.ReservationStatus{Phase: observatory.ReservationFailed, Conditions: []observatory.Condition{
			{Type: observatory.ConditionReady, Message: "Prepare failed: Camera east-main: indi: CCD Simulator.CCD_TEMPERATURE is Alert"}}},
			"Failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := phaseLine(&c.status); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
