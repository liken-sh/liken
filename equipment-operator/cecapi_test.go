package main

// cecAPI is a fake API server for the CECBus and Television tests. This
// file serves CECBuses and Displays, televisionapi_test.go serves
// Televisions and Receivers, and cecpodapi_test.go serves the node
// workload's pods.
//
// A status write replaces only the fields that its field manager writes,
// as server-side apply does. A node workload's write replaces that
// machine's entry in status.adapters, and the Deployment's write
// replaces status.devices and status.conditions.

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
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

type cecAPI struct {
	mutex       sync.Mutex
	buses       map[string]*CECBus
	displays    map[string]*Display
	televisions map[string]*Television
	receivers   map[string]Receiver
	// pods are the node workload's pods that a test declared, by name.
	// podsDeclared is true when a test declared any (cecpodapi_test.go).
	pods         map[string]fakePod
	podsDeclared bool
	// owners maps each Television name and condition type to the field
	// manager that last wrote the condition. applyConditions reads it.
	owners map[string]map[string]string
	// deletedTelevisions lists the Televisions that a client deleted.
	// derivedWrites counts the Deployment's Television status writes,
	// and powerWrites counts the node workloads' Television status
	// writes.
	deletedTelevisions []string
	derivedWrites      int
	powerWrites        int
	// noDisplayNodeField makes a Display list or watch with a status.node
	// field selector fail, as it does on an API server whose Display
	// definition declares no such selectable field.
	noDisplayNodeField bool
	// busWrites counts the Deployment's CECBus status writes.
	busWrites     int
	wakeWrites    int
	sessionWrites int
	// receiverCECWrites counts the Deployment's status.cec writes on a
	// Receiver.
	receiverCECWrites int
	version           int
	// moved is closed and replaced at each change, so a test that waits
	// for a state checks again at each change instead of polling.
	moved    chan struct{}
	watchers []*fakeWatcher
	deleted  []string
	client   *Client
	events   *eventstest.Events
	// refusing makes every CECBus list and every status write fail with
	// a 500. refusingTelevisions does the same for the Television list
	// only.
	refusing            bool
	refusingTelevisions bool
	// refusingPowerWrites fails the node workloads' Television status
	// writes with a 500. noTelevisionDefinition returns 404 for every
	// Television path, as a cluster without the Television CRD does.
	refusingPowerWrites    bool
	noTelevisionDefinition bool
	// noSessionWrites fails the Deployment's status.session writes with a
	// 500.
	noSessionWrites bool
	// throttledTelevisionLists is how many more Television lists fail
	// with a 429, as they do while the API server initializes the storage
	// of a new CRD.
	throttledTelevisionLists int
	// refusingWakeWrites fails the node workloads' wake writes with a
	// 500.
	refusingWakeWrites bool
	// sessionDelay delays each session write. sessionInFlight is the
	// number of session writes in progress, and sessionMostAtOnce is the
	// largest that number has been.
	sessionDelay      time.Duration
	sessionInFlight   int
	sessionMostAtOnce int
	// uids numbers the uids of the Televisions that the fake creates, and
	// created lists the names of the Televisions that a client created
	// with a POST.
	uids    int
	created []string
	// entryWrites counts the status writes of each node workload, by
	// machine.
	entryWrites map[string]int
	// throttlingEntries makes every node workload's CECBus status write
	// fail with a 429 and a Retry-After of 5 seconds. entryThrottles
	// counts those responses.
	throttlingEntries bool
	entryThrottles    int
	// While holdingEntries is not nil, a node workload's CECBus status
	// write blocks until holdingEntries is closed, unless its entry is
	// Stopped. Each blocked write first sends a value on heldEntry, and
	// waits for the test to receive it. holds counts the blocked writes
	// that have not finished. When stoppedEntry is not nil, each write of
	// a Stopped entry sends a value on it, or drops the value when no one
	// is receiving.
	holdingEntries chan struct{}
	heldEntry      chan struct{}
	holds          sync.WaitGroup
	stoppedEntry   chan struct{}
	// reads counts the GET requests that are not watches, by path.
	// watches counts the watch requests, by path.
	reads   map[string]int
	watches map[string]int
	// versions maps an object's path to its JSON content and the
	// resourceVersion at which that content last changed, so an object
	// keeps its resourceVersion until its content changes, as on a real
	// API server.
	versions map[string]contentVersion
}

