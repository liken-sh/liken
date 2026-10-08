package main

// These tests cover which screens a claim holds, as the API server
// answers it: a claim whose prepare has not finished holds its screen
// as firmly as one the kubelet prepared, and the Display pass reads
// the answer only before a compositor restart.

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// A claim on the lab machine's API server, allocated the devices it
// names from the driver and pool it names, and reserved for the pods
// it names.
type heldClaim struct {
	driver  string
	pool    string
	devices []string
	pods    []string
}

// claimServer is an API server that serves the claims of every
// namespace and the running pods it names. A pod in deleting is one
// the API server has begun to delete.
func claimServer(t *testing.T, claims []heldClaim, deleting map[string]bool, running ...string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ResourceClaimsPath {
			var items []map[string]any
			for _, one := range claims {
				var results, reserved []map[string]any
				for _, device := range one.devices {
					results = append(results, map[string]any{"request": "screen", "driver": one.driver, "pool": one.pool, "device": device})
				}
				for _, pod := range one.pods {
					reserved = append(reserved, map[string]any{"resource": podsResource, "name": pod})
				}
				items = append(items, map[string]any{
					"metadata": map[string]any{"name": "film", "namespace": "default"},
					"status": map[string]any{
						"allocation":  map[string]any{"devices": map[string]any{"results": results}},
						"reservedFor": reserved,
					},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
			return
		}
		name, isPod := strings.CutPrefix(r.URL.Path, "/api/v1/namespaces/default/pods/")
		if !isPod || !slices.Contains(running, name) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		pod := Pod{Metadata: PodMeta{Name: name, Namespace: "default"}}
		if deleting[name] {
			stamp := "2026-10-08T17:44:03Z"
			pod.Metadata.DeletionTimestamp = &stamp
		}
		_ = json.NewEncoder(w).Encode(pod)
	})
}

func TestAClaimHoldsItsScreenWhileALivePodHoldsTheClaim(t *testing.T) {
	cases := []struct {
		name     string
		claim    heldClaim
		deleting bool
		running  bool
		held     bool
	}{
		{
			// The pod exists whether or not its prepare has finished,
			// so the answer is the same while the kubelet retries it.
			name:    "an output a pod holds",
			claim:   heldClaim{driver: DriverName, pool: "stick-1", devices: []string{"hdmi-a-1"}, pods: []string{"film"}},
			running: true,
			held:    true,
		},
		{
			name:     "an output whose pod is being deleted",
			claim:    heldClaim{driver: DriverName, pool: "stick-1", devices: []string{"hdmi-a-1"}, pods: []string{"film"}},
			running:  true,
			deleting: true,
		},
		{
			name:  "an output whose pod is gone",
			claim: heldClaim{driver: DriverName, pool: "stick-1", devices: []string{"hdmi-a-1"}, pods: []string{"film"}},
		},
		{
			name:  "an output no pod holds",
			claim: heldClaim{driver: DriverName, pool: "stick-1", devices: []string{"hdmi-a-1"}},
		},
		{
			// Many claims share the draw device, and none of them owns
			// the mode.
			name:    "a draw device",
			claim:   heldClaim{driver: DriverName, pool: "stick-1", devices: []string{"hdmi-a-1-draw"}, pods: []string{"film"}},
			running: true,
		},
		{
			name:    "a control device",
			claim:   heldClaim{driver: DriverName, pool: "stick-1", devices: []string{"hdmi-a-1-control"}, pods: []string{"film"}},
			running: true,
		},
		{
			name:    "another node's output",
			claim:   heldClaim{driver: DriverName, pool: "liken-1", devices: []string{"hdmi-a-1"}, pods: []string{"film"}},
			running: true,
		},
		{
			name:    "another driver's device",
			claim:   heldClaim{driver: "audio.liken.sh", pool: "stick-1", devices: []string{"hdmi-a-1"}, pods: []string{"film"}},
			running: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var pods []string
			if c.running {
				pods = []string{"film"}
			}
			client := testClient(t, claimServer(t, []heldClaim{c.claim}, map[string]bool{"film": c.deleting}, pods...))

			held, err := allocatedOutputs(client, "stick-1", clusterStores{})

			if err != nil {
				t.Fatal(err)
			}
			if held["hdmi-a-1"] != c.held {
				t.Errorf("hdmi-a-1 held = %v, want %v (the answer was %v)", held["hdmi-a-1"], c.held, held)
			}
		})
	}
}

// The pass that finds the screen away from its resting mode leaves
// it alone while a claim holds it, even when the claim's prepare has
// not finished and no spec on disk names it. That prepare switched the
// screen to the claim's mode, and a pass that switched it back would
// make the kubelet's next retry switch it again.
func TestTheRestingModeWaitsForAClaimWhosePrepareHasNotFinished(t *testing.T) {
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     drillPanel(t, "lg-hdr-wqhd"),
		Modes:     []string{"1920x1080@60", "1280x720@60"},
		Current:   "1280x720@60",
	})
	fixture.allocated = map[string]bool{"hdmi-a-1": true}
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if len(fixture.modeSets) != 0 {
		t.Fatalf("the controller set %q while a claim held the screen", fixture.modeSets)
	}

	fixture.allocated = nil
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if want := "HDMI-A-1=1920x1080@60"; len(fixture.modeSets) != 1 || fixture.modeSets[0] != want {
		t.Errorf("the controller set %q after the claim ended, want %q", fixture.modeSets, want)
	}
}

// A pass that cannot read the claims cannot know the screen is free,
// so it switches nothing and reports why.
func TestAPassThatCannotReadTheClaimsSwitchesNothing(t *testing.T) {
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     drillPanel(t, "lg-hdr-wqhd"),
		Modes:     []string{"1920x1080@60", "1280x720@60"},
		Current:   "1280x720@60",
	})
	fixture.control.allocated = func() (map[string]bool, error) {
		return nil, errors.New("the API server refused the listing")
	}
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})

	err := fixture.pass()

	if err == nil || !strings.Contains(err.Error(), "refused the listing") {
		t.Errorf("the pass reported %v, want the failed listing", err)
	}
	if len(fixture.modeSets) != 0 {
		t.Errorf("the controller set %q without knowing the screen was free", fixture.modeSets)
	}
}

// The listing of every claim in the cluster runs only when a restart
// would follow, so a pass over a screen at its resting mode reads none.
func TestAPassThatRestartsNothingListsNoClaims(t *testing.T) {
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     drillPanel(t, "lg-hdr-wqhd"),
		Modes:     []string{"1920x1080@60", "1280x720@60"},
		Current:   "1920x1080@60",
	})
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})

	for range 3 {
		if err := fixture.pass(); err != nil {
			t.Fatal(err)
		}
	}

	if fixture.allocations != 0 {
		t.Errorf("the passes listed the claims %d times, want none", fixture.allocations)
	}
}

// The canvas heal waits on a claim whose prepare has not finished, for
// the reason the resting mode does.
func TestTheHealWaitsForAClaimWhosePrepareHasNotFinished(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	fixture.allocated = map[string]bool{"hdmi-a-1": true}
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	fixture.flap(t)
	fixture.advance(canvasSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if fixture.restarts != 0 {
		t.Fatalf("the compositor restarted %d times while a claim held a screen", fixture.restarts)
	}

	fixture.allocated = nil
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if fixture.restarts != 1 {
		t.Errorf("the compositor restarted %d times after the claim ended, want 1", fixture.restarts)
	}
}
