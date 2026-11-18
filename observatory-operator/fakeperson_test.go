package main

import (
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// setUp writes the SetUp condition on a resource as `kubectl liken
// observatory set-up` does: True with the reason SetUp, or False with
// the reason TakenDown. It sets status.setUpUntil when until is not
// zero.
func (w *world) setUp(kind observatory.Kind, name string, up bool, until time.Time) {
	w.t.Helper()
	status, reason, message := "True", observatory.ReasonSetUp, "Set up by stargazer"
	if !up {
		status, reason, message = "False", observatory.ReasonTakenDown, "Taken down by stargazer"
	}
	w.api.applyCondition(kindCollection(kind), name, map[string]any{
		"type": observatory.ConditionSetUp, "status": status, "reason": reason, "message": message,
		"lastTransitionTime": stamp().Format(time.RFC3339),
	}, until)
}

// applyCondition replaces the condition of c's type in an object's
// status.conditions, and sets or removes status.setUpUntil. It does
// what the API server does with the plugin's server-side apply, which
// merges conditions by type because the CRD declares them a map keyed
// by type.
func (a *fakeAPI) applyCondition(collection, name string, c map[string]any, until time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	held, ok := a.objects[collection][name]
	if !ok {
		return
	}
	next := clone(held)
	status, _ := next["status"].(map[string]any)
	if status == nil {
		status = map[string]any{}
	}
	list, _ := status["conditions"].([]any)
	merged := []any{}
	for _, item := range list {
		if item.(map[string]any)["type"] != c["type"] {
			merged = append(merged, item)
		}
	}
	status["conditions"] = append(merged, c)
	delete(status, "setUpUntil")
	if !until.IsZero() {
		status["setUpUntil"] = until.UTC().Format(time.RFC3339)
	}
	next["status"] = status
	a.store(collection, name, next, "MODIFIED")
}

func (w *world) conditionOn(kind observatory.Kind, name, conditionType string) observatory.Condition {
	object, _ := decode[struct {
		Status struct {
			Conditions []observatory.Condition `json:"conditions"`
		} `json:"status"`
	}](w.t, w.api, kindCollection(kind), name)
	return conditionOf(object.Status.Conditions, conditionType)
}

// setUpExpiryTime returns a resource's status.setUpExpiryTime, or the
// zero time when it is not set.
func (w *world) setUpExpiryTime(kind observatory.Kind, name string) time.Time {
	object, _ := decode[struct {
		Status struct {
			SetUpExpiryTime *time.Time `json:"setUpExpiryTime"`
		} `json:"status"`
	}](w.t, w.api, kindCollection(kind), name)
	if object.Status.SetUpExpiryTime == nil {
		return time.Time{}
	}
	return *object.Status.SetUpExpiryTime
}
