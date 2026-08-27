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
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

const (
	testClaimNamespace = "house"
	testClaimName      = "kitchen-screen"
	testClaimUID       = "claim-uid-1"
)

// allocatedClaim answers the one GET the driver makes, with the
// allocation the scheduler would have written on the claim's status:
// the results, and the config the scheduler resolved from the claim's
// own blocks and the DeviceClass's.
func allocatedClaim(t *testing.T, results []AllocatedDevice, config string) *Client {
	t.Helper()
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
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

// fakeCompositor stands in for the two things a mode switch touches
// outside this process: the current mode the card reports, and the
// signal that ends the compositor.
//
// The kubelet is what restarts the container, so the restart is
// modeled here: an ended compositor comes back with the modes the
// record states, unless the test says it declines them, which is what
// a mode weston cannot match does.
type fakeCompositor struct {
	mu          sync.Mutex
	record      string
	current     map[string]string
	offers      map[string][]drmMode
	kills       int
	republishes int
	declines    bool
	readErr     error
	// Which compositor is answering. Every restart is a new one,
	// and a readback takes no answer from the compositor it ended.
	session uint64
}

// modes is the GETCRTC readback: what each connector runs right now.
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

// Connectors stands in for the GETCONNECTOR read: every mode each
// connector offers now, with the kernel's vrefresh beside its name.
func (f *fakeCompositor) connectors() (map[string][]drmMode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	offered := map[string][]drmMode{}
	for connector, modes := range f.offers {
		offered[connector] = append([]drmMode{}, modes...)
	}
	return offered, nil
}

// Applied is what the card reports after weston takes the record's
// entry: the connector's first timing that matches it, with its
// refresh.
func (f *fakeCompositor) applied(connector, mode string) string {
	for _, offered := range f.offers[connector] {
		if modeMatches(mode, fmt.Sprintf("%s@%d", offered.Name, offered.Refresh)) {
			return fmt.Sprintf("%s@%d", offered.Name, offered.Refresh)
		}
	}
	return mode
}

// What the compositor itself reports about the outputs it
// serves, which is what a mode readback reads. The fixture serves
// what the card runs, because one compositor drives one card and the
// modes it serves are the modes the card is set to.
func (f *fakeCompositor) serving() servedOutputs {
	f.mu.Lock()
	defer f.mu.Unlock()
	return servedOutputs{session: f.session, modes: maps.Clone(f.current)}
}

// end is the SIGTERM and the kubelet's restart in one step.
func (f *fakeCompositor) end() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills++
	// The compositor that comes back is a new one whether or
	// not it takes the mode the record states.
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

// Republish stands in for the operator's own reconcile pass, which
// re-reads the connectors and writes the slice on divergence. The
// tests count calls, because the pass itself is main's wiring.
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

// labPlugin is the driver as it runs on the lab machine: an ultrawide
// on HDMI-A-1, a portable monitor on HDMI-A-2, and an empty DP-1, with
// a compositor serving its socket in the pod's runtime directory.
func labPlugin(t *testing.T, results []AllocatedDevice) *draPlugin {
	t.Helper()
	plugin, _ := labPluginWithConfig(t, results, "")
	return plugin
}

// labPluginWithConfig is the same driver, with the resolved config
// entries a claim and its DeviceClass produced, and with the
// compositor the switch acts on. The ultrawide runs its preferred
// mode and the portable panel runs 1920x1080, which is what both
// monitors come up at.
func labPluginWithConfig(t *testing.T, results []AllocatedDevice, config string) (*draPlugin, *fakeCompositor) {
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
		client:     allocatedClaim(t, results, config),
		sysRoot:    labSysfs(t),
		card:       "card1",
		socketDir:  servedSocketDir(t),
		configPath: filepath.Join(configDir, "weston.ini"),
		recordPath: compositor.record,
		// The wait is the same wait the operator runs on the machine,
		// with the bounds shortened so that a test that must reach the
		// timeout reaches it at once.
		currentModes:   compositor.modes,
		connectorModes: compositor.connectors,
		endCompositor:  compositor.end,
		served:         compositor.serving,
		republish:      compositor.republish,
		switchTimeout:  200 * time.Millisecond,
		switchInterval: time.Millisecond,
	}
	return plugin, compositor
}

// LabConnectorModes is what the kernel answers for the lab
// machine's connectors: the names its sysfs lists, with the
// refreshes each name carries.
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

