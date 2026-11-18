package main

// These tests call the plugin's prepare and unprepare as the kubelet
// does, against a fake API server that serves one allocated claim and a
// sysfs tree with the lab machine's monitors. They check which
// allocations prepare delivers and which it refuses.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

const (
	testClaimNamespace = "signage"
	testClaimName      = "menu-screen"
	testClaimUID       = "claim-uid-1"
)

// allocatedClaim returns a client for a fake API server that serves
// the claim with the status the scheduler would have written: the
// results, and the config merged from the claim's blocks and the
// DeviceClass's blocks.
func allocatedClaim(t *testing.T, results []AllocatedDevice, config string) *apiclient.Client {
	t.Helper()
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("encoding the allocation results: %v", err)
	}
	body := fmt.Sprintf(
		`{"metadata":{"name":%q,"namespace":%q,"uid":%q},"status":{"allocation":{"devices":{"results":%s,"config":[%s]}}}}`,
		testClaimName, testClaimNamespace, testClaimUID, encoded, config)

	return testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/apis/resource.k8s.io/v1/namespaces/" + testClaimNamespace + "/resourceclaims/" + testClaimName
		if r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		_, _ = w.Write([]byte(body))
	}))
}

// fakeCompositor replaces the card's mode reads and the compositor
// process that a mode switch ends. In production the compositor
// container starts weston again after the operator ends it, so the fake
// starts again too, with the modes in the record, unless declines is
// set. Weston declines a mode that it cannot match.
type fakeCompositor struct {
	mu          sync.Mutex
	record      string
	current     map[string]string
	offers      map[string][]drmMode
	kills       int
	republishes int
	declines    bool
	readErr     error
	// session identifies the running compositor. Every restart starts
	// a new one, so a readback ignores replies from the one it ended.
	session uint64
	// backoff is how long the new compositor takes to start, like a
	// crashed container in the kubelet's crash backoff. The delay uses
	// the clock, so a test that sets it runs in a synctest bubble. The
	// fake can also be set to never start.
	backoff time.Duration
	gone    bool
}

// modes replaces the GETCRTC read: the mode each connector runs.
func (f *fakeCompositor) modes() (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return nil, f.readErr
	}
	current := map[string]string{}
	for connector, mode := range f.current {
		current[connector] = mode
	}
	return current, nil
}

// connectors replaces the GETCONNECTOR read: every mode each connector
// offers, with the kernel's vrefresh next to its name.
func (f *fakeCompositor) connectors() (map[string][]drmMode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	offered := map[string][]drmMode{}
	for connector, modes := range f.offers {
		offered[connector] = append([]drmMode{}, modes...)
	}
	return offered, nil
}

// applied returns the mode the card reports after weston reads the
// record's entry: the first timing of the connector that matches it,
// with its refresh.
func (f *fakeCompositor) applied(connector, mode string) string {
	for _, offered := range f.offers[connector] {
		if modeMatches(mode, fmt.Sprintf("%s@%d", offered.Name, offered.Refresh)) {
			return fmt.Sprintf("%s@%d", offered.Name, offered.Refresh)
		}
	}
	return mode
}

// serving replaces the compositor's report of its outputs, with the
// card's current modes.
func (f *fakeCompositor) serving() servedOutputs {
	f.mu.Lock()
	defer f.mu.Unlock()
	return servedOutputs{session: f.session, modes: maps.Clone(f.current)}
}

func (f *fakeCompositor) pids() []int {
	return []int{14}
}

// signal ends the compositor and restarts it, in one step.
func (f *fakeCompositor) signal(int, syscall.Signal) error {
	return f.end()
}

func (f *fakeCompositor) end() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills++
	switch {
	case f.gone:
		return nil
	case f.backoff > 0:
		time.AfterFunc(f.backoff, func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			_ = f.start()
		})
		return nil
	}
	return f.start()
}

// start starts a new compositor, whether or not it takes the mode in
// the record. The caller holds mu.
func (f *fakeCompositor) start() error {
	f.session++
	if f.declines {
		return nil
	}
	record, err := readModeRecord(f.record)
	if err != nil {
		return err
	}
	for connector, mode := range record {
		f.current[connector] = f.applied(connector, mode)
	}
	return nil
}

func (f *fakeCompositor) ended() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.kills
}

// republish replaces the operator's reconcile pass, which reads the
// connectors again and writes the slice when it differs. Tests count
// the calls, because main sets up the real pass.
func (f *fakeCompositor) republish() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.republishes++
}

func (f *fakeCompositor) republished() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.republishes
}

