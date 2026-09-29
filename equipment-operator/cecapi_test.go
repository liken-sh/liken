package main

// A fake API server for the CECBus and Television tests. This file
// holds the CECBus and Display objects, and televisionapi_test.go adds
// the Televisions and the Receivers. It applies each write the way
// server-side apply does for the fields these writers state: a node
// workload's apply replaces its own machine's entry under
// status.adapters, and the Deployment's apply replaces the devices and
// the conditions. A watch gets one event for each change, so a loop
// under test wakes the way it wakes on a cluster.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type cecAPI struct {
	mutex       sync.Mutex
	buses       map[string]*CECBus
	displays    map[string]*Display
	televisions map[string]*Television
	receivers   map[string]Receiver
	// deletedTelevisions names each Television a writer deleted, and the
	// two counts are the Television status writes of the Deployment and
	// of the node workloads.
	deletedTelevisions []string
	derivedWrites      int
	// noDisplayNodeField refuses a Display list or watch by status.node,
	// the way an API server does whose Display definition declares no
	// such selectable field.
	noDisplayNodeField bool
	// busWrites counts the Deployment's CECBus status writes.
	busWrites     int
	powerWrites   int
	wakeWrites    int
	sessionWrites int
	version       int
	watchers      []*fakeWatcher
	deleted       []string
	client        *Client
	// refusing makes every list and every status write fail with a
	// 500, the way an API server answers while it is unhealthy.
	// refusingTelevisions does the same for the Television list alone.
	refusing            bool
	refusingTelevisions bool
	// refusingPowerWrites refuses the node workloads' Television status
	// writes, and noTelevisionDefinition answers every Television path
	// with not found, the way a cluster without the definition does.
	refusingPowerWrites    bool
	noTelevisionDefinition bool
	// noSessionWrites refuses the Deployment's status.session writes.
	noSessionWrites bool
	// throttledTelevisionLists counts the Television lists still to
	// answer with 429, the way the API server answers while a new CRD's
	// storage starts.
	throttledTelevisionLists int
	// refusingWakeWrites refuses the node workloads' wake writes.
	refusingWakeWrites bool
	// sessionDelay holds each session write, and sessionInFlight and
	// sessionMostAtOnce count the session writes that overlap.
	sessionDelay      time.Duration
	sessionInFlight   int
	sessionMostAtOnce int
	// uids numbers the Televisions the fake creates, and created names
	// each one a writer created with a POST.
	uids    int
	created []string
	// entryWrites counts the status writes of each node workload's
	// field manager.
	entryWrites map[string]int
	// throttlingEntries answers every node workload's status write with
	// a 429 that asks for a five-second wait, the way an API server that
	// is not ready answers, and entryThrottles counts those answers.
	throttlingEntries bool
	entryThrottles    int
	// createdUnseen names a CECBus that a read of the one object answers
	// 404 for, the way a bus a person creates a moment after the read
	// looks to the reader.
	createdUnseen string
	// reads counts each GET that is not a watch, a list or a read of
	// one object, by path, and watches counts the watches by path.
	reads   map[string]int
	watches map[string]int
	// stamps holds each object's content, by its path, when its
	// resourceVersion last moved, and that version. An object keeps its
	// version while its content stays, as on a real API server.
	stamps map[string]stamp
}

// stamp is one object's content and the version it took.
type stamp struct {
	content, version string
}

// refuse turns the refusals on or off.
func (a *cecAPI) refuse(refusing bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.refusing = refusing
}

func startCECAPI(t *testing.T) *cecAPI {
	t.Helper()
	api := &cecAPI{
		buses: map[string]*CECBus{}, displays: map[string]*Display{},
		televisions: map[string]*Television{}, receivers: map[string]Receiver{},
	}
	api.client = testAPIClient(t, http.HandlerFunc(api.handle))
	return api
}

// fakeWatcher is one open watch, the collection it watches, and the
// copy of each object it last sent, which is what the informer on the
// far end holds.
type fakeWatcher struct {
	path string
	// node is the machine a field selector on status.node names, or
	// empty for a watch of the whole collection.
	node   string
	sent   map[string]map[string]any
	events chan string
}

