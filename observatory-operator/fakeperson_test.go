package main

// A person who writes the SetUp condition. `kubectl liken observatory
// set-up` sends a server-side apply of the condition and of
// status.setUpUntil to the status subresource, and the API server
// merges the condition into status.conditions by its type, because
// the CRD lists the conditions as a map keyed by type. The fake makes
// the same change to the stored object.

import (
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// setUp writes a person's SetUp on a resource, as the plugin does: True
// with the reason SetUp, or False with the reason TakenDown, at the
// present time, and with a setUpUntil when until is not zero.
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

// applyCondition merges one condition into an object's
// status.conditions by its type, and sets or removes
// status.setUpUntil, as a server-side apply of the two fields does.
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

// conditionOn answers one condition of a resource as the API server
// holds it.
func (w *world) conditionOn(kind observatory.Kind, name, conditionType string) observatory.Condition {
	object, _ := decode[struct {
		Status struct {
			Conditions []observatory.Condition `json:"conditions"`
		} `json:"status"`
	}](w.t, w.api, kindCollection(kind), name)
	return conditionOf(object.Status.Conditions, conditionType)
}

// setUpExpiryTime answers a resource's status.setUpExpiryTime as the
// API server holds it, or the zero time when it has none.
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
