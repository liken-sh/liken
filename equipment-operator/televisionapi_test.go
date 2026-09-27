package main

// The fake API server's Television, Receiver, and Display collections.
// A status apply keeps each writer's fields apart the way server-side
// apply does for the fields these writers state: the Deployment's
// apply replaces cec, power, activeSource, activeDisplay, displays,
// and its own conditions, the Deployment's session apply replaces
// session, a node workload's power apply replaces powerGeneration and
// its own conditions, and a node workload's wake apply replaces wokeAt
// and its own conditions. No apply touches a field another manager owns.
// The conditions are a map keyed by type, so each apply leaves the
// other writer's conditions in place.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// serveTelevisionAPI answers a request for the collections this file
// holds, and false for any other request.
func (a *cecAPI) serveTelevisionAPI(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	a.mutex.Lock()
	refusing, missing := a.refusingTelevisions, a.noTelevisionDefinition
	listing := path == televisionsPath && r.Method == http.MethodGet && r.URL.Query().Get("watch") != "true"
	throttled := listing && a.throttledTelevisionLists > 0
	if throttled {
		a.throttledTelevisionLists--
	}
	a.mutex.Unlock()
	switch {
	case throttled:
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(initializingBody))
	case missing && strings.HasPrefix(path, televisionsPath):
		w.WriteHeader(http.StatusNotFound)
	case refusing && path == televisionsPath && r.URL.Query().Get("watch") != "true":
		w.WriteHeader(http.StatusInternalServerError)
	case path == displaysPath:
		a.serveJSON(w, func() any {
			list := DisplayList{}
			for _, name := range sortedKeys(a.displays) {
				list.Items = append(list.Items, *a.displays[name])
			}
			return list
		})
	case path == receiversPath:
		a.serveJSON(w, func() any {
			list := ReceiverList{Metadata: ListMeta{ResourceVersion: fmt.Sprint(a.version)}}
			for _, name := range sortedKeys(a.receivers) {
				list.Items = append(list.Items, a.receivers[name])
			}
			return list
		})
	case path == televisionsPath && r.URL.Query().Get("watch") == "true":
		a.serveWatch(w, r)
	case path == televisionsPath && r.Method == http.MethodPost:
		a.createTelevision(w, r)
	case path == televisionsPath:
		a.serveJSON(w, func() any {
			list := TelevisionList{Metadata: ListMeta{ResourceVersion: fmt.Sprint(a.version)}}
			for _, name := range sortedKeys(a.televisions) {
				list.Items = append(list.Items, copyTelevision(a.televisions[name]))
			}
			return list
		})
	case strings.HasPrefix(path, televisionsPath+"/"):
		a.serveTelevision(w, r, strings.TrimPrefix(path, televisionsPath+"/"))
	default:
		return false
	}
	return true
}

// createTelevision answers a create the way the API server does: a
// name that exists is a conflict, and a new object gets a uid and
// generation 1.
func (a *cecAPI) createTelevision(w http.ResponseWriter, r *http.Request) {
	var body Television
	_ = json.NewDecoder(r.Body).Decode(&body)
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if _, held := a.televisions[body.Metadata.Name]; held {
		w.WriteHeader(http.StatusConflict)
		return
	}
	a.store(body)
	a.created = append(a.created, body.Metadata.Name)
	a.changed()
	_, _ = io.WriteString(w, "{}")
}

// store keeps a new object with a new uid at generation 1. The caller
// holds the mutex.
func (a *cecAPI) store(television Television) {
	a.uids++
	television.Metadata.UID = fmt.Sprintf("uid-%d", a.uids)
	television.Metadata.Generation = 1
	television.Status = TelevisionStatus{Session: television.Status.Session}
	a.televisions[television.Metadata.Name] = &television
}

// serveJSON encodes what build answers under the mutex.
func (a *cecAPI) serveJSON(w http.ResponseWriter, build func() any) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	_ = json.NewEncoder(w).Encode(build())
}

