package main

// placementFixture fakes everything that the placement pass reads: a
// layout module on a real socket, an API server that stores Displays,
// Layouts, claims, and pods, and the CDI specs that a prepare writes to
// disk. A test sets up the compositor and the cluster, runs one pass,
// and then checks the lines that the module received and the status
// that the API server stored.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// The claim UIDs that the tests use. A surface id includes the first
// eight characters of its claim's UID, so these values are easy to
// recognize in an assertion.
const (
	idleClaimUID    = "idle-claim-0001"
	filmClaimUID    = "film-claim-0002"
	noticesClaimUID = "note-claim-0003"
	lotClaimUID     = "lot0-claim-0004"
)

// wiredScreen is one screen of the fixture: the connector and monitor
// that the card reports, and the logical size and scale that the
// module reports for the output.
type wiredScreen struct {
	Output Output
	Width  int
	Height int
	Scale  int
}

// primaryScreen is a 4K monitor at scale 2, which the compositor
// lays out at 1920 by 1080.
func primaryScreen() wiredScreen {
	return wiredScreen{Output: connectedOutput("HDMI-A-1", labMonitor()), Width: 1920, Height: 1080, Scale: 2}
}

// secondaryScreen is a second screen with a different logical size, so a
// test can check that each rectangle is computed from its own
// output's size.
func secondaryScreen() wiredScreen {
	return wiredScreen{Output: connectedOutput("HDMI-A-2", portableMonitor()), Width: 1280, Height: 800, Scale: 1}
}

type placementFixture struct {
	t      *testing.T
	module *fakeModule
	link   *layoutLink
	pass   *placementPass
	client *apiclient.Client
	wired  []wiredScreen

	displays map[string]*Display
	layouts  map[string]*Layout
	claims   map[string]*ResourceClaim
	pods     map[string]*Pod
	// The pods that the node's pod list leaves out. A request for one
	// of them by name still returns it.
	elsewhere map[string]bool
	// Counts of the requests that the API server received, and the
	// number of times it must still fail each path.
	claimLists   int
	podLists     int
	podGets      int
	displayLists int
	refuse       map[string]int
	// The method and path of every request that the API server received.
	requests []string
	// The Events that a recorder posts. The pass has no recorder until a
	// test sets one, as arrangers_test.go does.
	events eventstest.Events
	// The Event lines that posted() has already returned.
	seen []string

	ids     int
	version int
	read    int
}

func newPlacementFixture(t *testing.T, wired ...wiredScreen) *placementFixture {
	t.Helper()
	return newPlacementBench(t, nil, wired...)
}

// newPlacementBench is newPlacementFixture with a module that rejects
// the named requests, which simulates a compositor that refuses what
// the pass sends.
func newPlacementBench(t *testing.T, refuse map[string]string, wired ...wiredScreen) *placementFixture {
	t.Helper()
	if len(wired) == 0 {
		wired = []wiredScreen{primaryScreen()}
	}
	// The CDI specs record which claim owns which socket, so each
	// fixture needs its own spec directory.
	cdiDir = t.TempDir()

	var greeting []string
	for _, screen := range wired {
		greeting = append(greeting, fmt.Sprintf("output %s %d %d %d",
			screen.Output.Connector, screen.Width, screen.Height, screen.Scale))
	}
	fixture := &placementFixture{
		t:         t,
		wired:     wired,
		displays:  map[string]*Display{},
		layouts:   map[string]*Layout{},
		claims:    map[string]*ResourceClaim{},
		pods:      map[string]*Pod{},
		elsewhere: map[string]bool{},
		refuse:    map[string]int{},
	}
	fixture.module = newFakeModule(t, moduleScript{greeting: greeting, refuse: refuse})
	fixture.client = testClient(t, fixture.events.Around(fixture.handler()))
	fixture.link = servedLayoutLink(t, fixture.module)
	fixture.pass = newPlacementPass(fixture.client, "liken-1", filepath.Join(t.TempDir(), socketName),
		fixture.link, newClaimIndex(fixture.client), fixture.outputs)
	// Tests start with a compositor that replies to the probe. A test of
	// a compositor that does not reply sets its own probe result.
	fixture.serves()
	// The condition's transition time changes only when its status
	// changes. A fixed clock makes a pass that changes nothing produce
	// the same status, so it writes nothing.
	fixture.pass.now = func() time.Time { return time.Unix(0, 0).UTC() }
	fixture.read = len(fixture.module.read())
	return fixture
}

// serves makes the compositor reply to the probe.
func (f *placementFixture) serves() {
	f.probes(compositorLiveness{serving: true, reason: CompositorServingReason})
}

// probes sets the result of every later probe of the compositor's
// socket.
func (f *placementFixture) probes(live compositorLiveness) {
	f.pass.compositor = func() compositorLiveness { return live }
}

func (f *placementFixture) outputs() []Output {
	var outputs []Output
	for _, screen := range f.wired {
		outputs = append(outputs, screen.Output)
	}
	return outputs
}

