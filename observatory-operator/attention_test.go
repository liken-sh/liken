package main

// The notify action and the AttentionNeeded condition it sets.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The deactivation asks a person to cover the telescope and goes on at
// once: the reservation releases, the telescope's AttentionNeeded turns
// True with the request, and its transition posts one Warning. When
// the person takes the telescope down, the condition clears.
func TestANotifyAsksAPersonAndNeverWaits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		request := "Cover the telescope and carry it in"
		w.amend(observatory.TelescopeKind, "east", func(spec map[string]any) {
			spec["deactivation"] = []any{map[string]any{"notify": request}}
		})
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop",
			"end": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(time.Hour)
		r := w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		if s := stepOf(r, observatory.StepDeactivation); !slices.ContainsFunc(s.Actions, func(a observatory.StepAction) bool {
			return a.Kind == observatory.TelescopeKind.Name && a.Action == "notify: "+request && a.State == observatory.StepDone && a.Summary == "Asked: "+request
		}) {
			t.Errorf("Deactivation = %s", stepText(s))
		}
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		if c := w.conditionOn(observatory.TelescopeKind, "east", observatory.ConditionAttentionNeeded); c.Status != observatory.ConditionTrue ||
			c.Reason != observatory.ReasonNotified || c.Message != request {
			t.Errorf("AttentionNeeded = %+v", c)
		}
		if got, want := w.eventsWith(observatory.TelescopeKind, "east", observatory.ReasonNotified), []string{"Warning Notified: " + request + " x1"}; !slices.Equal(got, want) {
			t.Errorf("Events = %v, want %v", got, want)
		}

		time.Sleep(time.Minute)
		w.setUp(observatory.TelescopeKind, "east", false, time.Time{})
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		if c := w.conditionOn(observatory.TelescopeKind, "east", observatory.ConditionAttentionNeeded); c.Status != observatory.ConditionFalse ||
			c.Reason != observatory.ReasonAcknowledged || c.Message != "Taken down by stargazer" {
			t.Errorf("AttentionNeeded after the take-down = %+v", c)
		}
	})
}

// A telescope whose only procedure is the deactivation's notify has no
// activation run, so its next activation clears the request from the
// telescope's Active condition. The deactivation that follows asks
// again.
func TestTheNextActivationClearsARequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		request := "Cover the telescope and carry it in"
		w.amend(observatory.TelescopeKind, "east", func(spec map[string]any) {
			spec["deactivation"] = []any{map[string]any{"notify": request}}
		})
		attention := func() string {
			time.Sleep(2 * statusWindow)
			synctest.Wait()
			c := w.conditionOn(observatory.TelescopeKind, "east", observatory.ConditionAttentionNeeded)
			return string(c.Status) + " " + c.Reason
		}
		night := func(name string) {
			w.reserve(name, map[string]any{"telescope": "east", "holder": "desktop",
				"end": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
			w.phase(name, observatory.ReservationReady, 10*time.Minute)
		}
		night("east-tonight")
		time.Sleep(time.Hour)
		w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		if got := attention(); got != "True Notified" {
			t.Errorf("AttentionNeeded after the first night = %s", got)
		}
		night("east-again")
		if got := attention(); got != "False Activated" {
			t.Errorf("AttentionNeeded after the next activation = %s", got)
		}
		time.Sleep(time.Hour)
		w.phase("east-again", observatory.ReservationReleased, 10*time.Minute)
		if got := attention(); got != "True Notified" {
			t.Errorf("AttentionNeeded after the second night = %s", got)
		}
	})
}

// at answers a pointer to a time, for the records below.
func at(hour int) *time.Time {
	t := time.Date(2026, 10, 12, hour, 0, 0, 0, time.UTC)
	return &t
}