// labPlugin is the driver on the lab machine: an ultrawide monitor on
// HDMI-A-1, a portable monitor on HDMI-A-2, and nothing on DP-1, with a
// compositor serving its socket in the pod's runtime directory.
func labPlugin(t *testing.T, results []AllocatedDevice) *draPlugin {
	t.Helper()
	plugin, _ := labPluginWithConfig(t, results, "")
	return plugin
}

// labPluginWithConfig is labPlugin with the merged config entries of a
// claim and its DeviceClass, and with the fake compositor that a mode
// switch acts on. At the start the ultrawide runs its preferred mode
// and the portable panel runs 1920x1080.
func labPluginWithConfig(t *testing.T, results []AllocatedDevice, config string) (*draPlugin, *fakeCompositor) {
	t.Helper()
	plugin, compositor, _ := labPluginWithModule(t, results, config)
	return plugin, compositor
}

// labPluginWithModule is labPluginWithConfig with the fake layout
// module that prepare opens each claim's socket through. It also
// returns the module, so a test can read the requests it received.
func labPluginWithModule(t *testing.T, results []AllocatedDevice, config string) (*draPlugin, *fakeCompositor, *fakeModule) {
	t.Helper()
	cdiDir = t.TempDir()
	configDir := t.TempDir()
	compositor := &fakeCompositor{
		record: filepath.Join(configDir, "modes.json"),
		current: map[string]string{
			"HDMI-A-1": "3840x1600@60",
			"HDMI-A-2": "1920x1080@60",
		},
		offers: labConnectorModes(),
	}
	plugin := &draPlugin{
		client:         allocatedClaim(t, results, config),
		sysRoot:        labSysfs(t),
		card:           "card1",
		socketDir:      servedSocketDir(t),
		configPath:     filepath.Join(configDir, "weston.ini"),
		recordPath:     compositor.record,
		currentModes:   compositor.modes,
		connectorModes: compositor.connectors,
		compositors:    compositor.pids,
		signal:         compositor.signal,
		served:         compositor.serving,
		republish:      compositor.republish,
		// Short limits, so that a test of a declined mode reaches the
		// timeout quickly.
		switchTimeout:  200 * time.Millisecond,
		switchFallback: time.Millisecond,
	}
	module := newFakeModule(t, moduleScript{})
	plugin.layout = servedLayoutLink(t, module)
	return plugin, compositor, module
}

// labConnectorModes returns the modes the kernel lists for the lab
// machine's connectors, with the refresh rates of each mode.
func labConnectorModes() map[string][]drmMode {
	return map[string][]drmMode{
		"HDMI-A-1": {
			{Name: "3840x1600", Refresh: 60},
			{Name: "3840x1600", Refresh: 24},
			{Name: "3840x2160", Refresh: 30},
			{Name: "1920x1080", Refresh: 60},
		},
		"HDMI-A-2": {
			{Name: "1920x1080", Refresh: 60},
			{Name: "1600x900", Refresh: 60},
			{Name: "1280x800", Refresh: 60},
			{Name: "1280x720", Refresh: 60},
			{Name: "1280x720", Refresh: 24},
			{Name: "1024x768", Refresh: 60},
			{Name: "800x600", Refresh: 60},
			{Name: "720x480", Refresh: 60},
			{Name: "640x480", Refresh: 60},
		},
	}
}

// modeRecord reads the mode record that the plugin writes beside
// weston.ini.
func modeRecord(t *testing.T, plugin *draPlugin) map[string]string {
	t.Helper()
	record, err := readModeRecord(plugin.recordPath)
	if err != nil {
		t.Fatalf("reading the mode record: %v", err)
	}
	return record
}

// westonINI returns the config that the plugin wrote, or "" when the
// plugin wrote none.
func westonINI(t *testing.T, plugin *draPlugin) string {
	t.Helper()
	written, err := os.ReadFile(plugin.configPath)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("reading the weston config: %v", err)
	}
	return string(written)
}

// servedSocketDir returns a runtime directory with a compositor
// listening on its socket, because prepare fails while none listens.
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
		t.Fatalf("calling NodePrepareResources: %v", err)
	}
	if len(resp.Claims) != 1 {
		t.Fatalf("the response has %d claims, want 1: %+v", len(resp.Claims), resp.Claims)
	}
	claim, ok := resp.Claims[testClaimUID]
	if !ok {
		t.Fatalf("the response has no entry for the claim: %+v", resp.Claims)
	}
	return claim
}