// modeRecord reads the record the plugin keeps beside weston.ini.
func modeRecord(t *testing.T, plugin *draPlugin) map[string]string {
	t.Helper()
	record, err := readModeRecord(plugin.recordPath)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// westonINI reads the config the plugin regenerates on every change.
// A pod whose claims stated no mode has no file here at all, because
// the declare container writes it in a volume of the pod's own.
func westonINI(t *testing.T, plugin *draPlugin) string {
	t.Helper()
	written, err := os.ReadFile(plugin.configPath)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(written)
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

// screenRequest is the one allocation result a claim on the lab's
// portable panel holds.
func screenRequest() []AllocatedDevice {
	return []AllocatedDevice{
		{Request: "screen", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-2"},
	}
}

func TestPrepareDeliversAModeTheScreenAlreadyRuns(t *testing.T) {
	// The readback is what decides, so a claim that asks for the mode
	// on the screen right now delivers at once. This is what makes the
	// kubelet's retry free and the whole flow idempotent: nothing is
	// written and nothing restarts.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1920x1080"}`))

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare refused the mode the screen runs: %s", claim.Error)
	}
	if len(claim.Devices) != 1 {
		t.Fatalf("devices = %+v", claim.Devices)
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
	// The whole flow: the record takes the mode, the config regenerates
	// from the connector walk and the record, the compositor ends once,
	// and the delivery waits for GETCRTC to report the mode.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1280x720"}`))

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare refused a mode the connector offers: %s", claim.Error)
	}
	if len(claim.Devices) != 1 || claim.Devices[0].DeviceName != "hdmi-a-2" {
		t.Fatalf("devices = %+v", claim.Devices)
	}
	if compositor.ended() != 1 {
		t.Errorf("the compositor was ended %d times, want once", compositor.ended())
	}
	if got := modeRecord(t, plugin); got["HDMI-A-2"] != "1280x720" {
		t.Errorf("record = %v", got)
	}
	config := westonINI(t, plugin)
	for _, want := range []string{
		"name=HDMI-A-2\nmode=1280x720\napp-ids=hdmi-a-2",
		"name=HDMI-A-1\nmode=preferred\napp-ids=hdmi-a-1",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("the config does not contain %q:\n%s", want, config)
		}
	}
}

func TestPrepareSwitchesToAModeWithARefresh(t *testing.T) {
	// The whole WIDTHxHEIGHT@REFRESH string passes through to
	// weston's mode= line, which already reads that form.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1280x720@24"}`))

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare refused a refresh the connector offers: %s", claim.Error)
	}
	if compositor.ended() != 1 {
		t.Errorf("the compositor was ended %d times, want once", compositor.ended())
	}
	if got := modeRecord(t, plugin); got["HDMI-A-2"] != "1280x720@24" {
		t.Errorf("record = %v", got)
	}
	if got := westonINI(t, plugin); !strings.Contains(got, "name=HDMI-A-2\nmode=1280x720@24\n") {
		t.Errorf("the config does not state the refresh:\n%s", got)
	}
}

func TestPrepareDeliversARefreshTheScreenAlreadyRuns(t *testing.T) {
	// The readback speaks the same vocabulary, so a claim that names
	// the refresh already on screen delivers with no restart.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1920x1080@60"}`))

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare refused the refresh the screen runs: %s", claim.Error)
	}
	if compositor.ended() != 0 {
		t.Errorf("the compositor was ended %d times for a mode it already runs", compositor.ended())
	}
}

func TestPrepareRefusesARefreshTheConnectorDoesNotOffer(t *testing.T) {
	// Weston falls back silently for a refresh no timing carries, so
	// the refusal must name the refreshes that do exist.
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
	// Weston parses the refresh as an integer, so 59.94 would read
	// as 59, match nothing, and fall back silently.
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
	// The compositor probes the link on its way up and the mode list
	// grows with no uevent, so the pass after the readback is what
	// takes the list the kernel holds now.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1280x720"}`))

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}

	if compositor.republished() != 1 {
		t.Errorf("the slice was republished %d times, want once", compositor.republished())
	}
}

func TestPrepareRepublishesTheSliceWithNoRestart(t *testing.T) {
	// The case the folded open problem measured had no restart at
	// all, so a prepare that delivers a mode already on screen must
	// look again too. It just read the kernel, and the write costs
	// nothing when nothing moved.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1920x1080"}`))

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}

	if compositor.ended() != 0 {
		t.Fatalf("the compositor was ended %d times for a mode it already runs", compositor.ended())
	}
	if compositor.republished() != 1 {
		t.Errorf("the slice was republished %d times, want once", compositor.republished())
	}
}