// notified is the record of a deactivation run that asked a person to
// cover the telescope at an hour.
func notified(hour int) observatory.ProcedureRun {
	return observatory.ProcedureRun{Trigger: observatory.TriggerDeactivation, StartTime: at(hour), Actions: []observatory.ActionRun{
		{Action: "notify: Cover the telescope", State: observatory.StepDone, StartTime: at(hour), StopTime: at(hour)},
	}}
}

func activatedAt(hour int) observatory.ProcedureRun {
	return observatory.ProcedureRun{Trigger: observatory.TriggerActivation, StartTime: at(hour)}
}

func setUpAt(hour int, reason string) []observatory.Condition {
	return []observatory.Condition{{Type: observatory.ConditionSetUp, Status: observatory.ConditionTrue, Reason: reason, LastTransitionTime: *at(hour)}}
}

// takenDownAt is a person's take-down at an hour, with the message the
// plugin writes, or with none.
func takenDownAt(hour int, message string) []observatory.Condition {
	return []observatory.Condition{{Type: observatory.ConditionSetUp, Status: observatory.ConditionFalse, Reason: observatory.ReasonTakenDown,
		Message: message, LastTransitionTime: *at(hour)}}
}

// clearedAt is a stored AttentionNeeded that turned False at an hour.
func clearedAt(hour int) observatory.Condition {
	return observatory.Condition{Type: observatory.ConditionAttentionNeeded, Status: observatory.ConditionFalse,
		Reason: observatory.ReasonActivated, Message: "The activation began after the request", LastTransitionTime: *at(hour)}
}

func TestAttentionClearsWhenAPersonAnswersOrTheNextActivationBegins(t *testing.T) {
	cases := []struct {
		name   string
		runs   []observatory.ProcedureRun
		stored []observatory.Condition
		active *time.Time
		want   string
	}{
		{"no request", []observatory.ProcedureRun{activatedAt(1)}, nil, nil, ""},
		{"a request", []observatory.ProcedureRun{activatedAt(1), notified(5)}, nil, nil, "True Notified Cover the telescope"},
		{"a request before the take-down", []observatory.ProcedureRun{activatedAt(1), notified(5)}, takenDownAt(6, "Taken down by admin"), nil,
			"False Acknowledged Taken down by admin"},
		{"a request before a take-down with no message", []observatory.ProcedureRun{activatedAt(1), notified(5)}, takenDownAt(6, ""), nil,
			"False Acknowledged Taken down"},
		{"a request before the set-up", []observatory.ProcedureRun{activatedAt(1), notified(5)}, setUpAt(6, observatory.ReasonSetUp), nil,
			"False Acknowledged Set up"},
		{"a request after the set-up", []observatory.ProcedureRun{activatedAt(1), notified(5)}, setUpAt(0, observatory.ReasonSetUp), nil, "True Notified Cover the telescope"},
		{"a set-up that expired after the request", []observatory.ProcedureRun{activatedAt(1), notified(5)}, setUpAt(6, observatory.ReasonExpired), nil,
			"True Notified Cover the telescope"},
		{"a request before the next activation", []observatory.ProcedureRun{activatedAt(20), notified(5)}, nil, nil,
			"False Activated The activation began after the request"},
		{"a request before an activity with no activation run", []observatory.ProcedureRun{notified(5)}, nil, at(20),
			"False Activated The activation began after the request"},
		{"a request that an activity cleared before it ended", []observatory.ProcedureRun{notified(5)}, []observatory.Condition{clearedAt(20)}, nil,
			"False Activated The activation began after the request"},
		{"a request after the condition cleared", []observatory.ProcedureRun{notified(21)}, []observatory.Condition{clearedAt(20)}, nil,
			"True Notified Cover the telescope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ""
			var active time.Time
			if c.active != nil {
				active = *c.active
			}
			if a, ok := attention(c.runs, c.stored, active); ok {
				got = string(a.Status) + " " + a.Reason + " " + a.Message
			}
			if got != c.want {
				t.Errorf("attention = %q, want %q", got, c.want)
			}
		})
	}
}