type contentVersion struct {
	content, version string
}

func (a *cecAPI) refuse(refusing bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.refusing = refusing
}

func startCECAPI(t *testing.T) *cecAPI {
	t.Helper()
	api := &cecAPI{
		buses: map[string]*CECBus{}, displays: map[string]*Display{},
		televisions: map[string]*Television{}, receivers: map[string]Receiver{}, owners: map[string]map[string]string{},
		events: &eventstest.Events{},
	}
	api.client = testAPIClient(t, api.events.Around(http.HandlerFunc(api.handle)))
	return api
}

// fakeWatcher is one open watch. sent holds the objects that the watch
// has sent, which is the client's view of the collection.
type fakeWatcher struct {
	path string
	// node is the machine in the status.node field selector, or empty
	// for a watch of the whole collection.
	node   string
	sent   map[string]map[string]any
	events chan string
}

// nodeOf returns the machine in the request's status.node field
// selector, or "" when there is none.
func nodeOf(r *http.Request) string {
	node, _ := strings.CutPrefix(r.URL.Query().Get("fieldSelector"), "status.node=")
	return node
}

// selectedIn returns the objects of one collection that a watch or a
// list selects: all of them, or the ones with status.node equal to
// node. The caller holds the mutex.
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

// changed increments the version and sends each watch the changes in
// its collection. The caller holds the mutex.
func (a *cecAPI) changed() {
	a.version++
	a.move()
	for _, watcher := range a.watchers {
		a.send(watcher)
	}
}

// changedIn increments the version and sends the changes only to the
// watches of one collection, as the API server does. The caller holds
// the mutex.
func (a *cecAPI) changedIn(path string) {
	a.version++
	a.move()
	for _, watcher := range a.watchers {
		if watcher.path == path {
			a.send(watcher)
		}
	}
}

// move closes the moved channel, which ends each wait for a change,
// and replaces it. The caller holds the mutex.
func (a *cecAPI) move() {
	close(a.movement())
	a.moved = make(chan struct{})
}

// movement is nextMove for a caller that holds the mutex.
func (a *cecAPI) movement() chan struct{} {
	if a.moved == nil {
		a.moved = make(chan struct{})
	}
	return a.moved
}

// nextMove returns the channel that closes at the next change. A wait
// gets the channel before it reads the state, so a change that happens
// between the read and the wait still ends the wait.
func (a *cecAPI) nextMove() <-chan struct{} {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.movement()
}

// awaitMove waits for moved to close. It returns false when the
// deadline fires first.
func awaitMove(moved <-chan struct{}, deadline <-chan time.Time) bool {
	select {
	case <-moved:
		return true
	case <-deadline:
		return false
	}
}