// specFiles lists the CDI spec files that prepare wrote for the
// container runtime.
func specFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(cdiDir)
	if err != nil {
		t.Fatalf("listing the CDI spec files: %v", err)
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
		t.Fatalf("prepare of a live output returned the error %q, want none", claim.Error)
	}
	if len(claim.Devices) != 1 {
		t.Fatalf("devices = %+v, want one", claim.Devices)
	}
	device := claim.Devices[0]
	if device.DeviceName != "hdmi-a-1" || device.PoolName != "liken-1" {
		t.Errorf("device = %+v, want hdmi-a-1 of pool liken-1", device)
	}
	if len(device.RequestNames) != 1 || device.RequestNames[0] != "screen" {
		t.Errorf("requestNames = %v, want [screen]", device.RequestNames)
	}
	// The claim's UID is in the CDI ID, so a claim created again under
	// the same name never gets the device edits of the old claim.
	wantID := "display.liken.sh/output=" + testClaimUID + "-hdmi-a-1"
	if len(device.CdiDeviceIds) != 1 || device.CdiDeviceIds[0] != wantID {
		t.Errorf("cdiDeviceIds = %v, want %q", device.CdiDeviceIds, wantID)
	}

	if got := specFiles(t); len(got) != 1 || got[0] != "display.liken.sh-"+testClaimUID+".json" {
		t.Fatalf("the spec files are %v, want the one of the claim", got)
	}
	raw, err := os.ReadFile(filepath.Join(cdiDir, "display.liken.sh-"+testClaimUID+".json"))
	if err != nil {
		t.Fatalf("reading the CDI spec: %v", err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parsing the CDI spec: %v", err)
	}
	if len(spec.Devices) != 1 || spec.Devices[0].Name != testClaimUID+"-hdmi-a-1" {
		t.Fatalf("spec devices = %+v, want one for hdmi-a-1", spec.Devices)
	}
	edits := spec.Devices[0].ContainerEdits
	for _, want := range []string{
		"XDG_RUNTIME_DIR=" + plugin.socketDir,
		"WAYLAND_DISPLAY=wayland-" + testClaimUID,
		"DISPLAY_APP_ID=hdmi-a-1",
	} {
		if !containsString(edits.Env, want) {
			t.Errorf("env = %v, want %q in it", edits.Env, want)
		}
	}
	if len(edits.Mounts) != 1 || edits.Mounts[0].ContainerPath != plugin.socketDir {
		t.Errorf("mounts = %+v, want one at the socket directory", edits.Mounts)
	}
}

func TestPrepareRefusesWhileNoCompositorServes(t *testing.T) {
	// The compositor exited and left its socket file. A client given
	// that path would get a refused connection, so prepare fails and
	// the kubelet retries.
	plugin := labPlugin(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	})
	plugin.socketDir = t.TempDir()
	staleSocket(t, plugin.socketDir)

	claim := prepare(t, plugin)
	if claim.Error == "" {
		t.Fatal("prepare returned no error although no compositor serves the socket")
	}
	if !strings.Contains(claim.Error, "compositor") {
		t.Errorf("error = %q, want it to say %q", claim.Error, "compositor")
	}
	if got := specFiles(t); len(got) != 0 {
		t.Errorf("a refused claim left %v behind", got)
	}
}

func TestPrepareRefusesAnOutputWithNoMonitorOnIt(t *testing.T) {
	// The monitor was unplugged between the allocation and this call.
	// Delivering the socket anyway would put the client's surface on
	// the first output, over whatever is there.
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
		t.Errorf("devices = %+v, want none", claim.Devices)
	}
	if got := specFiles(t); len(got) != 0 {
		t.Errorf("a refused claim left %v behind", got)
	}
}

func TestPrepareServesEveryConnectorWithAMonitorOnIt(t *testing.T) {
	// weston.ini has a section for every connector, so prepare accepts
	// a monitor that was plugged in after the compositor started.
	for _, device := range []string{"hdmi-a-1", "hdmi-a-2"} {
		t.Run(device, func(t *testing.T) {
			plugin := labPlugin(t, []AllocatedDevice{
				{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: device},
			})

			claim := prepare(t, plugin)
			if claim.Error != "" {
				t.Fatalf("prepare of %s returned the error %q, want none", device, claim.Error)
			}
			if len(claim.Devices) != 1 || claim.Devices[0].DeviceName != device {
				t.Fatalf("devices = %+v, want one device for the claim", claim.Devices)
			}
		})
	}
}

func TestPrepareLeavesAnotherDriversAllocationAlone(t *testing.T) {
	// A claim for a screen and its speakers has a result from each
	// driver, and each driver's plugin prepares its own result.
	plugin := labPlugin(t, []AllocatedDevice{
		{Request: "speakers", Driver: "audio.liken.sh", Pool: "liken-1", Device: "hdmi-1"},
	})

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare of another driver's allocation returned the error %q, want none", claim.Error)
	}
	if len(claim.Devices) != 0 {
		t.Errorf("devices = %+v, want none", claim.Devices)
	}
	if got := specFiles(t); len(got) != 0 {
		t.Errorf("the driver wrote %v for another driver's device", got)
	}
}