// nodeOf answers the machine a request's field selector on status.node
// names, or empty.
func nodeOf(r *http.Request) string {
	node, _ := strings.CutPrefix(r.URL.Query().Get("fieldSelector"), "status.node=")
	return node
}

// selectedIn answers the objects of one collection a watch or a list
// selects: every object, or the objects whose status.node is node. The
// caller holds the mutex.
func (a *cecAPI) selectedIn(path, node string) map[string]map[string]any {
	objects := a.collection(path)
	if node == "" {
		return objects
	}
	for name, object := range objects {
		status, _ := object["status"].(map[string]any)
		if status["node"] != node {
			delete(objects, name)
		}
	}
	return objects
}

// changed bumps the version and sends every watch the change in its
// collection. The caller holds the mutex.
func (a *cecAPI) changed() {
	a.version++
	for _, watcher := range a.watchers {
		a.send(watcher)
	}
}

// changedIn bumps the version and sends the change to the watches of
// one collection alone, the way the API server does. The caller holds
// the mutex.
func (a *cecAPI) changedIn(path string) {
	a.version++
	for _, watcher := range a.watchers {
		if watcher.path == path {
			a.send(watcher)
		}
	}
}

// send tells one watch what its collection holds now: an ADDED event
// for each new object, a DELETED event for each object that is gone,
// and a MODIFIED event for every other object, changed or not, so each
// change wakes a loop that wakes on every event, the way an unrelated
// edit of an object it holds does. The events go out oldest version
// first, and a bookmark at the collection's version ends them, so the
// reflector resumes from the newest version. The caller holds the
// mutex.
func (a *cecAPI) send(watcher *fakeWatcher) {
	now := a.selectedIn(watcher.path, watcher.node)
	var events []string
	names := sortedKeys(now)
	slices.SortStableFunc(names, func(x, y string) int {
		return versionOf(now[x]) - versionOf(now[y])
	})
	for _, name := range names {
		kind := "MODIFIED"
		if _, held := watcher.sent[name]; !held {
			kind = "ADDED"
		}
		events = append(events, watchEvent(kind, now[name]))
	}
	for _, name := range sortedKeys(watcher.sent) {
		if _, held := now[name]; !held {
			events = append(events, watchEvent("DELETED", watcher.sent[name]))
		}
	}
	events = append(events, a.bookmark(watcher.path, false))
	watcher.sent = now
	for _, event := range events {
		select {
		case watcher.events <- event:
		default:
		}
	}
}

// fakeKinds names the kind the fake serves at each collection path.
var fakeKinds = map[string][2]string{
	cecBusesPath:    {equipmentAPIVersion, "CECBus"},
	televisionsPath: {equipmentAPIVersion, "Television"},
	receiversPath:   {equipmentAPIVersion, "Receiver"},
	displaysPath:    {"display.liken.sh/v1alpha1", "Display"},
}

// collection answers each object of one collection by name, as a watch
// event carries it. The caller holds the mutex.
func (a *cecAPI) collection(path string) map[string]map[string]any {
	objects := map[string]any{}
	switch path {
	case cecBusesPath:
		for name, bus := range a.buses {
			objects[name] = bus
		}
	case televisionsPath:
		for name, television := range a.televisions {
			objects[name] = television
		}
	case receiversPath:
		for name, receiver := range a.receivers {
			objects[name] = receiver
		}
	case displaysPath:
		for name, display := range a.displays {
			objects[name] = display
		}
	}
	kind := fakeKinds[path]
	fields := make(map[string]map[string]any, len(objects))
	for name, object := range objects {
		encoded, _ := json.Marshal(object)
		var one map[string]any
		_ = json.Unmarshal(encoded, &one)
		meta, _ := one["metadata"].(map[string]any)
		if meta == nil {
			meta = map[string]any{}
		}
		meta["name"] = name
		delete(meta, "resourceVersion")
		one["metadata"] = meta
		one["apiVersion"], one["kind"] = kind[0], kind[1]
		content, _ := json.Marshal(one)
		key := path + "/" + name
		if a.stamps == nil {
			a.stamps = map[string]stamp{}
		}
		if a.stamps[key].content != string(content) {
			a.stamps[key] = stamp{content: string(content), version: fmt.Sprint(a.version)}
		}
		meta["resourceVersion"] = a.stamps[key].version
		fields[name] = one
	}
	return fields
}

