package main

// The part of the fake API server that the applies need: a merge patch,
// which the operator sends to remove spec.hold, and the writes that
// machine-operator makes on a Disruption, which a test plays.

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// patch applies a JSON merge patch to an object, the way the API server
// does for application/merge-patch+json. A change of the spec moves the
// generation.
func (a *fakeAPI) patch(w http.ResponseWriter, r *http.Request, collection, name string) {
	body, _ := io.ReadAll(r.Body)
	var changes map[string]any
	if r.Header.Get("Content-Type") != "application/merge-patch+json" || json.Unmarshal(body, &changes) != nil {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	held, exists := a.objects[collection][name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	next := mergePatch(clone(held), changes)
	if _, spec := changes["spec"]; spec {
		metadata := next["metadata"].(map[string]any)
		generation, _ := metadata["generation"].(float64)
		metadata["generation"] = generation + 1
	}
	_ = json.NewEncoder(w).Encode(a.store(collection, next, "MODIFIED"))
}

// mergePatch merges changes into an object as RFC 7386 states: a null
// removes a field, an object merges into an object, and any other value
// replaces the field.
func mergePatch(object, changes map[string]any) map[string]any {
	for key, change := range changes {
		nested, isObject := change.(map[string]any)
		held, heldObject := object[key].(map[string]any)
		switch {
		case change == nil:
			delete(object, key)
		case isObject && heldObject:
			object[key] = mergePatch(held, nested)
		default:
			object[key] = change
		}
	}
	return object
}

// disruption answers a stored request for a reboot turn, and fails the
// test when there is none.
func (a *fakeAPI) disruption(t *testing.T, name string) disruption {
	t.Helper()
	a.mu.Lock()
	object := clone(a.objects[disruptionsPath][name])
	a.mu.Unlock()
	if object == nil {
		t.Fatalf("there is no Disruption %s", name)
	}
	var out disruption
	a.decode(t, object, &out)
	return out
}

// disruptionExists reports whether a request is stored.
func (a *fakeAPI) disruptionExists(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, exists := a.objects[disruptionsPath][name]
	return exists
}

// setTurn plays machine-operator: it sets one condition on a request.
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

// holdTurn plays machine-operator once it drained the machine of a
// request with spec.hold.
func (a *fakeAPI) holdTurn(t *testing.T, name string) {
	t.Helper()
	a.setTurn(t, name, heldCondition, "Drained")
}

// completeTurn plays machine-operator once the machine runs a boot after
// the one the request named.
func (a *fakeAPI) completeTurn(t *testing.T, name string) {
	t.Helper()
	a.setTurn(t, name, completeCondition, "BootChanged")
}