// screenRequest is the allocation result of a claim for the lab's
// portable panel.
func screenRequest() []AllocatedDevice {
	return []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-2"},
	}
}

func TestPrepareDeliversAModeTheScreenAlreadyRuns(t *testing.T) {
	// Prepare compares the mode with the one the screen runs before it
	// writes, so a kubelet retry writes nothing and restarts nothing.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1920x1080"}`))

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare of the mode the screen runs returned the error %q, want none", claim.Error)
	}
	if len(claim.Devices) != 1 {
		t.Fatalf("devices = %+v, want one device for the claim", claim.Devices)
	}
	if compositor.ended() != 0 {
		t.Errorf("the compositor was ended %d times for a mode it already runs", compositor.ended())
	}
	if got := modeRecord(t, plugin); len(got) != 0 {
		t.Errorf("record = %v, want nothing written", got)
	}
	if got := westonINI(t, plugin); got != "" {
		t.Errorf("the config was rewritten for a mode already up:\n%s", got)
	}
}

func TestPrepareSwitchesTheModeAndWaitsForTheReadback(t *testing.T) {
	// The record gets the mode, weston.ini is written from the
	// connectors and the record, the compositor ends once, and prepare
	// waits until the compositor reports the mode.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1280x720"}`))

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare of a mode the connector offers returned the error %q, want none", claim.Error)
	}
	if len(claim.Devices) != 1 || claim.Devices[0].DeviceName != "hdmi-a-2" {
		t.Fatalf("devices = %+v, want one device for the claim", claim.Devices)
	}
	if compositor.ended() != 1 {
		t.Errorf("the compositor was ended %d times, want once", compositor.ended())
	}
	if got := modeRecord(t, plugin); got["HDMI-A-2"] != "1280x720" {
		t.Errorf("record = %v, want HDMI-A-2 at 1280x720", got)
	}
	config := westonINI(t, plugin)
	for _, want := range []string{
		"name=HDMI-A-2\nmode=1280x720\n",
		"name=HDMI-A-1\nmode=preferred\n",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("the config does not contain %q:\n%s", want, config)
		}
	}
}

func TestPrepareSwitchesToAModeWithARefresh(t *testing.T) {
	// weston's mode= line accepts WIDTHxHEIGHT@REFRESH, so the whole
	// string goes into it.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1280x720@24"}`))

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare of a refresh the connector offers returned the error %q, want none", claim.Error)
	}
	if compositor.ended() != 1 {
		t.Errorf("the compositor was ended %d times, want once", compositor.ended())
	}
	if got := modeRecord(t, plugin); got["HDMI-A-2"] != "1280x720@24" {
		t.Errorf("record = %v, want HDMI-A-2 at 1280x720@24", got)
	}
	if got := westonINI(t, plugin); !strings.Contains(got, "name=HDMI-A-2\nmode=1280x720@24\n") {
		t.Errorf("the config does not state the refresh:\n%s", got)
	}
}

func TestPrepareDeliversARefreshTheScreenAlreadyRuns(t *testing.T) {
	// The current mode includes its refresh in the same format, so a
	// claim for the refresh that the screen runs needs no restart.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1920x1080@60"}`))

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare of the refresh the screen runs returned the error %q, want none", claim.Error)
	}
	if compositor.ended() != 0 {
		t.Errorf("the compositor was ended %d times for a mode it already runs", compositor.ended())
	}
}

func TestPrepareRefusesARefreshTheConnectorDoesNotOffer(t *testing.T) {
	// Weston silently falls back when no timing has the requested
	// refresh, so the error must list the refreshes that exist.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1280x720@100"}`))

	claim := prepare(t, plugin)
	if claim.Error == "" {
		t.Fatal("prepare accepted a refresh the connector does not offer")
	}
	for _, want := range []string{"1280x720@100", "60", "24"} {
		if !strings.Contains(claim.Error, want) {
			t.Errorf("error = %q, want it to say %q", claim.Error, want)
		}
	}
	if compositor.ended() != 0 {
		t.Error("the compositor was ended for a refresh that never passed validation")
	}
}

func TestPrepareRefusesARefreshThatIsNotAWholeNumber(t *testing.T) {
	// Weston parses the refresh as an integer, so 59.94 would become
	// 59, match no timing, and fall back silently.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1920x1080@59.94"}`))

	claim := prepare(t, plugin)
	if claim.Error == "" {
		t.Fatal("prepare accepted a refresh weston reads as another number")
	}
	if !strings.Contains(claim.Error, "59.94") {
		t.Errorf("error = %q, want it to name the refresh", claim.Error)
	}
	if compositor.ended() != 0 {
		t.Error("the compositor was ended for a mode that never parsed")
	}
}