// screen stores the Display of one monitor, as a person or the
// operator would have written it.
func (f *placementFixture) screen(monitor EDID, spec DisplaySpec) *Display {
	display := &Display{
		APIVersion: DisplayAPIVersion,
		Kind:       "Display",
		Metadata:   DisplayMeta{Name: monitorID(monitor)},
		Spec:       spec,
		Status:     DisplayStatus{Node: "liken-1"},
	}
	f.store(display)
	return display
}

func (f *placementFixture) store(display *Display) {
	f.version++
	display.Metadata.ResourceVersion = strconv.Itoa(f.version)
	f.displays[display.Metadata.Name] = display
}

// layout stores a Layout that the cluster owner wrote.
func (f *placementFixture) layout(name string, regions ...LayoutRegion) {
	f.layouts[name] = &Layout{
		APIVersion: DisplayAPIVersion,
		Kind:       "Layout",
		Metadata:   LayoutMeta{Name: name},
		Spec:       LayoutSpec{Regions: regions},
	}
}

// prepareClaim stores a claim that the kubelet prepared on one output and that
// the named pods prepareClaim. It returns the socket that the module opened
// for the claim, which is the socket that a test's surfaces connect on.
//
// A prepare leaves a CDI spec on disk and an entry in the operator's
// claim index. This function writes both. A test that wrote only the
// spec would simulate the operator container restarting while the
// compositor kept running.
func (f *placementFixture) prepareClaim(namespace, name, uid, connector string, pods ...string) string {
	f.t.Helper()
	socket := waylandSocketPrefix + uid
	device := deviceName(connector)
	edits := outputEdits(defaultSocketDir, socket, device)
	if err := writeCDISpec(uid, []cdiDevice{{Name: uid + "-" + device, ContainerEdits: edits}}); err != nil {
		f.t.Fatalf("writing the CDI spec for claim %s: %v", uid, err)
	}
	claim := &ResourceClaim{}
	claim.Metadata.Namespace, claim.Metadata.Name, claim.Metadata.UID = namespace, name, uid
	for _, pod := range pods {
		claim.Status.ReservedFor = append(claim.Status.ReservedFor,
			ClaimConsumer{Resource: podsResource, Name: pod, UID: pod + "-uid"})
	}
	f.claims[namespace+"/"+name] = claim
	f.pass.claims.remember(uid, namespace, name)
	return socket
}

// pod stores a pod with the labels that a region's selector matches.
func (f *placementFixture) pod(namespace, name string, labels map[string]string) {
	f.pods[namespace+"/"+name] = &Pod{
		Metadata: PodMeta{Name: name, Namespace: namespace, Labels: labels},
	}
}

// surface makes the module report one surface and returns its id.
// The pass reads the surface on its next run. The ids increase the way
// the module's ids do, so they also give the order in which the
// surfaces arrived.
func (f *placementFixture) surface(socket string, width, height int) int {
	f.t.Helper()
	f.ids++
	id := f.ids
	f.report(id, socket, width, height)
	return id
}

// report sends the surface line for a given id. A module that
// reconnects reports the same ids again.
func (f *placementFixture) report(id int, socket string, width, height int) {
	f.t.Helper()
	f.module.send(fmt.Sprintf("surface %d %s %d %d", id, socket, width, height))
	waitUntil(f.t, fmt.Sprintf("surface %d arrives", id), func() bool {
		_, held := f.link.state().Surfaces[id]
		return held
	})
}

// disconnect disconnects one monitor. The card then reports the connector
// with no monitor, and the module reports that the compositor destroyed
// the output. The surfaces on the output remain. The module hides
// each one and clears its placement, and the client stays connected.
func (f *placementFixture) disconnect(connector string) {
	f.t.Helper()
	for i, screen := range f.wired {
		if screen.Output.Connector == connector {
			f.wired[i].Output = Output{Connector: connector}
		}
	}
	f.module.send(layoutOutputGoneEvent + " " + connector)
	waitUntil(f.t, connector+" leaves the store", func() bool {
		_, held := f.link.state().Outputs[connector]
		return !held
	})
}

// reconnect reconnects a monitor, which makes the compositor create its
// output again.
func (f *placementFixture) reconnect(screen wiredScreen) {
	f.t.Helper()
	connector := screen.Output.Connector
	for i, wired := range f.wired {
		if wired.Output.Connector == connector {
			f.wired[i] = screen
		}
	}
	f.module.send(fmt.Sprintf("%s %s %d %d %d", layoutOutputEvent,
		connector, screen.Width, screen.Height, screen.Scale))
	waitUntil(f.t, connector+" returns to the store", func() bool {
		_, held := f.link.state().Outputs[connector]
		return held
	})
}

// sent returns the lines that the module has read since the last call,
// so a test sees the lines of one pass at a time. It leaves out the
// hello line that each connection starts with, because the link sends
// that line on its own. A test that counts connections counts them on
// the module.
func (f *placementFixture) sent() []string {
	read := f.module.read()
	var fresh []string
	for _, line := range read[f.read:] {
		if strings.HasPrefix(line, layoutHelloEvent+" ") {
			continue
		}
		fresh = append(fresh, line)
	}
	f.read = len(read)
	return fresh
}

