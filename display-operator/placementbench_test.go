package main

// One fixture holds every party the placement pass reads: the layout
// module on a real socket, an API server that stores the Displays,
// Layouts, claims and pods, and the CDI specs a prepare left on disk.
// A test states what the compositor holds and what the cluster
// declares, runs one pass, and reads the lines the module received
// and the status the API server stored.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The claims of the drills below. A surface id carries the first
// eight characters of its claim's UID, so these read in an
// assertion.
const (
	idleClaimUID    = "idle-claim-0001"
	filmClaimUID    = "film-claim-0002"
	noticesClaimUID = "note-claim-0003"
	lotClaimUID     = "lot0-claim-0004"
)

// One screen on the bench: the connector and monitor the card
// reports, and the logical size and scale the module reports for it.
type wiredScreen struct {
	Output Output
	Width  int
	Height int
	Scale  int
}

// The living-room panel of plan 15: 4K at scale 2, which the
// compositor lays out at 1920 by 1080.
func livingRoomScreen() wiredScreen {
	return wiredScreen{Output: litOutput("HDMI-A-1", labMonitor()), Width: 1920, Height: 1080, Scale: 2}
}

// The second screen of the bench, at its own logical size, so a test
// proves each rectangle is computed against its own output.
func deskScreen() wiredScreen {
	return wiredScreen{Output: litOutput("HDMI-A-2", portableMonitor()), Width: 1280, Height: 800, Scale: 1}
}

type placementFixture struct {
	t      *testing.T
	module *fakeModule
	link   *layoutLink
	pass   *placementPass
	client *Client
	wired  []wiredScreen

	displays map[string]*Display
	layouts  map[string]*Layout
	claims   map[string]*ResourceClaim
	pods     map[string]*Pod
	// The pods the node listing leaves out, which is what the read of
	// one holder by name answers for.
	elsewhere map[string]bool
	// What the API server was asked for, and what it refuses.
	claimLists int
	podLists   int
	podGets    int
	refuse     map[string]int

	ids     int
	version int
	read    int
}

func newPlacementFixture(t *testing.T, wired ...wiredScreen) *placementFixture {
	t.Helper()
	return newPlacementBench(t, nil, wired...)
}

// The same bench with a module that refuses the named requests, which
// is a compositor that will not take what the pass states.
func newPlacementBench(t *testing.T, refuse map[string]string, wired ...wiredScreen) *placementFixture {
	t.Helper()
	if len(wired) == 0 {
		wired = []wiredScreen{livingRoomScreen()}
	}
	// The specs a prepare leaves are the record of which claim holds
	// which socket, so every fixture owns its own directory of them.
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
	fixture.client = testClient(t, fixture.handler())
	fixture.link = servedLayoutLink(t, fixture.module)
	fixture.pass = newPlacementPass(fixture.client, "liken-1",
		fixture.link, newClaimIndex(fixture.client), fixture.outputs)
	// The condition's timestamp moves only when the condition does, so
	// the clock stands still and a pass that changes nothing writes
	// nothing.
	fixture.pass.now = func() time.Time { return time.Unix(0, 0).UTC() }
	fixture.read = len(fixture.module.read())
	return fixture
}

func (f *placementFixture) outputs() []Output {
	var outputs []Output
	for _, screen := range f.wired {
		outputs = append(outputs, screen.Output)
	}
	return outputs
}

// The resource of one screen, as a person or a machine writer wrote
// it.
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

// A Layout the cluster owner wrote.
func (f *placementFixture) layout(name string, regions ...LayoutRegion) {
	f.layouts[name] = &Layout{
		APIVersion: DisplayAPIVersion,
		Kind:       "Layout",
		Metadata:   LayoutMeta{Name: name},
		Spec:       LayoutSpec{Regions: regions},
	}
}

// A claim the kubelet prepared on one output, held by the named
// pods. The answer is the socket the module opened for it, which is
// the socket a test's surface arrives on.
//
// The spec on disk and the index in this process are both what a
// prepare leaves behind, so a fixture that skips the index drives the
// operator's container restarting under a compositor that kept
// running.
func (f *placementFixture) hold(namespace, name, uid, connector string, pods ...string) string {
	f.t.Helper()
	socket := waylandSocketPrefix + uid
	device := deviceName(connector)
	edits := outputEdits(defaultSocketDir, socket, device)
	if err := writeCDISpec(uid, []cdiDevice{{Name: uid + "-" + device, ContainerEdits: edits}}); err != nil {
		f.t.Fatal(err)
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

// One pod holding a claim, with the labels a region's selector reads.
func (f *placementFixture) pod(namespace, name string, labels map[string]string) {
	f.pods[namespace+"/"+name] = &Pod{
		Metadata: PodMeta{Name: name, Namespace: namespace, Labels: labels},
	}
}

// The module reports one surface, and the pass reads it on its next
// look. The ids count up the way the module's own do, so the id is
// also the order the surfaces arrived in.
func (f *placementFixture) surface(socket string, width, height int) int {
	f.t.Helper()
	f.ids++
	id := f.ids
	f.report(id, socket, width, height)
	return id
}

// The same surface line, for the ids a reconnected module reports
// again.
func (f *placementFixture) report(id int, socket string, width, height int) {
	f.t.Helper()
	f.module.send(fmt.Sprintf("surface %d %s %d %d", id, socket, width, height))
	waitUntil(f.t, fmt.Sprintf("surface %d arrives", id), func() bool {
		_, held := f.link.state().Surfaces[id]
		return held
	})
}

// The lines the module read since a test last asked, so each pass is
// read on its own batch. The hello of each connection is not one of
// them: it is the link's own, and a test that counts connections
// counts them on the module.
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
		f.t.Fatalf("the pass failed: %v", err)
	}
}

func (f *placementFixture) status(monitor EDID) DisplayStatus {
	f.t.Helper()
	display, held := f.displays[monitorID(monitor)]
	if !held {
		f.t.Fatalf("no Display for %s; the fixture holds %v", monitorID(monitor), f.names())
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

// assertSent compares one pass's batch with the lines the protocol
// states for it, in order.
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
		if f.refuse[r.URL.Path] > 0 {
			f.refuse[r.URL.Path]--
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch {
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
			f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

// The namespace and name at the end of a namespaced path, in the form
// this fixture keys its objects by.
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
		held.Status = written.Status
		f.store(held)
		f.serve(w, held)
		return
	}
	f.serve(w, f.displays[name])
}

// One object, or the answer the API server gives for an object that
// is not there. A nil pointer of any type reads as absent here, which
// is a Layout a Display names and no one wrote.
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

// The condition of the pass, which is the only report of a name that
// resolved to nothing.
func conditionOf(status DisplayStatus) DisplayCondition {
	for _, condition := range status.Conditions {
		if condition.Type == LayoutResolvedCondition {
			return condition
		}
	}
	return DisplayCondition{}
}

func assertCondition(t *testing.T, status DisplayStatus, want, reason string) {
	t.Helper()
	condition := conditionOf(status)
	if condition.Status != want || condition.Reason != reason {
		t.Errorf("%s is %s/%s, want %s/%s", LayoutResolvedCondition,
			condition.Status, condition.Reason, want, reason)
	}
}
