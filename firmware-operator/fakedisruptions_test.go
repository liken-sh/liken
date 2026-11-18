package main

// The fake API server's support for the apply tests: the merge patch
// that the operator sends to remove spec.hold, and helpers that write a
// Disruption's status as machine-operator would.

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// patch applies an application/merge-patch+json request. Like the API
// server, it increments metadata.generation when the patch changes the
// spec.
func (a *fakeAPI) patch(w http.ResponseWriter, r *http.Request, collection, name string) {
	body, _ := io.ReadAll(r.Body)
	var changes map[string]any
	if r.Header.Get("Content-Type") != "application/merge-patch+json" || json.Unmarshal(body, &changes) != nil {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	stored, exists := a.objects[collection][name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	next := mergePatch(clone(stored), changes)
	if _, spec := changes["spec"]; spec {
		metadata := next["metadata"].(map[string]any)
		generation, _ := metadata["generation"].(float64)
		metadata["generation"] = generation + 1
	}
	_ = json.NewEncoder(w).Encode(a.store(collection, next, "MODIFIED"))
}

// mergePatch merges changes into an object by RFC 7386: a null deletes
// the field, an object merges into an object, and any other value
// replaces the field.
func mergePatch(object, changes map[string]any) map[string]any {
	for key, change := range changes {
		nested, isObject := change.(map[string]any)
		current, currentIsObject := object[key].(map[string]any)
		switch {
		case change == nil:
			delete(object, key)
		case isObject && currentIsObject:
			object[key] = mergePatch(current, nested)
		default:
			object[key] = change
		}
	}
	return object
}

// disruption returns the stored Disruption with this name, and fails the
// test when there is none.
func (a *fakeAPI) disruption(t *testing.T, name string) disruption {
	t.Helper()
	a.mu.Lock()
	object := clone(a.objects[disruptionsPath][name])
	a.mu.Unlock()
	if object == nil {
		t.Fatalf("the fake API server has no Disruption %s", name)
	}
	var out disruption
	a.decode(t, object, &out)
	return out
}

func (a *fakeAPI) disruptionExists(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, exists := a.objects[disruptionsPath][name]
	return exists
}

// setTurn sets one condition on a Disruption, as machine-operator does.
func (a *fakeAPI) setTurn(t *testing.T, name, kind, reason string) {
	t.Helper()
	d := a.disruption(t, name)
	conditions.Set(&d.Status.Conditions, condition{Type: kind, Status: conditions.True, Reason: reason,
		LastTransitionTime: time.Now().UTC().Truncate(time.Second)})
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[disruptionsPath][name])
	next["status"] = toMap(d.Status)
	a.store(disruptionsPath, next, "MODIFIED")
}

// holdTurn sets the condition that machine-operator sets after it
// drains the machine for a Disruption with spec.hold.
func (a *fakeAPI) holdTurn(t *testing.T, name string) {
	t.Helper()
	a.setTurn(t, name, heldCondition, "Drained")
}

// completeTurn sets the condition that machine-operator sets when the
// machine has booted again after the Disruption.
func (a *fakeAPI) completeTurn(t *testing.T, name string) {
	t.Helper()
	a.setTurn(t, name, completeCondition, "BootChanged")
}