func (f *placementFixture) run() {
	f.t.Helper()
	if err := f.pass.pass(); err != nil {
		f.t.Fatalf("running the placement pass: %v", err)
	}
}

func (f *placementFixture) status(monitor EDID) DisplayStatus {
	f.t.Helper()
	display, held := f.displays[monitorID(monitor)]
	if !held {
		f.t.Fatalf("the fixture has no Display for %s, and holds %v", monitorID(monitor), f.names())
	}
	return display.Status
}

func (f *placementFixture) names() []string {
	var names []string
	for name := range f.displays {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// assertSent checks that one pass sent exactly the wanted protocol
// lines, in order.
func assertSent(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("the module read %q, want %q", got, want)
	}
	for i, line := range want {
		if got[i] != line {
			t.Errorf("line %d = %q, want %q", i, got[i], line)
		}
	}
}

func (f *placementFixture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		if f.refuse[r.URL.Path] > 0 {
			f.refuse[r.URL.Path]--
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch {
		case r.URL.Path == DisplaysPath:
			f.displayLists++
			list := DisplayList{}
			for _, display := range f.displays {
				list.Items = append(list.Items, *display)
			}
			f.serve(w, &list)
		case strings.HasPrefix(r.URL.Path, DisplaysPath):
			f.serveDisplay(w, r)
		case strings.HasPrefix(r.URL.Path, LayoutsPath+"/"):
			f.serve(w, f.layouts[strings.TrimPrefix(r.URL.Path, LayoutsPath+"/")])
		case r.URL.Path == ResourceClaimsPath:
			f.claimLists++
			list := ResourceClaimList{}
			for _, claim := range f.claims {
				list.Items = append(list.Items, *claim)
			}
			f.serve(w, &list)
		case strings.Contains(r.URL.Path, "/resourceclaims/"):
			f.serve(w, f.claims[namespacedName(r.URL.Path, "resourceclaims")])
		case r.URL.Path == PodsPath:
			f.servePods(w, r)
		case strings.Contains(r.URL.Path, "/pods/"):
			f.podGets++
			f.serve(w, f.pods[namespacedName(r.URL.Path, "pods")])
		default:
			f.t.Errorf("the fake API server received %s %s, which it does not serve", r.Method, r.URL.Path)
		}
	})
}

// namespacedName extracts the namespace and name from a namespaced
// path, as the "namespace/name" key that the fixture stores objects
// under.
func namespacedName(path, resource string) string {
	fields := strings.Split(path, "/")
	for i, field := range fields {
		if field == resource && i > 1 && i+1 < len(fields) {
			return fields[i-1] + "/" + fields[i+1]
		}
	}
	return ""
}

func (f *placementFixture) servePods(w http.ResponseWriter, r *http.Request) {
	f.podLists++
	if selector := r.URL.Query().Get("fieldSelector"); selector != "spec.nodeName=liken-1" {
		f.t.Errorf("the pass listed pods with %q, want spec.nodeName=liken-1", selector)
	}
	list := PodList{}
	for key, pod := range f.pods {
		if f.elsewhere[key] {
			continue
		}
		list.Items = append(list.Items, *pod)
	}
	f.serve(w, &list)
}

func (f *placementFixture) serveDisplay(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, DisplaysPath+"/")
	if r.Method == http.MethodPut && strings.HasSuffix(name, "/status") {
		written := &Display{}
		_ = json.NewDecoder(r.Body).Decode(written)
		held, stored := f.displays[strings.TrimSuffix(name, "/status")]
		if !stored {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// A write based on an older resourceVersion than the stored
		// object fails with a conflict, as in the real API server.
		if version := written.Metadata.ResourceVersion; version != "" && version != held.Metadata.ResourceVersion {
			w.WriteHeader(http.StatusConflict)
			return
		}
		held.Status = written.Status
		f.store(held)
		f.serve(w, held)
		return
	}
	f.serve(w, f.displays[name])
}

// serve writes an object as JSON, or a 404 when the object is a nil
// pointer. A nil Layout is the case of a Display that names a Layout
// that does not exist.
func (f *placementFixture) serve(w http.ResponseWriter, object any) {
	switch held := object.(type) {
	case *Display:
		if held == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
	case *Layout:
		if held == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
	case *ResourceClaim:
		if held == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
	case *Pod:
		if held == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}
	_ = json.NewEncoder(w).Encode(object)
}

// conditionOf returns the LayoutResolved condition.
func conditionOf(status DisplayStatus) DisplayCondition {
	return conditionByType(status, LayoutResolvedCondition)
}

// conditionByType returns one condition of a status, and an empty one
// for a condition the status does not carry.
func conditionByType(status DisplayStatus, kind string) DisplayCondition {
	for _, condition := range status.Conditions {
		if condition.Type == kind {
			return condition
		}
	}
	return DisplayCondition{}
}

func assertCondition(t *testing.T, status DisplayStatus, want conditions.Status, reason string) {
	t.Helper()
	condition := conditionOf(status)
	if condition.Status != want || condition.Reason != reason {
		t.Errorf("%s is %s/%s, want %s/%s", LayoutResolvedCondition,
			condition.Status, condition.Reason, want, reason)
	}
}
