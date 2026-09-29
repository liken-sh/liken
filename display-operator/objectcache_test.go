package main

// These tests count the requests each pass sends when it reads the
// Displays, the Layouts, and this node's pods from the watches'
// stores, and cover a Display whose copy in the store is older than
// this operator's own write.

import (
	"net/http"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// storeHolding is a watch's store that holds copies of the objects
// given, as a watch holds them once it has delivered every write. A
// later write to the fixture leaves the store older than the API
// server.
func storeHolding[T any](t *testing.T, objects ...T) informer.View {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for index := range objects {
		fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&objects[index])
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Add(&unstructured.Unstructured{Object: fields}); err != nil {
			t.Fatal(err)
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

// reads answers the GET requests among the requests given, which
// covers a read of one object and a list alike.
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
// pass that sweeps lists them too. From the store it reads nothing.
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
			fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
			if err := fixture.pass(); err != nil {
				t.Fatal(err)
			}
			if c.cached {
				fixture.control.displays = newDisplayStore(fixture.client, storeHolding(t, displaysOf(fixture.displays)...))
			}
			if c.sweep {
				fixture.advance(backstopInterval)
			}
			fixture.requests = nil

			if err := fixture.pass(); err != nil {
				t.Fatal(err)
			}
			if got := reads(fixture.requests); len(got) != c.want {
				t.Errorf("the pass sent %v, want %d reads", got, c.want)
			}
		})
	}
}

// A settled placement pass reads the screen's Display, the Layout it
// names, the claim behind the surface, and this node's pods. From the
// stores it reads the claim alone, because no watch holds the claims.
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
			film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
			fixture.pod("living-room", "film-0", map[string]string{"media.liken.sh/focus": "true"})
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

// The store can still hold a Display as it was before this operator
// captured the panel's brightness. The operator remembers the version
// its own write produced, reads the Display from the API server
// instead of the store's older copy, and neither captures the dark
// panel's brightness over the saved one nor writes the panel again.
func TestAPassDoesNotActOnACopyOlderThanItsOwnWrite(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})
	fixture.control.displays = newDisplayStore(fixture.client, storeHolding(t, displaysOf(fixture.displays)...))
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	writes := len(fixture.panel.took(vcpBrightness))

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	brightness := fixture.display().Status.Captured.Brightness
	if brightness == nil {
		t.Fatal("no brightness is captured, want the 50 the first pass saved")
	}
	if *brightness != 50 {
		t.Errorf("captured brightness = %d, want the 50 the first pass saved", *brightness)
	}
	if got := len(fixture.panel.took(vcpBrightness)); got != writes {
		t.Errorf("the second pass wrote the brightness %d more times, want none", got-writes)
	}
}

// A Layout the store does not hold is a Layout that does not exist,
// once the store holds its first read, so the screen is drawn to the
// default with no read of the API server.
func TestALayoutTheStoreDoesNotHoldIsNotRead(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{Layout: "missing"})
	fixture.pass.stores = clusterStores{layouts: storeHolding[Layout](t)}

	fixture.run()

	for _, request := range fixture.requests {
		if strings.HasPrefix(request, "GET "+LayoutsPath) {
			t.Errorf("the pass read %s", request)
		}
	}
	assertCondition(t, fixture.status(labMonitor()), conditionFalse, LayoutNotFoundReason)
}

// A store can become ready while a pass reads a Layout from it. The
// read goes to the API server, which holds the Layout, and never
// answers that a Layout the store holds does not exist.
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
// which the store of this node's pods holds, so a pass that publishes
// the slice reads no pod from the API server.
func TestTheRestartCountReadsThePodFromTheStore(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the reader asked the API server for %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	restarts := newWestonRestarts(client, "liken-system", "display-operator-abcde")
	own := Pod{
		Metadata: PodMeta{Name: "display-operator-abcde", Namespace: "liken-system"},
		Status:   PodStatus{InitContainerStatuses: []ContainerStatus{{Name: compositorMode, RestartCount: 2}}},
	}
	restarts.pods = clusterStores{pods: storeHolding(t, own)}

	if _, err := restarts.growth(); err != nil {
		t.Fatal(err)
	}
	if restarts.seen != 2 {
		t.Errorf("the reader read a count of %d, want the 2 the pod in the store holds", restarts.seen)
	}
}
