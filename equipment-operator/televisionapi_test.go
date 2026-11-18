package main

// The fake API server's Television, Receiver, and Display collections.
// A status apply changes only the fields of its own field manager, as
// server-side apply does:
//
//   - The Deployment's apply replaces cec, power, activeSource,
//     activeDisplay, displays, and its own conditions.
//   - The Deployment's session apply replaces session.
//   - A node workload's power apply replaces powerGeneration and its own
//     conditions.
//   - A node workload's wake apply replaces wokeAt and its own
//     conditions.
//   - A node workload's standby apply replaces standbyAt and its own
//     conditions.
//   - A node workload's power read apply replaces powerRead.
//   - A node workload's screen apply replaces screenAsk.

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

// serveTelevisionAPI returns false for a request outside the collections
// in this file.
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
	case refusing && r.Method == http.MethodGet && strings.HasPrefix(path, televisionsPath) && r.URL.Query().Get("watch") != "true":
		w.WriteHeader(http.StatusInternalServerError)
	case path == displaysPath && nodeOf(r) != "" && a.refusesDisplayNodeField():
		w.WriteHeader(http.StatusBadRequest)
	case path == displaysPath && r.URL.Query().Get("watch") == "true":
		a.serveWatch(w, r)
	case path == displaysPath:
		a.mutex.Lock()
		a.serveCollection(w, displaysPath, nodeOf(r))
		a.mutex.Unlock()
	case path == receiversPath && r.URL.Query().Get("watch") == "true":
		a.serveWatch(w, r)
	case r.Method == http.MethodPatch && strings.HasPrefix(path, receiversPath+"/") && strings.HasSuffix(path, "/status"):
		a.applyReceiverStatus(w, r, strings.TrimSuffix(strings.TrimPrefix(path, receiversPath+"/"), "/status"))
	case r.Method == http.MethodGet && strings.HasPrefix(path, receiversPath+"/"):
		a.mutex.Lock()
		a.serveStored(w, receiversPath, strings.TrimPrefix(path, receiversPath+"/"))
		a.mutex.Unlock()
	case path == receiversPath:
		a.mutex.Lock()
		a.serveCollection(w, receiversPath, "")
		a.mutex.Unlock()
	case path == televisionsPath && r.URL.Query().Get("watch") == "true":
		a.serveWatch(w, r)
	case path == televisionsPath && r.Method == http.MethodPost:
		a.createTelevision(w, r)
	case path == televisionsPath:
		a.mutex.Lock()
		a.serveCollection(w, televisionsPath, "")
		a.mutex.Unlock()
	case strings.HasPrefix(path, televisionsPath+"/"):
		a.serveTelevision(w, r, strings.TrimPrefix(path, televisionsPath+"/"))
	default:
		return false
	}
	return true
}

func (a *cecAPI) refusesDisplayNodeField() bool {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.noDisplayNodeField
}

// createTelevision returns a conflict for a name that already exists,
// as the API server does.
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
	a.serveStored(w, televisionsPath, body.Metadata.Name)
}

// store saves a new object with a new uid and generation 1, and keeps
// only the session and displays of its status. The caller holds the
// mutex.
func (a *cecAPI) store(television Television) {
	a.uids++
	television.Metadata.UID = fmt.Sprintf("uid-%d", a.uids)
	television.Metadata.Generation = 1
	delete(a.owners, television.Metadata.Name)
	television.Status = TelevisionStatus{Session: television.Status.Session, Displays: television.Status.Displays}
	a.televisions[television.Metadata.Name] = &television
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
	isWake := strings.HasPrefix(manager, "equipment-operator-wake-")
	isStandby := strings.HasPrefix(manager, "equipment-operator-standby-")
	isPowerRead := strings.HasPrefix(manager, "equipment-operator-powerread-")
	isScreen := strings.HasPrefix(manager, "equipment-operator-screen-")
	if manager == sessionFieldManager {
		// Each session write waits sessionDelay, so a test can detect two
		// writes that overlap.
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
	case r.Method == http.MethodGet && !status:
		a.serveStored(w, televisionsPath, name)
		return
	case r.Method == http.MethodDelete:
		delete(a.televisions, name)
		a.deletedTelevisions = append(a.deletedTelevisions, name)
	case r.Method != http.MethodPatch || !status || !held:
		w.WriteHeader(http.StatusNotFound)
		return
	case body.Metadata.UID != "" && body.Metadata.UID != television.Metadata.UID:
		// An apply that includes a uid requires the object to have it.
		w.WriteHeader(http.StatusConflict)
		return
	case isWake && a.refusingWakeWrites:
		w.WriteHeader(http.StatusInternalServerError)
		return
	case manager == sessionFieldManager && a.noSessionWrites:
		w.WriteHeader(http.StatusInternalServerError)
		return
	case manager == sessionFieldManager:
		// A status write does not change the generation.
		a.sessionWrites++
		television.Status.Session = body.Status.Session
	case node && a.refusingPowerWrites:
		w.WriteHeader(http.StatusInternalServerError)
		return
	case node:
		a.powerWrites++
		television.Status.PowerGeneration = body.Status.PowerGeneration
		a.applyConditions(television, manager, body.Status.Conditions)
	case isWake:
		a.wakeWrites++
		television.Status.WokeAt = body.Status.WokeAt
		a.applyConditions(television, manager, body.Status.Conditions)
	case isPowerRead:
		television.Status.PowerRead = body.Status.PowerRead
	case isScreen:
		television.Status.ScreenAsk = body.Status.ScreenAsk
	case isStandby:
		television.Status.StandbyAt = body.Status.StandbyAt
		a.applyConditions(television, manager, body.Status.Conditions)
	default:
		a.derivedWrites++
		television.Status.CEC = body.Status.CEC
		television.Status.Power = body.Status.Power
		television.Status.ActiveSource = body.Status.ActiveSource
		television.Status.ActiveDisplay = body.Status.ActiveDisplay
		television.Status.Displays = body.Status.Displays
		a.applyConditions(television, manager, body.Status.Conditions)
	}
	a.changed()
	if r.Method == http.MethodDelete {
		_, _ = io.WriteString(w, "{}")
		return
	}
	a.serveStored(w, televisionsPath, name)
}

