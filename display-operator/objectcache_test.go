package main

import (
	"net/http"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// storeHolding returns a ready store with a copy of each object. A
// later write to the fixture leaves the store's copy older than the API
// server's.
func storeHolding[T any](t *testing.T, objects ...T) informer.View {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for index := range objects {
		fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&objects[index])
		if err != nil {
			t.Fatalf("converting the object to unstructured: %v", err)
		}
		if err := store.Add(&unstructured.Unstructured{Object: fields}); err != nil {
			t.Fatalf("adding the object to the store: %v", err)
		}
	}
	return informer.View{Store: store, Synced: func() bool { return true }}
}

func displaysOf(held map[string]*Display) []Display {
	var displays []Display
	for _, display := range held {
		displays = append(displays, *display)
	}
	return displays
}

func valuesOf[T any](held map[string]*T) []T {
	var items []T
	for _, item := range held {
		items = append(items, *item)
	}
	return items
}

func reads(requests []string) []string {
	var got []string
	for _, request := range requests {
		if strings.HasPrefix(request, "GET ") {
			got = append(got, request)
		}
	}
	return got
}

// A settled Display controller pass reads each panel's Display, and a
// pass that sweeps lists them too. With the store ready, it sends no
// read to the API server.
func TestADisplayPassFromTheStoreSendsNoRead(t *testing.T) {
	for _, c := range []struct {
		name   string
		cached bool
		sweep  bool
		want   int
	}{
		{"the API server, a settled pass", false, false, 1},
		{"the store, a settled pass", true, false, 0},
		{"the API server, a pass that sweeps", false, true, 2},
		{"the store, a pass that sweeps", true, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
			if err := fixture.pass(); err != nil {
				t.Fatalf("running the pass: %v", err)
			}
			if c.cached {
				fixture.control.displays = newDisplayStore(fixture.client, storeHolding(t, displaysOf(fixture.displays)...))
			}
			if c.sweep {
				fixture.advance(backstopInterval)
			}
			fixture.requests = nil

			if err := fixture.pass(); err != nil {
				t.Fatalf("running the pass: %v", err)
			}
			if got := reads(fixture.requests); len(got) != c.want {
				t.Errorf("the pass sent %v, want %d reads", got, c.want)
			}
		})
	}
}

// A settled placement pass reads the screen's Display, the Layout it
// references, the claim behind the surface, and this node's pods. With
// the stores ready, it reads only the claim from the API server,
// because no watch stores claims.
func TestAPlacementPassFromTheStoresReadsOnlyTheClaim(t *testing.T) {
	for _, c := range []struct {
		name   string
		cached bool
		want   int
	}{
		{"the API server", false, 4},
		{"the stores", true, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture := newPlacementFixture(t)
			fixture.layout("den", LayoutRegion{Name: "whole"})
			fixture.screen(labMonitor(), DisplaySpec{Layout: "den"})
			film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
			fixture.pod("media", "film-0", map[string]string{"media.liken.sh/focus": "true"})
			fixture.surface(film, 1920, 1080)
			fixture.run()
			if c.cached {
				fixture.pass.displays = newDisplayStore(fixture.client, storeHolding(t, displaysOf(fixture.displays)...))
				fixture.pass.stores = clusterStores{
					layouts: storeHolding(t, valuesOf(fixture.layouts)...),
					pods:    storeHolding(t, valuesOf(fixture.pods)...),
				}
			}
			fixture.requests = nil

			fixture.run()

			if got := reads(fixture.requests); len(got) != c.want {
				t.Errorf("the pass sent %v, want %d reads", got, c.want)
			}
		})
	}
}