func TestPrepareRepublishesTheSliceAfterTheReadback(t *testing.T) {
	// The mode list can grow with no uevent while the new compositor
	// probes the link, so prepare runs the reconcile pass after the
	// switch.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1280x720"}`))

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}

	if compositor.republished() != 1 {
		t.Errorf("the slice was republished %d times, want once", compositor.republished())
	}
}

func TestPrepareRepublishesTheSliceWithNoRestart(t *testing.T) {
	// A mode list can also grow with no restart, so a prepare for the
	// mode already on screen republishes too.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1920x1080"}`))

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}

	if compositor.ended() != 0 {
		t.Fatalf("the compositor was ended %d times for a mode it already runs", compositor.ended())
	}
	if compositor.republished() != 1 {
		t.Errorf("the slice was republished %d times, want once", compositor.republished())
	}
}

func TestPrepareRepublishesNothingForAModeItRefused(t *testing.T) {
	// A mode that fails validation stops prepare before it reads the
	// current mode, so there is nothing new to republish.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1234x567"}`))

	if claim := prepare(t, plugin); claim.Error == "" {
		t.Fatal("prepare accepted a mode the connector does not offer")
	}

	if compositor.republished() != 0 {
		t.Errorf("the slice was republished %d times, want none", compositor.republished())
	}
}

func TestPrepareRefusesAModeTheConnectorDoesNotOffer(t *testing.T) {
	// Validation reads the kernel's GETCONNECTOR mode list, not the
	// published attribute, and the error lists every mode. The
	// attribute stops at 64 characters, so this error is the only place
	// a person sees the modes that it cuts off.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1234x567"}`))

	claim := prepare(t, plugin)
	if claim.Error == "" {
		t.Fatal("prepare accepted a mode the connector does not offer")
	}
	for _, want := range []string{"1234x567", "1920x1080 1600x900 1280x800 1280x720"} {
		if !strings.Contains(claim.Error, want) {
			t.Errorf("error = %q, want it to say %q", claim.Error, want)
		}
	}
	if compositor.ended() != 0 {
		t.Errorf("the compositor was ended for a mode that never passed validation")
	}
	if got := specFiles(t); len(got) != 0 {
		t.Errorf("a refused claim left %v behind", got)
	}
}

func TestPrepareRefusesAModeOnAConnectorWithNoMonitor(t *testing.T) {
	// DP-1 has no monitor, so there is no mode list to validate
	// against.
	plugin, compositor := labPluginWithConfig(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "dp-1"},
	}, claimMode(`{"mode": "1280x720"}`))

	claim := prepare(t, plugin)
	if claim.Error == "" {
		t.Fatal("prepare accepted a mode on a connector with no monitor")
	}
	if !strings.Contains(claim.Error, "no monitor") {
		t.Errorf("error = %q, want it to say %q", claim.Error, "no monitor")
	}
	if compositor.ended() != 0 {
		t.Error("the compositor was ended for a connector with no monitor")
	}
}

func TestPrepareRefusesParametersItCannotRead(t *testing.T) {
	// A mistyped key that prepare ignored would leave the screen at the
	// wrong mode with no message.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"resolution": "1280x720"}`))

	claim := prepare(t, plugin)
	if claim.Error == "" {
		t.Fatal("prepare accepted parameters it cannot read")
	}
	if !strings.Contains(claim.Error, "resolution") {
		t.Errorf("error = %q, want it to name the key", claim.Error)
	}
	if compositor.ended() != 0 {
		t.Error("the compositor was ended for a claim that never parsed")
	}
}

func TestPrepareRefusesToRestartTwiceForAModeTheCompositorDeclined(t *testing.T) {
	// Weston falls back to the preferred mode when it cannot match the
	// config, with no log line and no failed exit. Only the mode that
	// the compositor reports after the restart shows the fallback.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1280x720"}`))
	compositor.declines = true

	first := prepare(t, plugin)
	if first.Error == "" {
		t.Fatal("prepare returned no error although the screen never switched to the mode")
	}
	if !strings.Contains(first.Error, "1280x720") {
		t.Errorf("error = %q, want it to name the mode", first.Error)
	}
	if compositor.ended() != 1 {
		t.Fatalf("the compositor was ended %d times, want once", compositor.ended())
	}

	// The kubelet's retry fails without a second restart, because a
	// restart blanks every screen on the card.
	second := prepare(t, plugin)
	if second.Error == "" {
		t.Fatal("the retry returned no error although the screen never switched to the mode")
	}
	if !strings.Contains(second.Error, "declined") {
		t.Errorf("error = %q, want it to say the compositor declined the mode", second.Error)
	}
	if compositor.ended() != 1 {
		t.Errorf("the compositor was ended %d times, want the one restart", compositor.ended())
	}
}