// send brings one watch up to date with its collection. It sends ADDED
// for each object that the watch has not sent and MODIFIED for every
// other object, changed or not, in resourceVersion order. Then it sends
// DELETED for each sent object that no longer exists, and last a
// bookmark at the collection's version, so the reflector resumes from
// the newest version. The MODIFIED events for unchanged objects make a
// loop that reacts to any event run a pass, as a change to another
// object of the collection does on a cluster. An event that does not
// fit in the watch's buffer is dropped. The caller holds the mutex.
func (a *cecAPI) send(watcher *fakeWatcher) {
	now := a.selectedIn(watcher.path, watcher.node)
	var events []string
	names := sortedKeys(now)
	slices.SortStableFunc(names, func(x, y string) int {
		return versionOf(now[x]) - versionOf(now[y])
	})
	for _, name := range names {
		kind := "MODIFIED"
		if _, sent := watcher.sent[name]; !sent {
			kind = "ADDED"
		}
		events = append(events, watchEvent(kind, now[name]))
	}
	for _, name := range sortedKeys(watcher.sent) {
		if _, exists := now[name]; !exists {
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

// fakeKinds maps each collection path to the apiVersion and kind of its
// objects.
var fakeKinds = map[string][2]string{
	cecBusesPath:    {equipmentAPIVersion, "CECBus"},
	televisionsPath: {equipmentAPIVersion, "Television"},
	receiversPath:   {equipmentAPIVersion, "Receiver"},
	displaysPath:    {"display.liken.sh/v1alpha1", "Display"},
}

// collection returns the objects of one collection by name, in the JSON
// form that a watch event carries. Each object's resourceVersion is
// the version at which its content last changed. The caller holds the
// mutex.
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
	case fakePodsPath:
		objects = a.podObjects()
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
		if a.versions == nil {
			a.versions = map[string]contentVersion{}
		}
		if a.versions[key].content != string(content) {
			a.versions[key] = contentVersion{content: string(content), version: fmt.Sprint(a.version)}
		}
		meta["resourceVersion"] = a.versions[key].version
		fields[name] = one
	}
	return fields
}

func versionOf(object map[string]any) int {
	meta, _ := object["metadata"].(map[string]any)
	version, _ := meta["resourceVersion"].(string)
	number, _ := strconv.Atoi(version)
	return number
}

// stored returns one object as the API server returns it, with its
// resourceVersion, or nil when the collection has no such object. The
// caller holds the mutex.
func (a *cecAPI) stored(path, name string) map[string]any {
	return a.collection(path)[name]
}

// serveStored responds to a read or a write of one object with the
// object's current state, or with 404 when it does not exist. The
// caller holds the mutex.
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

// bookmark returns a BOOKMARK event with the collection's version. When
// initialEventsEnd is true, it carries the k8s.io/initial-events-end
// annotation that ends the initial events of a streaming list. The
// caller holds the mutex.
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
	if a.servePodAPI(w, r) || a.serveTelevisionAPI(w, r) {
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
		a.serveStored(w, cecBusesPath, strings.TrimPrefix(path, cecBusesPath+"/"))
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
	display, found := a.displays[name]
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(display)
}

func (a *cecAPI) serveList(w http.ResponseWriter) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.serveCollection(w, cecBusesPath, "")
}

// serveCollection responds to a list as the API server does. Each item
// has its own resourceVersion and the list has the collection's, so a
// client can compare the versions it listed with the versions that a
// watch sends. The caller holds the mutex.
func (a *cecAPI) serveCollection(w http.ResponseWriter, path, node string) {
	selected := a.selectedIn(path, node)
	items := make([]map[string]any, 0, len(selected))
	for _, name := range sortedKeys(selected) {
		items = append(items, selected[name])
	}
	kind := fakeKinds[path]
	_ = json.NewEncoder(w).Encode(map[string]any{
		"apiVersion": kind[0],
		"kind":       kind[1] + "List",
		"metadata":   map[string]string{"resourceVersion": fmt.Sprint(a.version)},
		"items":      items,
	})
}

// serveWatch responds to a watch as the API server does. A streaming
// list, with sendInitialEvents=true, gets an ADDED event for each object
// and then a bookmark that ends the initial events. A watch from the
// current version gets the changes after it. A watch from any other
// version gets a 410, and the reflector lists the collection again.
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

// forget removes a watch whose connection has closed.
func (a *cecAPI) forget(watcher *fakeWatcher) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.watchers = slices.DeleteFunc(a.watchers, func(one *fakeWatcher) bool { return one == watcher })
}