func TestPrepareRepublishesNothingForAModeItRefused(t *testing.T) {
	// A prepare that failed validation never reached the card, so it
	// read no list and has nothing to republish.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1234x567"}`))

	if claim := prepare(t, plugin); claim.Error == "" {
		t.Fatal("prepare accepted a mode the connector does not offer")
	}

	if compositor.republished() != 0 {
		t.Errorf("the slice was republished %d times, want none", compositor.republished())
	}
}

func TestPrepareRefusesAModeTheConnectorDoesNotOffer(t *testing.T) {
	// Validation reads the connector's own sysfs list, never the
	// published attribute, and the failure names the whole list. The
	// attribute stops at 64 characters, so it is the one place a person
	// sees the names the attribute could not carry.
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
	// DP-1 has nothing on it, so there is no mode list to validate
	// against and no screen to light. The pod waits in
	// ContainerCreating, and the failure says why.
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
	// A key this driver does not read fails the prepare rather than
	// being dropped, whichever source wrote it. A dropped typo would
	// drive the wrong mode with nothing said anywhere.
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
	// Weston falls back to the preferred mode silently when it cannot
	// match what the config asks for, with no log line and no failed
	// exit. The readback is what catches that, and a second restart
	// would blank every screen on the machine for the same wrong
	// answer.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1280x720"}`))
	compositor.declines = true

	first := prepare(t, plugin)
	if first.Error == "" {
		t.Fatal("prepare delivered a screen that never took the mode")
	}
	if !strings.Contains(first.Error, "1280x720") {
		t.Errorf("error = %q, want it to name the mode", first.Error)
	}
	if compositor.ended() != 1 {
		t.Fatalf("the compositor was ended %d times, want once", compositor.ended())
	}

	// The kubelet retries the prepare it holds a pod for. The config
	// already asks for this mode and a restart already happened, so the
	// answer is the failure and not another dark machine.
	second := prepare(t, plugin)
	if second.Error == "" {
		t.Fatal("the retry delivered a screen that never took the mode")
	}
	if !strings.Contains(second.Error, "declined") {
		t.Errorf("error = %q, want it to say the compositor declined the mode", second.Error)
	}
	if compositor.ended() != 1 {
		t.Errorf("the compositor was ended %d times, want the one restart", compositor.ended())
	}
}

func TestUnprepareTakesTheModeOutOfTheRecord(t *testing.T) {
	// A claim that ends restarts nothing: the device allocates to one
	// claim at a time, and a revert would restart every screen on the
	// machine to serve nobody. The record and the config lose the
	// entry, so the next compositor start comes up at the mode the
	// monitor prefers.
	plugin, compositor := labPluginWithConfig(t, screenRequest(), claimMode(`{"mode": "1280x720"}`))
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}

	unprepare(t, plugin)

	if got := modeRecord(t, plugin); len(got) != 0 {
		t.Errorf("record = %v, want nothing left", got)
	}
	if got := westonINI(t, plugin); !strings.Contains(got, "name=HDMI-A-2\nmode=preferred") {
		t.Errorf("the config still states a mode:\n%s", got)
	}
	if compositor.ended() != 1 {
		t.Errorf("the compositor was ended %d times, want only the switch's restart", compositor.ended())
	}
}

