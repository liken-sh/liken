package main

// These tests cover which screens a claim holds, as the API server
// reports it. A claim whose prepare has not finished holds its screen
// as much as one the kubelet prepared, and the Display pass lists the
// claims only before a compositor restart.

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// heldClaim is a ResourceClaim allocated devices from a driver and
// pool, and reserved for the listed pods.
type heldClaim struct {
	driver  string
	pool    string
	devices []string
	pods    []string
}

// claimServer serves the given claims in the claims list, and a pod for
// each name in running. A pod whose name is true in deleting has a
// deletion timestamp.
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
			// The pod exists whether or not the prepare has finished, so
			// the result is the same while the kubelet retries.
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
			// Many claims share a draw device, and none of them owns the
			// mode.
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
				t.Fatalf("reading the allocated outputs of node stick-1: %v", err)
			}
			if held["hdmi-a-1"] != c.held {
				t.Errorf("hdmi-a-1 is held: %v, want %v (the outputs are %v)", held["hdmi-a-1"], c.held, held)
			}
		})
	}
}

// While a claim holds a screen that is not at its declared mode, the
// pass leaves the mode alone, even when the claim's prepare has not
// finished and no spec on disk lists it. The prepare already switched
// the screen to the claim's mode, and switching it back would make the
// kubelet's next retry switch it again.
func TestTheDeclaredModeIsNotRestoredWhileAnUnpreparedClaimHoldsTheScreen(t *testing.T) {
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     recordedPanel(t, "lg-hdr-wqhd"),
		Modes:     []string{"1920x1080@60", "1280x720@60"},
		Current:   "1280x720@60",
	})
	fixture.allocated = map[string]bool{"hdmi-a-1": true}
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})

	if err := fixture.pass(); err != nil {
		t.Fatalf("running the first pass while a claim holds the screen: %v", err)
	}
	if len(fixture.modeSets) != 0 {
		t.Fatalf("the controller set %q while a claim held the screen", fixture.modeSets)
	}

	fixture.allocated = nil
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass after the claim ended: %v", err)
	}
	if want := "HDMI-A-1=1920x1080@60"; len(fixture.modeSets) != 1 || fixture.modeSets[0] != want {
		t.Errorf("the controller set %q after the claim ended, want %q", fixture.modeSets, want)
	}
}

// When the pass cannot list the claims, it does not know whether the
// screen is free. It changes no mode and returns the error.
func TestAPassThatCannotReadTheClaimsSwitchesNothing(t *testing.T) {
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     recordedPanel(t, "lg-hdr-wqhd"),
		Modes:     []string{"1920x1080@60", "1280x720@60"},
		Current:   "1280x720@60",
	})
	errClaimListing := errors.New("the API server refused the listing")
	fixture.control.allocated = func() (map[string]bool, error) {
		return nil, errClaimListing
	}
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})

	err := fixture.pass()

	if !errors.Is(err, errClaimListing) {
		t.Errorf("the pass returned %v, want the error of the claim listing", err)
	}
	if len(fixture.modeSets) != 0 {
		t.Errorf("the controller set %q when it could not read whether a claim holds the screen", fixture.modeSets)
	}
}

// The pass lists every claim in the cluster only when a restart would
// follow, so it lists none while the screen is at its declared mode.
func TestAPassThatRestartsNothingListsNoClaims(t *testing.T) {
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     recordedPanel(t, "lg-hdr-wqhd"),
		Modes:     []string{"1920x1080@60", "1280x720@60"},
		Current:   "1920x1080@60",
	})
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})

	for range 3 {
		if err := fixture.pass(); err != nil {
			t.Fatalf("running a pass at the declared mode: %v", err)
		}
	}

	if fixture.allocations != 0 {
		t.Errorf("the passes listed the claims %d times, want none", fixture.allocations)
	}
}

// The canvas repair restarts the compositor only after no claim holds
// a screen, including a claim whose prepare has not finished, for the
// same reason as the declared mode.
func TestTheCanvasRepairWaitsWhileAnUnpreparedClaimHoldsTheScreen(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	fixture.allocated = map[string]bool{"hdmi-a-1": true}
	if err := fixture.pass(); err != nil {
		t.Fatalf("running the first pass while a claim holds the screen: %v", err)
	}

	fixture.flap(t)
	fixture.advance(outputSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass after the screen flapped: %v", err)
	}
	if fixture.restarts != 0 {
		t.Fatalf("the compositor restarted %d times while a claim held a screen", fixture.restarts)
	}

	fixture.allocated = nil
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass after the claim ended: %v", err)
	}
	if fixture.restarts != 1 {
		t.Errorf("the compositor restarted %d times after the claim ended, want 1", fixture.restarts)
	}
}

// A claim is preparing when a live claim holds the output and no spec
// on disk lists it yet.
func TestAClaimIsPreparingUntilItsSpecIsWritten(t *testing.T) {
	cases := []struct {
		name      string
		allocated map[string]bool
		prepared  map[string]bool
		want      map[string]bool
	}{
		{name: "a claim the kubelet has not prepared", allocated: map[string]bool{"hdmi-a-1": true}, want: map[string]bool{"hdmi-a-1": true}},
		{name: "a claim the kubelet prepared", allocated: map[string]bool{"hdmi-a-1": true}, prepared: map[string]bool{"hdmi-a-1": true}, want: map[string]bool{}},
		{name: "no claim at all", want: map[string]bool{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := preparingOutputs(c.allocated, c.prepared); !maps.Equal(got, c.want) {
				t.Errorf("the preparing outputs are %v, want %v", got, c.want)
			}
		})
	}
}