// versionOf answers an object's resourceVersion as a number.
func versionOf(object map[string]any) int {
	meta, _ := object["metadata"].(map[string]any)
	version, _ := meta["resourceVersion"].(string)
	number, _ := strconv.Atoi(version)
	return number
}

// stored answers one object as the API server returns it, with its
// resourceVersion, or nil when the collection does not hold it. The
// caller holds the mutex.
func (a *cecAPI) stored(path, name string) map[string]any {
	return a.collection(path)[name]
}

// serveStored answers a read of one object, or a write, with the
// object as it stands. The caller holds the mutex.
func (a *cecAPI) serveStored(w http.ResponseWriter, path, name string) {
	object := a.stored(path, name)
	if object == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(object)
}

func watchEvent(kind string, object map[string]any) string {
	encoded, _ := json.Marshal(map[string]any{"type": kind, "object": object})
	return string(encoded)
}

// bookmark is a bookmark at the collection's version. The last one of
// a streaming list marks the end of its initial events. The caller
// holds the mutex.
func (a *cecAPI) bookmark(path string, initialEventsEnd bool) string {
	kind := fakeKinds[path]
	meta := map[string]any{"resourceVersion": fmt.Sprint(a.version)}
	if initialEventsEnd {
		meta["annotations"] = map[string]string{"k8s.io/initial-events-end": "true"}
	}
	return watchEvent("BOOKMARK", map[string]any{"apiVersion": kind[0], "kind": kind[1], "metadata": meta})
}

func (a *cecAPI) handle(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	a.mutex.Lock()
	refusing := a.refusing
	a.mutex.Unlock()
	watch := r.URL.Query().Get("watch") == "true"
	if r.Method == http.MethodGet && !watch {
		a.mutex.Lock()
		if a.reads == nil {
			a.reads = map[string]int{}
		}
		a.reads[path]++
		a.mutex.Unlock()
	}
	if refusing && !watch && (r.Method == http.MethodGet && path == cecBusesPath || strings.HasSuffix(path, "/status")) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if a.serveTelevisionAPI(w, r) {
		return
	}
	switch {
	case strings.HasPrefix(path, displaysPath+"/"):
		a.serveDisplay(w, strings.TrimPrefix(path, displaysPath+"/"))
	case path == cecBusesPath && r.URL.Query().Get("watch") == "true":
		a.serveWatch(w, r)
	case r.Method == http.MethodPost && path == cecBusesPath:
		a.create(w, r)
	case path == cecBusesPath:
		a.serveList(w)
	case r.Method == http.MethodGet && strings.HasPrefix(path, cecBusesPath+"/"):
		a.mutex.Lock()
		name := strings.TrimPrefix(path, cecBusesPath+"/")
		if name == a.createdUnseen {
			w.WriteHeader(http.StatusNotFound)
		} else {
			a.serveStored(w, cecBusesPath, name)
		}
		a.mutex.Unlock()
	case r.Method == http.MethodPatch && strings.HasSuffix(path, "/status"):
		a.applyStatus(w, r, strings.TrimSuffix(strings.TrimPrefix(path, cecBusesPath+"/"), "/status"))
	case r.Method == http.MethodPatch:
		a.applySpec(w, r, strings.TrimPrefix(path, cecBusesPath+"/"))
	case r.Method == http.MethodDelete:
		a.delete(w, strings.TrimPrefix(path, cecBusesPath+"/"))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (a *cecAPI) serveDisplay(w http.ResponseWriter, name string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	display, held := a.displays[name]
	if !held {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(display)
}

func (a *cecAPI) serveList(w http.ResponseWriter) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	list := CECBusList{Metadata: ListMeta{ResourceVersion: fmt.Sprint(a.version)}}
	names := make([]string, 0, len(a.buses))
	for name := range a.buses {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		list.Items = append(list.Items, copyBus(a.buses[name]))
	}
	_ = json.NewEncoder(w).Encode(list)
}

// serveWatch answers a watch the way the API server does. A streaming
// list, the read client-go's reflector sends first, gets an ADDED event
// for each object and a bookmark that ends the initial events. A watch
// that resumes from the current version gets what changes after it,
// and one that resumes from an older version gets a 410, which makes
// the reflector read the collection again.
func (a *cecAPI) serveWatch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	watcher := &fakeWatcher{path: r.URL.Path, node: nodeOf(r), events: make(chan string, 1024)}
	a.mutex.Lock()
	if a.watches == nil {
		a.watches = map[string]int{}
	}
	a.watches[r.URL.Path]++
	var opening []string
	switch {
	case query.Get("sendInitialEvents") == "true":
		watcher.sent = a.selectedIn(watcher.path, watcher.node)
		for _, name := range sortedKeys(watcher.sent) {
			opening = append(opening, watchEvent("ADDED", watcher.sent[name]))
		}
		opening = append(opening, a.bookmark(watcher.path, true))
	case query.Get("resourceVersion") == fmt.Sprint(a.version):
		watcher.sent = a.selectedIn(watcher.path, watcher.node)
	default:
		opening = append(opening, `{"type":"ERROR","object":{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Expired","code":410}}`)
		watcher = nil
	}
	if watcher != nil {
		a.watchers = append(a.watchers, watcher)
	}
	a.mutex.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	for _, event := range opening {
		_, _ = io.WriteString(w, event+"\n")
	}
	w.(http.Flusher).Flush()
	if watcher == nil {
		return
	}
	defer a.forget(watcher)
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-watcher.events:
			_, _ = io.WriteString(w, event+"\n")
			w.(http.Flusher).Flush()
		}
	}
}