// unprepare runs the kubelet's own call for the one claim these tests
// use, and fails when the driver answers with anything but success.
func unprepare(t *testing.T, plugin *draPlugin) {
	t.Helper()
	resp, err := plugin.NodeUnprepareResources(context.Background(), &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: testClaimNamespace, Name: testClaimName, Uid: testClaimUID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer := resp.Claims[testClaimUID]; answer == nil || answer.Error != "" {
		t.Fatalf("unprepare = %+v", resp.Claims)
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

// labPluginWithClaim is the driver on the lab machine with the panels
// wired to the i2c nodes their connectors name, for whatever
// allocation the claim holds. The control device's delivery is a node
// path, so these tests need the `ddc` links that benchPanels writes.
func labPluginWithClaim(t *testing.T, results []AllocatedDevice, config string,
	monitors map[string]*fakeMonitor) (*draPlugin, *panelBench) {
	t.Helper()
	plugin, _ := labPluginWithConfig(t, results, config)
	controls, bench := benchPanels(t, plugin.sysRoot, plugin.card, monitors)
	plugin.controls = controls
	plugin.powerPath = filepath.Join(t.TempDir(), "power.json")
	return plugin, bench
}

// controlRequest allocates the control device of the lab's portable
// panel, the second device HDMI-A-2 publishes.
func controlRequest() []AllocatedDevice {
	return []AllocatedDevice{
		{Request: "panel", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-2-control"},
	}
}

// preparedSpec reads the one file prepare left for the container
// runtime.
func preparedSpec(t *testing.T) cdiSpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cdiDir, cdiPrefix+testClaimUID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestPrepareDeliversTheControlNodeAndItsPath(t *testing.T) {
	panel := newFakeMonitor()
	plugin, bench := labPluginWithClaim(t, controlRequest(), "", claimedPanel(panel))

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare refused a panel that answers DDC/CI: %s", claim.Error)
	}
	if len(claim.Devices) != 1 || claim.Devices[0].DeviceName != "hdmi-a-2-control" {
		t.Fatalf("devices = %+v", claim.Devices)
	}
	wantID := cdiKind + "=" + testClaimUID + "-hdmi-a-2-control"
	if got := claim.Devices[0].CdiDeviceIds; len(got) != 1 || got[0] != wantID {
		t.Errorf("cdiDeviceIds = %v, want %q", got, wantID)
	}

	spec := preparedSpec(t)
	if len(spec.Devices) != 1 {
		t.Fatalf("spec devices = %+v", spec.Devices)
	}
	edits := spec.Devices[0].ContainerEdits
	// HDMI-A-2 reaches its panel on i2c-2 in this fixture, and the
	// kernel's numbering is why the path also travels as a variable.
	if len(edits.DeviceNodes) != 1 || edits.DeviceNodes[0].Path != "/dev/i2c-2" {
		t.Fatalf("deviceNodes = %+v", edits.DeviceNodes)
	}
	if edits.DeviceNodes[0].Permissions != "rw" {
		t.Errorf("permissions = %q", edits.DeviceNodes[0].Permissions)
	}
	if !containsString(edits.Env, "DISPLAY_CONTROL_BUS=/dev/i2c-2") {
		t.Errorf("env = %v", edits.Env)
	}
	// The consumer holds the wire and makes every write on it, so this
	// prepare puts nothing on the bus and never wakes the panel.
	if bench.opened() != 0 {
		t.Errorf("prepare opened %d buses for a control device", bench.opened())
	}
	if len(panel.sets) != 0 {
		t.Errorf("prepare wrote %+v to the panel", panel.sets)
	}
}

func TestPrepareDeliversAScreenAndItsControlChannel(t *testing.T) {
	// The two requests of one claim: the screen, whose brightness this
	// operator sets at prepare, and the control channel, which the
	// consumer drives itself. The block names the screen's request,
	// because a block that names none applies to every request.
	panel := newFakeMonitor()
	results := append(screenRequest(), controlRequest()...)
	plugin, _ := labPluginWithClaim(t, results,
		configEntry(configFromClaim, `"screen"`, `{"brightness": 40}`), claimedPanel(panel))

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare refused a claim on a screen and its controls: %s", claim.Error)
	}
	if len(claim.Devices) != 2 {
		t.Fatalf("devices = %+v", claim.Devices)
	}

	// One kind and one file for the whole claim, so an unprepare has
	// one file to remove however many devices the claim held.
	if got := specFiles(t); len(got) != 1 {
		t.Fatalf("the spec files are %v", got)
	}
	spec := preparedSpec(t)
	if spec.Kind != cdiKind || len(spec.Devices) != 2 {
		t.Fatalf("spec = %+v", spec)
	}
	screen, control := spec.Devices[0], spec.Devices[1]
	if screen.Name != testClaimUID+"-hdmi-a-2" || control.Name != testClaimUID+"-hdmi-a-2-control" {
		t.Fatalf("spec devices = %+v", spec.Devices)
	}
	if len(screen.ContainerEdits.Mounts) != 1 || len(screen.ContainerEdits.DeviceNodes) != 0 {
		t.Errorf("the screen's edits = %+v", screen.ContainerEdits)
	}
	if len(control.ContainerEdits.DeviceNodes) != 1 || len(control.ContainerEdits.Mounts) != 0 {
		t.Errorf("the control device's edits = %+v", control.ContainerEdits)
	}
	// The screen's own request still sets what it states.
	if got := panel.took(vcpBrightness); len(got) != 1 || got[0] != 40 {
		t.Errorf("the panel took %v, want the brightness the screen's request states", got)
	}
}

func TestPrepareRefusesParametersOnAControlRequest(t *testing.T) {
	// A control device is the opposite bargain: the consumer holds the
	// wire and makes every write. A parameter that resolved onto its
	// request named the wrong request, and a silent drop would leave a
	// person waiting for a brightness that no code was ever going to
	// set.
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
	// The fixture wires the portable panel only, so HDMI-A-1 has no
	// `ddc` link, which is what a DisplayPort connector behind an MST
	// hub also has: its DDC travels inside the AUX stream, where no
	// i2c-dev node reaches it.
	plugin, _ := labPluginWithClaim(t, []AllocatedDevice{
		{Request: "panel", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-1-control"},
	}, "", claimedPanel(newFakeMonitor()))

	claim := prepare(t, plugin)
	if claim.Error == "" {
		t.Fatal("prepare delivered a control device with no node behind it")
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
	// The panel that answered the probe is gone, so the wire behind
	// the node answers nothing. The pod waits in ContainerCreating and
	// the output's NoExecute taint is what ends it.
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
	// Nothing powers a panel on or off for a control device, at either
	// end of the claim. The operator wrote no VCP code at prepare, so
	// it owes the panel nothing when the claim ends.
	panel := newFakeMonitor()
	plugin, bench := labPluginWithClaim(t, controlRequest(), "", claimedPanel(panel))
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
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

// drawRequest allocates the draw device of the lab's portable panel,
// the shared companion HDMI-A-2 publishes beside its output.
func drawRequest() []AllocatedDevice {
	return []AllocatedDevice{
		{Request: "idle", Driver: DriverName, Pool: "liken-1", Device: "hdmi-a-2-draw"},
	}
}

func TestPrepareDeliversTheSocketForADrawDevice(t *testing.T) {
	// A draw device delivers the same Wayland connection the output
	// delivers, so a second client draws on the output through the
	// shared socket. It sets no mode and no panel power: the output
	// device owns the mode, and a power write from one of many holders
	// would act on a screen the others hold.
	panel := newFakeMonitor()
	plugin, compositor := labPluginWithConfig(t, drawRequest(), "")
	controls, bench := benchPanels(t, plugin.sysRoot, plugin.card, claimedPanel(panel))
	plugin.controls = controls
	plugin.powerPath = filepath.Join(t.TempDir(), "power.json")

	claim := prepare(t, plugin)
	if claim.Error != "" {
		t.Fatalf("prepare refused a draw device on a live output: %s", claim.Error)
	}
	if len(claim.Devices) != 1 || claim.Devices[0].DeviceName != "hdmi-a-2-draw" {
		t.Fatalf("devices = %+v", claim.Devices)
	}
	wantID := cdiKind + "=" + testClaimUID + "-hdmi-a-2-draw"
	if got := claim.Devices[0].CdiDeviceIds; len(got) != 1 || got[0] != wantID {
		t.Errorf("cdiDeviceIds = %v, want %q", got, wantID)
	}

	spec := preparedSpec(t)
	if len(spec.Devices) != 1 || spec.Devices[0].Name != testClaimUID+"-hdmi-a-2-draw" {
		t.Fatalf("spec devices = %+v", spec.Devices)
	}
	edits := spec.Devices[0].ContainerEdits
	for _, want := range []string{
		"XDG_RUNTIME_DIR=" + plugin.socketDir,
		"WAYLAND_DISPLAY=" + socketName,
		"DISPLAY_APP_ID=hdmi-a-2",
	} {
		if !containsString(edits.Env, want) {
			t.Errorf("env = %v, want %q in it", edits.Env, want)
		}
	}
	if len(edits.Mounts) != 1 || edits.Mounts[0].ContainerPath != plugin.socketDir {
		t.Errorf("mounts = %+v", edits.Mounts)
	}
	// The draw device delivers a socket, not a device node.
	if len(edits.DeviceNodes) != 0 {
		t.Errorf("the draw device delivered device nodes: %+v", edits.DeviceNodes)
	}

	// No mode: the record stays empty and the compositor never
	// restarts, because the output device is the one that sets a mode.
	if record := modeRecord(t, plugin); len(record) != 0 {
		t.Errorf("the draw device wrote a mode record: %+v", record)
	}
	if compositor.kills != 0 {
		t.Errorf("the draw device restarted the compositor %d times", compositor.kills)
	}
	if got := westonINI(t, plugin); got != "" {
		t.Errorf("the draw device rewrote weston.ini: %q", got)
	}
	// No panel power: nothing reached the i2c wire and the power record
	// stays empty, so the draw device owes the panel nothing.
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
