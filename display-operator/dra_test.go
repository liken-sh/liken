package main

// These tests drive the real plugin through the kubelet's own call,
// against an API server that serves one allocated claim and a sysfs
// tree that holds the lab machine's monitors. What they cover is the
// decision inside prepare: which allocations become a delivery, and
// which ones the driver refuses.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

const (
	testClaimNamespace = "house"
	testClaimName      = "kitchen-screen"
	testClaimUID       = "claim-uid-1"
)

// allocatedClaim answers the one GET the driver makes, with the
// allocation the scheduler would have written on the claim's status.
func allocatedClaim(t *testing.T, results []AllocatedDevice) *Client {
	t.Helper()
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(
		`{"metadata":{"name":%q,"namespace":%q,"uid":%q},"status":{"allocation":{"devices":{"results":%s}}}}`,
		testClaimName, testClaimNamespace, testClaimUID, encoded)

	return testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/apis/resource.k8s.io/v1/namespaces/" + testClaimNamespace + "/resourceclaims/" + testClaimName
		if r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		_, _ = w.Write([]byte(body))
	}))
}

// labPlugin is the driver as it runs on the lab machine: an ultrawide
// on HDMI-A-1, a portable monitor on HDMI-A-2, and an empty DP-1, with
// a compositor serving its socket in the pod's runtime directory.
func labPlugin(t *testing.T, results []AllocatedDevice) *draPlugin {
	t.Helper()
	cdiDir = t.TempDir()
	return &draPlugin{
		client:    allocatedClaim(t, results),
		sysRoot:   labSysfs(t),
		card:      "card1",
		socketDir: servedSocketDir(t),
	}
}

// servedSocketDir is a runtime directory with a compositor answering
// in it. The socket is the delivery, so prepare answers only while a
// compositor is on the other end of it.
func servedSocketDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	servingSocket(t, dir)
	return dir
}

func prepare(t *testing.T, plugin *draPlugin) *drav1.NodePrepareResourceResponse {
	t.Helper()
	resp, err := plugin.NodePrepareResources(context.Background(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{
			Namespace: testClaimNamespace,
			Name:      testClaimName,
			Uid:       testClaimUID,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The response must carry one entry for each claim, because the
	// kubelet treats a missing entry as a failure to retry.
	if len(resp.Claims) != 1 {
		t.Fatalf("claims = %+v", resp.Claims)
	}
	claim, ok := resp.Claims[testClaimUID]
	if !ok {
		t.Fatalf("the response has no answer for the claim: %+v", resp.Claims)
	}
	return claim
}

// specFiles lists what prepare left for the container runtime.
func specFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(cdiDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestPrepareDeliversTheSocketAndTheAppID(t *testing.T) {
	plugin := labPlugin(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	})

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare refused a live output: %s", claim.Error)
	}
	if len(claim.Devices) != 1 {
		t.Fatalf("devices = %+v", claim.Devices)
	}
	device := claim.Devices[0]
	if device.DeviceName != "hdmi-a-1" || device.PoolName != "liken-1" {
		t.Errorf("device = %+v", device)
	}
	if len(device.RequestNames) != 1 || device.RequestNames[0] != "screen" {
		t.Errorf("requestNames = %v", device.RequestNames)
	}
	// The CDI ID names the file's device, and the claim's UID is in it
	// so a claim recreated under one name never reuses a stale grant.
	wantID := "display.liken.sh/output=" + testClaimUID + "-hdmi-a-1"
	if len(device.CdiDeviceIds) != 1 || device.CdiDeviceIds[0] != wantID {
		t.Errorf("cdiDeviceIds = %v, want %q", device.CdiDeviceIds, wantID)
	}

	if got := specFiles(t); len(got) != 1 || got[0] != "display.liken.sh-"+testClaimUID+".json" {
		t.Fatalf("the spec files are %v", got)
	}
	raw, err := os.ReadFile(filepath.Join(cdiDir, "display.liken.sh-"+testClaimUID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Devices) != 1 || spec.Devices[0].Name != testClaimUID+"-hdmi-a-1" {
		t.Fatalf("spec devices = %+v", spec.Devices)
	}
	edits := spec.Devices[0].ContainerEdits
	for _, want := range []string{
		"XDG_RUNTIME_DIR=" + plugin.socketDir,
		"WAYLAND_DISPLAY=" + socketName,
		"DISPLAY_APP_ID=hdmi-a-1",
	} {
		if !containsString(edits.Env, want) {
			t.Errorf("env = %v, want %q in it", edits.Env, want)
		}
	}
	if len(edits.Mounts) != 1 || edits.Mounts[0].ContainerPath != plugin.socketDir {
		t.Errorf("mounts = %+v", edits.Mounts)
	}
}

func TestPrepareRefusesWhileNoCompositorServes(t *testing.T) {
	// The compositor died and left its socket file behind, and its
	// container is restarting. The screen is real and the allocation
	// stands, and a delivery now would hand the client a path that
	// refuses it, so the kubelet holds the pod and retries.
	plugin := labPlugin(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	})
	plugin.socketDir = t.TempDir()
	staleSocket(t, plugin.socketDir)

	claim := prepare(t, plugin)
	if claim.Error == "" {
		t.Fatal("prepare delivered a socket that no compositor serves")
	}
	if !strings.Contains(claim.Error, "compositor") {
		t.Errorf("error = %q, want it to say %q", claim.Error, "compositor")
	}
	if got := specFiles(t); len(got) != 0 {
		t.Errorf("a refused claim left %v behind", got)
	}
}

func TestPrepareRefusesAnOutputWithNoMonitorOnIt(t *testing.T) {
	// The monitor left between the allocation and this call.
	// Delivering the socket anyway would put the client's surface on
	// the first output, on top of whatever was there.
	plugin := labPlugin(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "dp-1"},
	})

	claim := prepare(t, plugin)
	if claim.Error == "" {
		t.Fatal("prepare accepted a connector with no monitor on it")
	}
	if !strings.Contains(claim.Error, "no monitor") {
		t.Errorf("error = %q, want it to say %q", claim.Error, "no monitor")
	}
	if len(claim.Devices) != 0 {
		t.Errorf("devices = %+v", claim.Devices)
	}
	if got := specFiles(t); len(got) != 0 {
		t.Errorf("a refused claim left %v behind", got)
	}
}