// forget drops a watch whose connection ended.
func (a *cecAPI) forget(watcher *fakeWatcher) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.watchers = slices.DeleteFunc(a.watchers, func(one *fakeWatcher) bool { return one == watcher })
}

// create answers a POST the way the API server does: a name that exists
// answers 409 and changes nothing.
func (a *cecAPI) create(w http.ResponseWriter, r *http.Request) {
	var body CECBus
	_ = json.NewDecoder(r.Body).Decode(&body)
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if _, held := a.buses[body.Metadata.Name]; held {
		w.WriteHeader(http.StatusConflict)
		return
	}
	body.Metadata.Generation = 1
	a.buses[body.Metadata.Name] = &body
	a.changed()
	a.serveStored(w, cecBusesPath, body.Metadata.Name)
}

func (a *cecAPI) applySpec(w http.ResponseWriter, r *http.Request, name string) {
	var body CECBus
	_ = json.NewDecoder(r.Body).Decode(&body)
	a.mutex.Lock()
	defer a.mutex.Unlock()
	bus, held := a.buses[name]
	if !held {
		bus = &CECBus{Metadata: ObjectMeta{Name: name, Generation: 1}}
		a.buses[name] = bus
	}
	bus.Metadata.Labels = body.Metadata.Labels
	bus.Spec = body.Spec
	a.changed()
	a.serveStored(w, cecBusesPath, name)
}

