package main

// A fake API server for the CECBus tests. It holds CECBus and Display
// objects and applies each write the way server-side apply does for
// the fields these writers state: a node workload's apply replaces its
// own machine's entry under status.adapters, and the Deployment's
// apply replaces the devices and the conditions. A watch gets one
// event for each change, so a loop under test wakes the way it wakes
// on a cluster.

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
	mutex    sync.Mutex
	buses    map[string]*CECBus
	displays map[string]*Display
	version  int
	watchers []chan string
	deleted  []string
	client   *Client
	// refusing makes every list and every status write fail with a
	// 500, the way an API server answers while it is unhealthy.
	refusing bool
	// entryWrites counts the status writes of each node workload's
	// field manager.
	entryWrites map[string]int
}

// refuse turns the refusals on or off.
func (a *cecAPI) refuse(refusing bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.refusing = refusing
}

func startCECAPI(t *testing.T) *cecAPI {
	t.Helper()
	api := &cecAPI{buses: map[string]*CECBus{}, displays: map[string]*Display{}}
	api.client = testAPIClient(t, http.HandlerFunc(api.handle))
	return api
}

// changed bumps the collection's version and wakes every watch. The
// caller holds the mutex.
func (a *cecAPI) changed() {
	a.version++
	event := fmt.Sprintf(`{"type":"MODIFIED","object":{"metadata":{"resourceVersion":"%d"}}}`, a.version)
	for _, watcher := range a.watchers {
		select {
		case watcher <- event:
		default:
		}
	}
}

func (a *cecAPI) handle(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	a.mutex.Lock()
	refusing := a.refusing
	a.mutex.Unlock()
	watch := r.URL.Query().Get("watch") == "true"
	if refusing && !watch && (r.Method == http.MethodGet && path == cecBusesPath || strings.HasSuffix(path, "/status")) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	switch {
	case strings.HasPrefix(path, displaysPath+"/"):
		a.serveDisplay(w, strings.TrimPrefix(path, displaysPath+"/"))
	case path == cecBusesPath && r.URL.Query().Get("watch") == "true":
		a.serveWatch(w, r)
	case path == cecBusesPath:
		a.serveList(w)
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

func (a *cecAPI) serveWatch(w http.ResponseWriter, r *http.Request) {
	events := make(chan string, 16)
	a.mutex.Lock()
	a.watchers = append(a.watchers, events)
	// A watch resumes from the version its list gave it, as on a real
	// API server, so a change made between the list and the watch still
	// reaches the loop.
	if from, err := strconv.Atoi(r.URL.Query().Get("resourceVersion")); err == nil && from < a.version {
		events <- fmt.Sprintf(`{"type":"MODIFIED","object":{"metadata":{"resourceVersion":"%d"}}}`, a.version)
	}
	a.mutex.Unlock()
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-events:
			_, _ = io.WriteString(w, event+"\n")
			w.(http.Flusher).Flush()
		}
	}
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
	_ = json.NewEncoder(w).Encode(bus)
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
		if a.entryWrites == nil {
			a.entryWrites = map[string]int{}
		}
		a.entryWrites[machine]++
		bus.Status.Adapters = slices.DeleteFunc(bus.Status.Adapters, func(entry CECAdapterStatus) bool { return entry.Machine == machine })
		bus.Status.Adapters = append(bus.Status.Adapters, body.Status.Adapters...)
	} else {
		bus.Status.Devices = body.Status.Devices
		bus.Status.Conditions = body.Status.Conditions
	}
	a.changed()
	_ = json.NewEncoder(w).Encode(bus)
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
}

// nudge wakes every watch with no change, the way an unrelated edit
// does, so the loop under test runs a pass.
func (a *cecAPI) nudge() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.changed()
}

func (a *cecAPI) putDisplay(name, node, physicalAddress string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	display := &Display{Metadata: ObjectMeta{Name: name}}
	display.Status.Node = node
	display.Status.PhysicalAddress = physicalAddress
	a.displays[name] = display
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