func TestPrepareServesEveryConnectorWithAMonitorOnIt(t *testing.T) {
	// The driver checks one fact: whether a monitor is connected. The
	// config has a section for every connector, so when the monitor
	// arrived does not matter.
	for _, device := range []string{"hdmi-a-1", "hdmi-a-2"} {
		t.Run(device, func(t *testing.T) {
			plugin := labPlugin(t, []AllocatedDevice{
				{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: device},
			})

			claim := prepare(t, plugin)
			if claim.Error != "" {
				t.Fatalf("prepare refused %s: %s", device, claim.Error)
			}
			if len(claim.Devices) != 1 || claim.Devices[0].DeviceName != device {
				t.Fatalf("devices = %+v", claim.Devices)
			}
		})
	}
}

func TestPrepareLeavesAnotherDriversAllocationAlone(t *testing.T) {
	// A claim that asks for a screen and that screen's speakers holds
	// a result from each driver, and each driver's own plugin prepares
	// its own.
	plugin := labPlugin(t, []AllocatedDevice{
		{Request: "speakers", Driver: "audio.liken.sh", Pool: "liken-1", Device: "hdmi-1"},
	})

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare failed on another driver's allocation: %s", claim.Error)
	}
	if len(claim.Devices) != 0 {
		t.Errorf("devices = %+v", claim.Devices)
	}
	if got := specFiles(t); len(got) != 0 {
		t.Errorf("the driver wrote %v for another driver's device", got)
	}
}

func TestUnprepareRemovesTheSpec(t *testing.T) {
	plugin := labPlugin(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	})
	prepare(t, plugin)

	resp, err := plugin.NodeUnprepareResources(context.Background(), &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: testClaimNamespace, Name: testClaimName, Uid: testClaimUID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer := resp.Claims[testClaimUID]; answer == nil || answer.Error != "" {
		t.Fatalf("unprepare = %+v", resp.Claims)
	}
	if got := specFiles(t); len(got) != 0 {
		t.Errorf("unprepare left %v behind", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
