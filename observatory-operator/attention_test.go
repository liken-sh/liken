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
			c.Reason != observatory.ReasonAcknowledged {
			t.Errorf("AttentionNeeded after the take-down = %+v", c)
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

func TestAttentionClearsWhenAPersonAnswersOrTheNextActivationBegins(t *testing.T) {
	cases := []struct {
		name   string
		runs   []observatory.ProcedureRun
		stored []observatory.Condition
		want   string
	}{
		{"no request", []observatory.ProcedureRun{activatedAt(1)}, nil, ""},
		{"a request", []observatory.ProcedureRun{activatedAt(1), notified(5)}, nil, "True Notified Cover the telescope"},
		{"a request before the set-up", []observatory.ProcedureRun{activatedAt(1), notified(5)}, setUpAt(6, observatory.ReasonTakenDown),
			"False Acknowledged A person wrote SetUp after the request"},
		{"a request after the set-up", []observatory.ProcedureRun{activatedAt(1), notified(5)}, setUpAt(0, observatory.ReasonSetUp), "True Notified Cover the telescope"},
		{"a set-up that expired after the request", []observatory.ProcedureRun{activatedAt(1), notified(5)}, setUpAt(6, observatory.ReasonExpired),
			"True Notified Cover the telescope"},
		{"a request before the next activation", []observatory.ProcedureRun{activatedAt(20), notified(5)}, nil,
			"False Activated The activation began after the request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ""
			if a, ok := attention(c.runs, c.stored); ok {
				got = string(a.Status) + " " + a.Reason + " " + a.Message
			}
			if got != c.want {
				t.Errorf("attention = %q, want %q", got, c.want)
			}
		})
	}
}