func (a *cecAPI) applyStatus(w http.ResponseWriter, r *http.Request, name string) {
	var body CECBus
	_ = json.NewDecoder(r.Body).Decode(&body)
	manager := r.URL.Query().Get("fieldManager")
	a.mutex.Lock()
	defer a.mutex.Unlock()
	bus, held := a.buses[name]
	if !held {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if machine, node := strings.CutPrefix(manager, "equipment-operator-cec-"); node {
		if a.throttlingEntries {
			a.entryThrottles++
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if a.entryWrites == nil {
			a.entryWrites = map[string]int{}
		}
		a.entryWrites[machine]++
		bus.Status.Adapters = slices.DeleteFunc(bus.Status.Adapters, func(entry CECAdapterStatus) bool { return entry.Machine == machine })
		bus.Status.Adapters = append(bus.Status.Adapters, body.Status.Adapters...)
	} else {
		a.busWrites++
		bus.Status.Devices = body.Status.Devices
		bus.Status.Conditions = body.Status.Conditions
	}
	a.changed()
	a.serveStored(w, cecBusesPath, name)
}

func (a *cecAPI) delete(w http.ResponseWriter, name string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	delete(a.buses, name)
	a.deleted = append(a.deleted, name)
	a.changed()
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "{}")
}

// copyBus is a deep copy through JSON, so a test reads a snapshot the
// handler cannot change under it.
func copyBus(bus *CECBus) CECBus {
	raw, _ := json.Marshal(bus)
	var copied CECBus
	_ = json.Unmarshal(raw, &copied)
	return copied
}

// putBus stores a bus as a person or a test declares it.
func (a *cecAPI) putBus(bus CECBus) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if bus.Metadata.Generation == 0 {
		bus.Metadata.Generation = 1
	}
	a.buses[bus.Metadata.Name] = &bus
	a.changed()
}

// writesOf counts one machine's entry writes.
func (a *cecAPI) writesOf(machine string) int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.entryWrites[machine]
}

// removeDisplay deletes a Display, so a read of it answers not found.
func (a *cecAPI) removeDisplay(name string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	delete(a.displays, name)
	a.changed()
}

// nudge wakes every watch with no change, the way an unrelated edit
// does, so the loop under test runs a pass.
func (a *cecAPI) nudge() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.changed()
}

// moveDisplay gives a Display a new physical address, and tells only
// the watches of the Displays.
func (a *cecAPI) moveDisplay(name, physicalAddress string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.displays[name].Status.PhysicalAddress = physicalAddress
	a.changedIn(displaysPath)
}

func (a *cecAPI) putDisplay(name, node, physicalAddress string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	display := &Display{Metadata: ObjectMeta{Name: name}}
	display.Status.Node = node
	display.Status.PhysicalAddress = physicalAddress
	a.displays[name] = display
	a.changed()
}

// bus answers a snapshot of one bus, and false when it does not exist.
func (a *cecAPI) bus(name string) (CECBus, bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	bus, held := a.buses[name]
	if !held {
		return CECBus{}, false
	}
	return copyBus(bus), true
}

func (a *cecAPI) deletedNames() []string {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return slices.Clone(a.deleted)
}

// entry answers one machine's entry in one bus.
func (a *cecAPI) entry(bus, machine string) (CECAdapterStatus, bool) {
	held, found := a.bus(bus)
	if !found {
		return CECAdapterStatus{}, false
	}
	for _, entry := range held.Status.Adapters {
		if entry.Machine == machine {
			return entry, true
		}
	}
	return CECAdapterStatus{}, false
}

// waitForEntry waits until one machine's entry in one bus satisfies a
// check, and fails the test with the last entry it read otherwise.
func (a *cecAPI) waitForEntry(t *testing.T, bus, machine string, ready func(CECAdapterStatus) bool) CECAdapterStatus {
	t.Helper()
	return a.waitForEntryWithin(t, bus, machine, testTimeout, ready)
}

// vividScanTime bounds a scan on the kernel's vivid driver, which
// sends each message at the speed of a real CEC wire, so a scan takes
// a few seconds.
const vividScanTime = 20 * time.Second

func (a *cecAPI) waitForEntryWithin(t *testing.T, bus, machine string, within time.Duration, ready func(CECAdapterStatus) bool) CECAdapterStatus {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		entry, held := a.entry(bus, machine)
		if held && ready(entry) {
			return entry
		}
		if time.Now().After(deadline) {
			t.Fatalf("CECBus %s never held the wanted entry for %s; the last was %+v (held %v)", bus, machine, entry, held)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitUntil waits for a condition on the fake's state.
func (a *cecAPI) waitUntil(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func writeEmpty(path string) error {
	return os.WriteFile(path, nil, 0o600)
}