// applyReceiverStatus applies a Receiver's status. An apply from
// receiverCECFieldManager replaces only status.cec.
func (a *cecAPI) applyReceiverStatus(w http.ResponseWriter, r *http.Request, name string) {
	var body Receiver
	_ = json.NewDecoder(r.Body).Decode(&body)
	a.mutex.Lock()
	defer a.mutex.Unlock()
	receiver, held := a.receivers[name]
	if !held {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("fieldManager") == receiverCECFieldManager {
		a.receiverCECWrites++
		receiver.Status.CEC = body.Status.CEC
	} else {
		receiver.Status.ReceiverStatus = body.Status.ReceiverStatus
	}
	a.receivers[name] = receiver
	a.changed()
	a.serveStored(w, receiversPath, name)
}

// applyConditions merges one manager's conditions the way a forced
// server-side apply merges a list keyed by type. It removes each
// condition that the manager owns and no longer sends, replaces each
// type it sends and takes ownership of it, and keeps the rest. The
// caller holds the mutex.
func (a *cecAPI) applyConditions(television *Television, manager string, applied []Condition) {
	name := television.Metadata.Name
	if a.owners[name] == nil {
		a.owners[name] = map[string]string{}
	}
	owned := a.owners[name]
	kept := slices.DeleteFunc(slices.Clone(television.Status.Conditions), func(condition Condition) bool {
		stated := slices.ContainsFunc(applied, func(other Condition) bool { return other.Type == condition.Type })
		return owned[condition.Type] == manager && !stated
	})
	for _, condition := range applied {
		owned[condition.Type] = manager
	}
	television.Status.Conditions = mergeConditions(kept, applied)
}

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

// putTelevision stores a Television as a person would apply it. An
// existing object keeps its uid, its status, and the labels that
// television does not set, and a changed spec increments the
// generation. A status.session, status.screenAsk, or status.displays in
// television replaces the stored one, so a test can set them without
// running the workload that writes them.
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
	if television.Status.ScreenAsk != nil {
		held.Status.ScreenAsk = television.Status.ScreenAsk
	}
	if television.Status.Displays != nil {
		held.Status.Displays = television.Status.Displays
	}
	for key, value := range television.Metadata.Labels {
		if held.Metadata.Labels == nil {
			held.Metadata.Labels = map[string]string{}
		}
		held.Metadata.Labels[key] = value
	}
	a.changed()
}

func (a *cecAPI) removeTelevision(name string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	delete(a.televisions, name)
	a.changed()
}

// refusePowerWrites makes the node workloads' power applies fail with a
// 500. Their wake, standby, power read, and screen applies still
// succeed.
func (a *cecAPI) refusePowerWrites(refusing bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.refusingPowerWrites = refusing
}

func (a *cecAPI) television(name string) (Television, bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	television, held := a.televisions[name]
	if !held {
		return Television{}, false
	}
	return copyTelevision(television), true
}

// refuseTelevisions makes every GET of Televisions, other than a watch,
// fail with a 500 while refusing is true.
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

// editReceiver replaces a Receiver, increments its generation as the
// API server does, and notifies only the Receiver watches.
func (a *cecAPI) editReceiver(receiver Receiver) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	receiver.Metadata.Generation = a.receivers[receiver.Metadata.Name].Metadata.Generation + 1
	a.receivers[receiver.Metadata.Name] = receiver
	a.changedIn(receiversPath)
}

// writes returns the number of the Deployment's status applies, not
// counting session applies, and of the node workloads' power applies.
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

// waitForTelevision waits until the Television satisfies ready, and
// fails the test after testTimeout.
func (a *cecAPI) waitForTelevision(t *testing.T, name string, ready func(Television) bool) Television {
	t.Helper()
	return a.waitForTelevisionWithin(t, name, testTimeout, ready)
}

func (a *cecAPI) waitForTelevisionWithin(t *testing.T, name string, within time.Duration, ready func(Television) bool) Television {
	t.Helper()
	deadline := time.After(within)
	for {
		moved := a.nextMove()
		television, held := a.television(name)
		if held && ready(television) {
			return television
		}
		if !awaitMove(moved, deadline) {
			t.Fatalf("Television %s did not reach the wanted status in time. Last read: %+v, found: %v", name, television, held)
		}
	}
}

// refuseWakeWrites makes the node workloads' wake applies fail with a
// 500.
func (a *cecAPI) refuseWakeWrites(refusing bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.refusingWakeWrites = refusing
}