// create responds to a POST as the API server does. A name that
// already exists gets a 409, and nothing changes.
func (a *cecAPI) create(w http.ResponseWriter, r *http.Request) {
	var body CECBus
	_ = json.NewDecoder(r.Body).Decode(&body)
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if _, exists := a.buses[body.Metadata.Name]; exists {
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
	bus, found := a.buses[name]
	if !found {
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
	holding, arrived, stoppedEntry := a.holdingEntries, a.heldEntry, a.stoppedEntry
	a.mutex.Unlock()
	stopped := len(body.Status.Adapters) == 1 && body.Status.Adapters[0].State == AdapterStopped
	if stopped && stoppedEntry != nil {
		select {
		case stoppedEntry <- struct{}{}:
		default:
		}
	}
	if holding != nil && strings.HasPrefix(manager, "equipment-operator-cec-") && !stopped {
		a.holds.Add(1)
		defer a.holds.Done()
		arrived <- struct{}{}
		<-holding
	}
	a.mutex.Lock()
	defer a.mutex.Unlock()
	bus, found := a.buses[name]
	if !found {
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

// copyBus returns a deep copy of a bus, made through JSON, so that a
// test reads a snapshot that the handlers cannot change.
func copyBus(bus *CECBus) CECBus {
	raw, _ := json.Marshal(bus)
	var copied CECBus
	_ = json.Unmarshal(raw, &copied)
	return copied
}

// putBus stores a bus as a user would create it, with generation 1
// when it sets none.
func (a *cecAPI) putBus(bus CECBus) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if bus.Metadata.Generation == 0 {
		bus.Metadata.Generation = 1
	}
	a.buses[bus.Metadata.Name] = &bus
	a.changed()
}

// writesOf returns the number of status writes from one machine's node
// workload.
func (a *cecAPI) writesOf(machine string) int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.entryWrites[machine]
}

// removeDisplay deletes a Display, so that a read of it returns 404.
func (a *cecAPI) removeDisplay(name string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	delete(a.displays, name)
	a.changed()
}

// nudge sends every watch its objects again with no change, so the
// loop under test runs a pass.
func (a *cecAPI) nudge() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.changed()
}

// moveDisplay changes a Display's physical address and sends the change
// only to the watches of Displays.
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

// bus returns a snapshot of one bus, and false when the bus does not
// exist.
func (a *cecAPI) bus(name string) (CECBus, bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	bus, found := a.buses[name]
	if !found {
		return CECBus{}, false
	}
	return copyBus(bus), true
}

func (a *cecAPI) deletedNames() []string {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return slices.Clone(a.deleted)
}

// entry returns one machine's entry in the status of one bus, and false
// when the bus or the entry does not exist.
func (a *cecAPI) entry(bus, machine string) (CECAdapterStatus, bool) {
	snapshot, found := a.bus(bus)
	if !found {
		return CECAdapterStatus{}, false
	}
	for _, entry := range snapshot.Status.Adapters {
		if entry.Machine == machine {
			return entry, true
		}
	}
	return CECAdapterStatus{}, false
}

// waitForEntry waits until ready returns true for one machine's entry
// in one bus. It fails the test, with the last entry it read, when that
// does not happen within testTimeout.
func (a *cecAPI) waitForEntry(t *testing.T, bus, machine string, ready func(CECAdapterStatus) bool) CECAdapterStatus {
	t.Helper()
	return a.waitForEntryWithin(t, bus, machine, testTimeout, ready)
}

// scanned waits until the machine's entry on the den bus is Scanned,
// then until every other goroutine in the synctest bubble is blocked,
// and returns the entry. By then the node workload has finished listing
// the collections it watches, so a change that the test makes next
// reaches it as a watch event and not during a list.
func (a *cecAPI) scanned(t *testing.T, machine string) CECAdapterStatus {
	t.Helper()
	entry := a.waitForEntry(t, "den", machine, func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	synctest.Wait()
	return entry
}

// vividScanTime is the longest that a test waits for a scan on the
// kernel's vivid driver. The driver sends each message at the speed of
// a real CEC bus, so a scan takes seconds.
const vividScanTime = 20 * time.Second

func (a *cecAPI) waitForEntryWithin(t *testing.T, bus, machine string, within time.Duration, ready func(CECAdapterStatus) bool) CECAdapterStatus {
	t.Helper()
	deadline := time.After(within)
	for {
		moved := a.nextMove()
		entry, found := a.entry(bus, machine)
		if found && ready(entry) {
			return entry
		}
		if !awaitMove(moved, deadline) {
			t.Fatalf("the CECBus %s has no wanted entry for %s within %v, the last entry is %+v (found %v)", bus, machine, within, entry, found)
		}
	}
}

// waitUntil polls ready every 5 milliseconds and fails the test with
// what when ready is still false after testTimeout.
func (a *cecAPI) waitUntil(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %v", what, testTimeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func writeEmpty(path string) error {
	return os.WriteFile(path, nil, 0o600)
}