func TestUnprepareTakesTheModeOutOfTheRecord(t *testing.T) {
	// Unprepare does not restart the compositor to revert the mode. It
	// removes the entry from the record and the config, so the next
	// compositor start uses the mode the monitor prefers.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1280x720"}`))
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}

	unprepare(t, plugin)

	if got := modeRecord(t, plugin); len(got) != 0 {
		t.Errorf("record = %v, want nothing left", got)
	}
	if got := westonINI(t, plugin); !strings.Contains(got, "name=HDMI-A-2\nmode=preferred") {
		t.Errorf("the config still sets a mode:\n%s", got)
	}
	if compositor.ended() != 1 {
		t.Errorf("the compositor was ended %d times, want only the switch's restart", compositor.ended())
	}
}

// unprepare unprepares the test claim as the kubelet does, and fails
// the test on any error.
func unprepare(t *testing.T, plugin *draPlugin) {
	t.Helper()
	resp, err := plugin.NodeUnprepareResources(context.Background(), &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: testClaimNamespace, Name: testClaimName, Uid: testClaimUID}},
	})
	if err != nil {
		t.Fatalf("calling NodeUnprepareResources: %v", err)
	}
	if result := resp.Claims[testClaimUID]; result == nil || result.Error != "" {
		t.Fatalf("unprepare returned %+v, want an entry with no error", resp.Claims)
	}
}

func TestUnprepareRemovesTheSpec(t *testing.T) {
	plugin := labPlugin(t, []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1"},
	})
	prepare(t, plugin)

	unprepare(t, plugin)

	if got := specFiles(t); len(got) != 0 {
		t.Errorf("unprepare left %v behind", got)
	}
}

// labPluginWithClaim is labPluginWithConfig with the `ddc` links that
// benchPanels writes from each connector to its panel's i2c node, which
// a control device delivers.
func labPluginWithClaim(t *testing.T, results []AllocatedDevice, config string,
	monitors map[string]*fakeMonitor) (*draPlugin, *panelBench) {
	t.Helper()
	plugin, _ := labPluginWithConfig(t, results, config)
	controls, bench := benchPanels(t, plugin.sysRoot, plugin.card, monitors)
	plugin.controls = controls
	plugin.powerPath = filepath.Join(t.TempDir(), "power.json")
	return plugin, bench
}

// controlRequest is the allocation result for the control device of
// the lab's portable panel on HDMI-A-2.
func controlRequest() []AllocatedDevice {
	return []AllocatedDevice{
		{Request: "panel", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-2-control"},
	}
}

// preparedSpec reads the CDI spec file that prepare wrote.
func preparedSpec(t *testing.T) cdiSpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cdiDir, cdiPrefix+testClaimUID+".json"))
	if err != nil {
		t.Fatalf("reading the CDI spec: %v", err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parsing the CDI spec: %v", err)
	}
	return spec
}

func TestPrepareDeliversTheControlNodeAndItsPath(t *testing.T) {
	panel := newFakeMonitor()
	plugin, bench := labPluginWithClaim(t, controlRequest(), "", claimedPanel(panel))

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare of a panel that responds to DDC/CI returned the error %q, want none", claim.Error)
	}
	if len(claim.Devices) != 1 || claim.Devices[0].DeviceName != "hdmi-a-2-control" {
		t.Fatalf("devices = %+v, want one device for the claim", claim.Devices)
	}
	wantID := cdiKind + "=" + testClaimUID + "-hdmi-a-2-control"
	if got := claim.Devices[0].CdiDeviceIds; len(got) != 1 || got[0] != wantID {
		t.Errorf("cdiDeviceIds = %v, want %q", got, wantID)
	}

	spec := preparedSpec(t)
	if len(spec.Devices) != 1 {
		t.Fatalf("spec devices = %+v, want the devices of the claim", spec.Devices)
	}
	edits := spec.Devices[0].ContainerEdits
	// In this fixture HDMI-A-2 reaches its panel on i2c-2. The kernel
	// can number the node differently on another boot, so the spec
	// also passes the path as an environment variable.
	if len(edits.DeviceNodes) != 1 || edits.DeviceNodes[0].Path != "/dev/i2c-2" {
		t.Fatalf("deviceNodes = %+v, want /dev/i2c-2", edits.DeviceNodes)
	}
	if edits.DeviceNodes[0].Permissions != "rw" {
		t.Errorf("permissions = %q, want rw", edits.DeviceNodes[0].Permissions)
	}
	if !containsString(edits.Env, "DISPLAY_CONTROL_BUS=/dev/i2c-2") {
		t.Errorf("env = %v, want DISPLAY_CONTROL_BUS=/dev/i2c-2 in it", edits.Env)
	}
	// The consumer writes to the bus itself, so prepare sends nothing
	// to the panel and does not wake it.
	if bench.opened() != 0 {
		t.Errorf("prepare opened %d buses for a control device", bench.opened())
	}
	if len(panel.sets) != 0 {
		t.Errorf("prepare wrote %+v to the panel", panel.sets)
	}
}