// The store can still have the Display from before the operator
// captured the panel's brightness. The memo has the version of the
// operator's own write, so the second pass reads the Display from the
// API server. It keeps the saved brightness, not the dark panel's, and
// does not write the panel again.
func TestAPassDoesNotActOnACopyOlderThanItsOwnWrite(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})
	fixture.control.displays = newDisplayStore(fixture.client, storeHolding(t, displaysOf(fixture.displays)...))
	if err := fixture.pass(); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	writes := len(fixture.panel.writesTo(vcpBrightness))

	if err := fixture.pass(); err != nil {
		t.Fatalf("running the pass: %v", err)
	}

	brightness := fixture.display().Status.Captured.Brightness
	if brightness == nil {
		t.Fatal("no brightness is captured, want the 50 the first pass saved")
	}
	if *brightness != 50 {
		t.Errorf("captured brightness = %d, want the 50 the first pass saved", *brightness)
	}
	if got := len(fixture.panel.writesTo(vcpBrightness)); got != writes {
		t.Errorf("the second pass wrote the brightness %d more times, want none", got-writes)
	}
}

// A list before the Display watch finishes its first read comes from
// the API server. The store can then become ready with an older copy,
// because the watch's first read can come from the API server's watch
// cache. The next list must not return that older copy. Otherwise a
// Display controller that saw a person's override in the first list
// would read the copy from before the override and revert the panel.
func TestAListAfterTheStoreBecomesReadyKeepsTheNewerCopy(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	fixture.declare(DisplaySpec{})
	older := storeHolding(t, displaysOf(fixture.displays)...)
	fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})
	ready := false
	displays := newDisplayStore(fixture.client, informer.View{Store: older.Store, Synced: func() bool { return ready }})

	listed, err := displays.list()
	if err != nil || len(listed) != 1 || listed[0].Spec.Override == nil {
		t.Fatalf("the list before the store is ready = %+v, %v; want the override", listed, err)
	}
	ready = true
	read, err := displays.list()

	if err != nil || len(read) != 1 || read[0].Spec.Override == nil {
		t.Errorf("the list from the ready store = %+v, %v; want the override, not the store's older copy", read, err)
	}
}

// A Layout that a ready store does not have does not exist, so the
// pass reports LayoutNotFound with no read of the API server.
func TestALayoutMissingFromAReadyStoreIsNotRead(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{Layout: "missing"})
	fixture.pass.stores = clusterStores{layouts: storeHolding[Layout](t)}

	fixture.run()

	for _, request := range fixture.requests {
		if strings.HasPrefix(request, "GET "+LayoutsPath) {
			t.Errorf("the pass sent %s, want no read of Layouts from the API server", request)
		}
	}
	assertCondition(t, fixture.status(labMonitor()), conditionFalse, LayoutNotFoundReason)
}

// A store can become ready while a pass reads a Layout from it. The
// read goes to the API server, which has the Layout, and never reports
// ErrNotFound for a Layout that the store has.
func TestALayoutReadWhileTheStoreBecomesReadyIsFound(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("den", LayoutRegion{Name: "whole"})
	becomesReady := storeHolding(t, valuesOf(fixture.layouts)...)
	checks := 0
	becomesReady.Synced = func() bool {
		checks++
		return checks > 1
	}
	stores := clusterStores{layouts: becomesReady}

	layout, err := stores.layout(fixture.client, "den")

	if err != nil || layout.Metadata.Name != "den" {
		t.Errorf("layout(den) = %v, %v, want the Layout den", layout, err)
	}
}

// The compositor's restart count comes from this operator's own pod,
// which is in the store of this node's pods, so a pass that publishes
// the slice reads no pod from the API server.
func TestTheRestartCountReadsThePodFromTheStore(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the reader sent %s to the API server, want no request", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	restarts := newWestonRestarts(client, "liken-system", "display-operator-abcde")
	own := Pod{
		Metadata: PodMeta{Name: "display-operator-abcde", Namespace: "liken-system"},
		Status:   PodStatus{InitContainerStatuses: []ContainerStatus{{Name: compositorMode, RestartCount: 2}}},
	}
	restarts.pods = clusterStores{pods: storeHolding(t, own)}

	if _, err := restarts.growth(); err != nil {
		t.Fatalf("reading the restart count: %v", err)
	}
	if restarts.seen != 2 {
		t.Errorf("the reader read a count of %d, want the 2 the pod in the store holds", restarts.seen)
	}
}