func sortedKeys[V any](held map[string]V) []string {
	names := make([]string, 0, len(held))
	for name := range held {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (a *cecAPI) serveTelevision(w http.ResponseWriter, r *http.Request, rest string) {
	name, status := strings.CutSuffix(rest, "/status")
	var body Television
	_ = json.NewDecoder(r.Body).Decode(&body)
	manager := r.URL.Query().Get("fieldManager")
	node := strings.HasPrefix(manager, "equipment-operator-cec-")
	waking := strings.HasPrefix(manager, "equipment-operator-wake-")
	if manager == sessionFieldManager {
		// The fake counts the session writes in flight, and holds each one
		// for sessionDelay, so a test can see two that overlap.
		a.mutex.Lock()
		a.sessionInFlight++
		a.sessionMostAtOnce = max(a.sessionMostAtOnce, a.sessionInFlight)
		delay := a.sessionDelay
		a.mutex.Unlock()
		time.Sleep(delay)
		defer func() {
			a.mutex.Lock()
			a.sessionInFlight--
			a.mutex.Unlock()
		}()
	}
	a.mutex.Lock()
	defer a.mutex.Unlock()
	television, held := a.televisions[name]
	switch {
	case r.Method == http.MethodDelete:
		delete(a.televisions, name)
		a.deletedTelevisions = append(a.deletedTelevisions, name)
	case r.Method != http.MethodPatch || !status || !held:
		w.WriteHeader(http.StatusNotFound)
		return
	case body.Metadata.UID != "" && body.Metadata.UID != television.Metadata.UID:
		// An apply that states a uid is a precondition on it.
		w.WriteHeader(http.StatusConflict)
		return
	case waking && a.refusingWakeWrites:
		w.WriteHeader(http.StatusInternalServerError)
		return
	case manager == sessionFieldManager && a.noSessionWrites:
		w.WriteHeader(http.StatusInternalServerError)
		return
	case manager == sessionFieldManager:
		// A status write is no spec edit, so the generation stays, as on
		// a real API server.
		a.sessionWrites++
		television.Status.Session = body.Status.Session
	case node && a.refusingPowerWrites:
		w.WriteHeader(http.StatusInternalServerError)
		return
	case node:
		a.powerWrites++
		television.Status.PowerGeneration = body.Status.PowerGeneration
		television.Status.Conditions = mergeConditions(television.Status.Conditions, body.Status.Conditions)
	case waking:
		a.wakeWrites++
		television.Status.WokeAt = body.Status.WokeAt
		television.Status.Conditions = mergeConditions(television.Status.Conditions, body.Status.Conditions)
	default:
		a.derivedWrites++
		television.Status.CEC = body.Status.CEC
		television.Status.Power = body.Status.Power
		television.Status.ActiveSource = body.Status.ActiveSource
		television.Status.ActiveDisplay = body.Status.ActiveDisplay
		television.Status.Displays = body.Status.Displays
		television.Status.Conditions = mergeConditions(television.Status.Conditions, body.Status.Conditions)
	}
	a.changed()
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "{}")
}

// mergeConditions replaces each condition of the same type and keeps
// the others, the way server-side apply merges a map list.
func mergeConditions(held, applied []Condition) []Condition {
	merged := slices.DeleteFunc(slices.Clone(held), func(condition Condition) bool {
		return slices.ContainsFunc(applied, func(other Condition) bool { return other.Type == condition.Type })
	})
	return append(merged, applied...)
}

func copyTelevision(television *Television) Television {
	raw, _ := json.Marshal(television)
	var copied Television
	_ = json.Unmarshal(raw, &copied)
	return copied
}

// putTelevision stores a Television as a person declares it. A new
// name is a new object with a new uid. An existing object keeps its
// uid, status, and labels the person does not state, and a changed
// spec is a new generation, as on a real API server. A status.session
// the object states is the Deployment's write in the same step, a
// shortcut for a test that sets a session; a session it does not state
// stays as it is, as a person's apply of the spec leaves it.
func (a *cecAPI) putTelevision(television Television) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	held, found := a.televisions[television.Metadata.Name]
	if !found {
		a.store(television)
		a.changed()
		return
	}
	if !reflect.DeepEqual(held.Spec, television.Spec) {
		held.Metadata.Generation++
	}
	held.Spec = television.Spec
	if television.Status.Session != nil {
		held.Status.Session = television.Status.Session
	}
	for key, value := range television.Metadata.Labels {
		if held.Metadata.Labels == nil {
			held.Metadata.Labels = map[string]string{}
		}
		held.Metadata.Labels[key] = value
	}
	a.changed()
}

// removeTelevision deletes a Television, as a person or a prune does.
func (a *cecAPI) removeTelevision(name string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	delete(a.televisions, name)
	a.changed()
}

// refusePowerWrites makes the node workloads' status writes fail with a
// 500, or answer again.
func (a *cecAPI) refusePowerWrites(refusing bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.refusingPowerWrites = refusing
}

// television answers a snapshot of one Television, and false when it
// does not exist.
func (a *cecAPI) television(name string) (Television, bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	television, held := a.televisions[name]
	if !held {
		return Television{}, false
	}
	return copyTelevision(television), true
}

// refuseTelevisions turns the refusal of the Television list on or
// off.
func (a *cecAPI) refuseTelevisions(refusing bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.refusingTelevisions = refusing
}

func (a *cecAPI) putReceiver(receiver Receiver) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.receivers[receiver.Metadata.Name] = receiver
	a.changed()
}

// writes answers how many status writes each writer made to the
// Televisions.
func (a *cecAPI) writes() (derived, power int) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.derivedWrites, a.powerWrites
}

func (a *cecAPI) deletedTelevisionNames() []string {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return slices.Clone(a.deletedTelevisions)
}

// waitForTelevision waits until one Television satisfies a check, and
// fails the test with the last one it read otherwise.
func (a *cecAPI) waitForTelevision(t *testing.T, name string, ready func(Television) bool) Television {
	t.Helper()
	return a.waitForTelevisionWithin(t, name, testTimeout, ready)
}

func (a *cecAPI) waitForTelevisionWithin(t *testing.T, name string, within time.Duration, ready func(Television) bool) Television {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		television, held := a.television(name)
		if held && ready(television) {
			return television
		}
		if time.Now().After(deadline) {
			t.Fatalf("Television %s never held the wanted status; the last was %+v (held %v)", name, television, held)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// refuseWakeWrites makes the node workloads' wake writes fail with a
// 500, or answer again.
func (a *cecAPI) refuseWakeWrites(refusing bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.refusingWakeWrites = refusing
}