func TestPrepareDeliversAScreenAndItsControlChannel(t *testing.T) {
	// The operator sets the screen's brightness at prepare, and the
	// consumer drives the control channel itself. The parameter block
	// names the screen's request, because a block with no request name
	// applies to every request.
	panel := newFakeMonitor()
	results := append(screenRequest(), controlRequest()...)
	plugin, _ := labPluginWithClaim(t, results,
		configEntry(configFromClaim, `"screen"`, `{"brightness": 40}`), claimedPanel(panel))

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare of a claim on a screen and its controls returned the error %q, want none", claim.Error)
	}
	if len(claim.Devices) != 2 {
		t.Fatalf("devices = %+v, want one device for the claim", claim.Devices)
	}

	// A claim has one CDI spec file for all its devices.
	if got := specFiles(t); len(got) != 1 {
		t.Fatalf("the spec files are %v, want one", got)
	}
	spec := preparedSpec(t)
	if spec.Kind != cdiKind || len(spec.Devices) != 2 {
		t.Fatalf("spec = %+v, want kind %s with 2 devices", spec, cdiKind)
	}
	screen, control := spec.Devices[0], spec.Devices[1]
	if screen.Name != testClaimUID+"-hdmi-a-2" || control.Name != testClaimUID+"-hdmi-a-2-control" {
		t.Fatalf("spec devices = %+v, want the devices of the claim", spec.Devices)
	}
	if len(screen.ContainerEdits.Mounts) != 1 || len(screen.ContainerEdits.DeviceNodes) != 0 {
		t.Errorf("the screen's edits = %+v", screen.ContainerEdits)
	}
	if len(control.ContainerEdits.DeviceNodes) != 1 || len(control.ContainerEdits.Mounts) != 0 {
		t.Errorf("the control device's edits = %+v", control.ContainerEdits)
	}
	if got := panel.writesTo(vcpBrightness); len(got) != 1 || got[0] != 40 {
		t.Errorf("the panel received the writes %v, want the brightness that the screen's request sets", got)
	}
}

func TestPrepareRefusesParametersOnAControlRequest(t *testing.T) {
	// If prepare ignored a parameter on a control request, the person
	// would wait for a brightness that nothing sets.
	cases := []struct {
		name   string
		config string
		says   string
	}{
		{name: "a mode", config: claimMode(`{"mode": "1280x720"}`), says: modeParameter},
		{name: "a brightness", config: claimControl(`{"brightness": 40}`), says: brightnessParameter},
		{name: "a power", config: claimControl(`{"power": "on"}`), says: powerParameter},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			panel := newFakeMonitor()
			plugin, bench := labPluginWithClaim(t, controlRequest(), c.config, claimedPanel(panel))

			claim := prepare(t, plugin)
			if claim.Error == "" {
				t.Fatal("prepare accepted a parameter on a control request")
			}
			for _, want := range []string{c.says, "hdmi-a-2-control"} {
				if !strings.Contains(claim.Error, want) {
					t.Errorf("error = %q, want it to say %q", claim.Error, want)
				}
			}
			if bench.opened() != 0 {
				t.Errorf("a refused claim opened %d buses", bench.opened())
			}
			if got := specFiles(t); len(got) != 0 {
				t.Errorf("a refused claim left %v behind", got)
			}
		})
	}
}

func TestPrepareRefusesAControlOnAConnectorWithNoDDCChannel(t *testing.T) {
	// The fixture links only the portable panel, so HDMI-A-1 has no
	// `ddc` link. A DisplayPort connector behind an MST hub has none
	// either, because its DDC travels inside the AUX stream, which no
	// i2c-dev node reaches.
	plugin, _ := labPluginWithClaim(t, []AllocatedDevice{
		{Request: "panel", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1-control"},
	}, "", claimedPanel(newFakeMonitor()))

	claim := prepare(t, plugin)
	if claim.Error == "" {
		t.Fatal("prepare returned no error for a control device with no node behind it")
	}
	for _, want := range []string{"HDMI-A-1", "DDC/CI"} {
		if !strings.Contains(claim.Error, want) {
			t.Errorf("error = %q, want it to say %q", claim.Error, want)
		}
	}
	if got := specFiles(t); len(got) != 0 {
		t.Errorf("a refused claim left %v behind", got)
	}
}

func TestPrepareRefusesAControlOnAConnectorWithNoMonitor(t *testing.T) {
	// The panel that replied to the probe is gone, so the i2c bus behind
	// the node gets no reply. The pod waits in ContainerCreating until
	// the output's NoExecute taint evicts it.
	plugin, _ := labPluginWithClaim(t, []AllocatedDevice{
		{Request: "panel", Driver: DriverName, Pool: "liken-1", Device: "dp-1-control"},
	}, "", claimedPanel(newFakeMonitor()))

	claim := prepare(t, plugin)
	if claim.Error == "" {
		t.Fatal("prepare accepted a control device on a connector with nothing on it")
	}
	for _, want := range []string{"dp-1", "no monitor"} {
		if !strings.Contains(claim.Error, want) {
			t.Errorf("error = %q, want it to say %q", claim.Error, want)
		}
	}
}

func TestUnprepareOfAControlWritesNothingToThePanel(t *testing.T) {
	// The operator writes no VCP code at prepare of a control device,
	// so it has no power command to send at unprepare.
	panel := newFakeMonitor()
	plugin, bench := labPluginWithClaim(t, controlRequest(), "", claimedPanel(panel))
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}

	unprepare(t, plugin)

	if len(panel.sets) != 0 {
		t.Errorf("unprepare wrote %+v to the panel", panel.sets)
	}
	if bench.opened() != 0 {
		t.Errorf("unprepare opened %d buses", bench.opened())
	}
	if got := specFiles(t); len(got) != 0 {
		t.Errorf("unprepare left %v behind", got)
	}
}

// drawRequest is the allocation result for the draw device of the
// lab's portable panel on HDMI-A-2.
func drawRequest() []AllocatedDevice {
	return []AllocatedDevice{
		{Request: "idle", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-2-draw"},
	}
}

func TestPrepareDeliversTheSocketForADrawDevice(t *testing.T) {
	// A draw device delivers the claim's own socket on the output's
	// screen, so a second client can draw there. It sets no mode and no
	// panel power.
	panel := newFakeMonitor()
	plugin, compositor := labPluginWithConfig(t, drawRequest(), "")
	controls, bench := benchPanels(t, plugin.sysRoot, plugin.card, claimedPanel(panel))
	plugin.controls = controls
	plugin.powerPath = filepath.Join(t.TempDir(), "power.json")

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare of a draw device on a live output returned the error %q, want none", claim.Error)
	}
	if len(claim.Devices) != 1 || claim.Devices[0].DeviceName != "hdmi-a-2-draw" {
		t.Fatalf("devices = %+v, want one device for the claim", claim.Devices)
	}
	wantID := cdiKind + "=" + testClaimUID + "-hdmi-a-2-draw"
	if got := claim.Devices[0].CdiDeviceIds; len(got) != 1 || got[0] != wantID {
		t.Errorf("cdiDeviceIds = %v, want %q", got, wantID)
	}

	spec := preparedSpec(t)
	if len(spec.Devices) != 1 || spec.Devices[0].Name != testClaimUID+"-hdmi-a-2-draw" {
		t.Fatalf("spec devices = %+v, want the devices of the claim", spec.Devices)
	}
	edits := spec.Devices[0].ContainerEdits
	for _, want := range []string{
		"XDG_RUNTIME_DIR=" + plugin.socketDir,
		"WAYLAND_DISPLAY=wayland-" + testClaimUID,
		"DISPLAY_APP_ID=hdmi-a-2",
	} {
		if !containsString(edits.Env, want) {
			t.Errorf("env = %v, want %q in it", edits.Env, want)
		}
	}
	if len(edits.Mounts) != 1 || edits.Mounts[0].ContainerPath != plugin.socketDir {
		t.Errorf("mounts = %+v", edits.Mounts)
	}
	if len(edits.DeviceNodes) != 0 {
		t.Errorf("the draw device added device nodes: %+v", edits.DeviceNodes)
	}

	if record := modeRecord(t, plugin); len(record) != 0 {
		t.Errorf("the draw device wrote a mode record: %+v", record)
	}
	if compositor.kills != 0 {
		t.Errorf("the draw device restarted the compositor %d times", compositor.kills)
	}
	if got := westonINI(t, plugin); got != "" {
		t.Errorf("the draw device rewrote weston.ini: %q", got)
	}
	// An empty power record means unprepare schedules no standby.
	if bench.opened() != 0 {
		t.Errorf("the draw device opened %d buses", bench.opened())
	}
	if len(panel.sets) != 0 {
		t.Errorf("the draw device wrote %+v to the panel", panel.sets)
	}
	if record := powerRecord(t, plugin); len(record) != 0 {
		t.Errorf("the draw device wrote a power record: %+v", record)
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
