package main

// These tests cover the publisher's decisions: create the slice when
// it is absent, leave the slice alone when it is current, replace the
// slice with an increased pool generation when the outputs changed,
// and write nothing at all when the walk found no connector.

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// slicePublishFixture is a small API server that holds at most one
// ResourceSlice. It records the requests it received.
type slicePublishFixture struct {
	existing *ResourceSlice
	requests []string
	created  *ResourceSlice
	updated  *ResourceSlice
	deleted  bool
}

func (f *slicePublishFixture) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			if f.existing == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(f.existing)
		case http.MethodPost:
			f.created = &ResourceSlice{}
			_ = json.NewDecoder(r.Body).Decode(f.created)
			_ = json.NewEncoder(w).Encode(f.created)
		case http.MethodPut:
			f.updated = &ResourceSlice{}
			_ = json.NewDecoder(r.Body).Decode(f.updated)
			_ = json.NewEncoder(w).Encode(f.updated)
		case http.MethodDelete:
			f.deleted = true
			f.existing = nil
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

func testOwner() OwnerReference {
	return OwnerReference{APIVersion: "v1", Kind: "Node", Name: "liken-1", UID: "abc-123"}
}

func testOutputs(t *testing.T) []Output {
	t.Helper()
	return discoverOutputs(labSysfs(t), "card1")
}

func TestSliceDevicesPublishesTheMonitorsFacts(t *testing.T) {
	devices := sliceDevices(testOutputs(t))
	if len(devices) != 3 {
		t.Fatalf("got %d devices, want 3", len(devices))
	}
	// Sorted by name, so the same hardware always makes the same
	// slice.
	if devices[0].Name != "dp-1" || devices[1].Name != "hdmi-a-1" || devices[2].Name != "hdmi-a-2" {
		t.Fatalf("names = %q, %q, %q", devices[0].Name, devices[1].Name, devices[2].Name)
	}

	ultrawide := devices[1].Attributes
	want := map[string]string{
		"connector":      "HDMI-A-1",
		"appId":          "hdmi-a-1",
		"manufacturer":   "GSM",
		"model":          "LG HDR WQHD",
		"serial":         "202NTRLCC070",
		pairingAttribute: "gsm-7716-lg-hdr-wqhd",
	}
	for name, value := range want {
		attribute, ok := ultrawide[name]
		if !ok || attribute.String == nil {
			t.Fatalf("%s is missing: %+v", name, ultrawide)
		}
		if *attribute.String != value {
			t.Errorf("%s = %q, want %q", name, *attribute.String, value)
		}
	}
	sizes := map[string]int64{
		"widthPixels":       3840,
		"heightPixels":      1600,
		"refreshMillihertz": 59999,
		"widthMillimeters":  879,
		"heightMillimeters": 366,
	}
	for name, value := range sizes {
		attribute, ok := ultrawide[name]
		if !ok || attribute.Int == nil {
			t.Fatalf("%s is missing: %+v", name, ultrawide)
		}
		if *attribute.Int != value {
			t.Errorf("%s = %d, want %d", name, *attribute.Int, value)
		}
	}
	if len(devices[1].Taints) != 0 {
		t.Errorf("a monitor that is on the wire has taints: %+v", devices[1].Taints)
	}
}

// attributeText reads one string attribute, and fails the test when
// the device published none.
func attributeText(t *testing.T, device SliceDevice, name string) string {
	t.Helper()
	attribute, ok := device.Attributes[name]
	if !ok || attribute.String == nil {
		t.Fatalf("%s publishes no %s: %+v", device.Name, name, device.Attributes)
	}
	return *attribute.String
}

func TestSliceDevicesPublishesTheModesTheMonitorAccepts(t *testing.T) {
	// Both lists run past the API's 64-character limit, so each ends
	// on the last whole name that fits, and the kernel's descending
	// order makes the dropped tail the smallest modes. The ultrawide
	// names sixteen resolutions and publishes six; the portable panel
	// names eight and drops only 640x480.
	devices := sliceDevices(testOutputs(t))

	want := "3840x1600 3840x2160 3440x1440 1920x1080 1680x1050 1600x900"
	if got := attributeText(t, devices[1], "modes"); got != want {
		t.Errorf("hdmi-a-1 modes = %q, want %q", got, want)
	}
	want = "1920x1080 1600x900 1280x800 1280x720 1024x768 800x600 720x480"
	if got := attributeText(t, devices[2], "modes"); got != want {
		t.Errorf("hdmi-a-2 modes = %q, want %q", got, want)
	}
}

// attributeListCase is one list and the string the limit leaves of it.
type attributeListCase struct {
	values []string
	want   string
}

func TestAttributeListEndsOnTheLastWholeEntryThatFits(t *testing.T) {
	// A name cut in half names a mode no monitor accepts, so the cut
	// falls between entries and never inside one.
	fits := []string{"1920x1080", "1440x1080", "1600x900", "1280x800", "1280x720", "1024x768", "1152x864"}
	oneMore := []string{"1920x1080", "1440x1080", "1600x900", "1280x800", "1280x720", "1024x768", "1152x864", "640x480"}
	filled := "1920x1080 1440x1080 1600x900 1280x800 1280x720 1024x768 1152x864"
	cases := map[string]attributeListCase{
		"a list under the limit":       {[]string{"1920x1080", "800x600"}, "1920x1080 800x600"},
		"a list that fills it exactly": {fits, filled},
		"the entry that would pass it": {oneMore, filled},
		"no entries at all":            {nil, ""},
		"one entry past the limit":     {[]string{strings.Repeat("1920x1080", 8)}, ""},
	}
	for name, tc := range cases {
		if got := attributeList(tc.values); got != tc.want {
			t.Errorf("%s: attributeList = %q, want %q", name, got, tc.want)
		}
	}
}

// controlledPanel is a connector whose panel answered the DDC/CI
// probe, which is the only state in which one connector publishes two
// devices.
func controlledPanel(controls supportedControls) Output {
	output := litOutput("HDMI-A-1", labMonitor())
	output.Controls = controls
	return output
}

// deviceNames lists what one pass published, in the order the slice
// carries them.
func deviceNames(devices []SliceDevice) []string {
	names := make([]string, len(devices))
	for i, device := range devices {
		names[i] = device.Name
	}
	return names
}

func TestSliceDevicesPublishesAControlDeviceBesideTheOutput(t *testing.T) {
	devices := sliceDevices([]Output{controlledPanel(supportedControls{Brightness: true, Power: true})})

	if got := deviceNames(devices); !slices.Equal(got, []string{"hdmi-a-1", "hdmi-a-1-control"}) {
		t.Fatalf("devices = %v", got)
	}
	output, control := devices[0], devices[1]
	// The monitor identity is the attribute both devices publish, so
	// one claim holds a screen and its control channel through a
	// matchAttribute constraint across its two requests.
	if got, want := attributeText(t, control, pairingAttribute), attributeText(t, output, pairingAttribute); got != want {
		t.Errorf("%s = %q, want %q", pairingAttribute, got, want)
	}
	if got := attributeText(t, control, "connector"); got != "HDMI-A-1" {
		t.Errorf("connector = %q", got)
	}
	for _, name := range []string{"controlsBrightness", "controlsPower"} {
		attribute, published := control.Attributes[name]
		if !published || attribute.Bool == nil || !*attribute.Bool {
			t.Errorf("%s = %+v, want it published and true", name, attribute)
		}
	}
	// The delivery is the i2c node, so a control device promises no
	// Wayland connection and publishes no app-id.
	if _, published := control.Attributes["appId"]; published {
		t.Errorf("the control device publishes an appId: %+v", control.Attributes)
	}
	// The marker is what a device class selects on, and only the
	// control device carries it.
	if attribute, published := control.Attributes["control"]; !published || attribute.Bool == nil || !*attribute.Bool {
		t.Errorf("control = %+v, want it published and true", attribute)
	}
	if _, published := output.Attributes["control"]; published {
		t.Errorf("the output device publishes the control marker: %+v", output.Attributes)
	}
	if len(control.Taints) != 0 {
		t.Errorf("taints = %+v", control.Taints)
	}
}

func TestSliceDevicesPublishesAControlDeviceOnlyForAPanelThatAnswered(t *testing.T) {
	cases := []struct {
		name      string
		output    Output
		published bool
	}{
		{
			name:      "a panel that carries both controls",
			output:    controlledPanel(supportedControls{Brightness: true, Power: true}),
			published: true,
		},
		{
			// A display implements the subset of the standard it
			// chooses, and one control is enough to be worth handing over.
			name:      "a panel that carries only the brightness",
			output:    controlledPanel(supportedControls{Brightness: true}),
			published: true,
		},
		{
			name:      "a panel that carries only the power",
			output:    controlledPanel(supportedControls{Power: true}),
			published: true,
		},
		{
			// The node would answer nothing, so a claim on it parks
			// instead of receiving a wire with no panel behind it.
			name:   "a panel that answers no DDC/CI",
			output: controlledPanel(supportedControls{}),
		},
		{
			name:   "a connector with nothing on it",
			output: Output{Connector: "HDMI-A-1", Controls: supportedControls{Brightness: true, Power: true}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			published := deviceNames(sliceDevices([]Output{c.output}))
			if got := slices.Contains(published, "hdmi-a-1-control"); got != c.published {
				t.Errorf("the control device published = %v, want %v: %v", got, c.published, published)
			}
		})
	}
}

func TestControlDeviceCarriesTheOutputsTaints(t *testing.T) {
	// A control device is never claimable while the screen beside it
	// serves nobody, so it takes the output device's taints and never a
	// set of its own.
	control, published := controlDevice(controlledPanel(supportedControls{Power: true}), unservableTaints())

	if !published {
		t.Fatal("a panel that answers the power control published no control device")
	}
	if len(control.Taints) != 1 || control.Taints[0].Key != disconnectedTaint {
		t.Errorf("taints = %+v", control.Taints)
	}
}

func TestCompositorDownTaintsTheControlDeviceToo(t *testing.T) {
	// The pass that finds no compositor taints every device it
	// publishes, and the control device is in that list.
	devices := compositorDown(sliceDevices([]Output{controlledPanel(supportedControls{Brightness: true})}))

	if got := deviceNames(devices); !slices.Equal(got, []string{"hdmi-a-1", "hdmi-a-1-control"}) {
		t.Fatalf("devices = %v", got)
	}
	if len(devices[1].Taints) != 1 || devices[1].Taints[0].Effect != "NoExecute" {
		t.Errorf("taints = %+v", devices[1].Taints)
	}
}

func TestSliceDevicesTaintsAnOutputThatServesNobody(t *testing.T) {
	devices := sliceDevices(testOutputs(t))
	dark := devices[0]

	if dark.Name != "dp-1" {
		t.Fatalf("devices[0] = %q", dark.Name)
	}
	// The connector is a fact about the card, so it publishes whether
	// a monitor is on it or not. Everything else comes from an EDID
	// that no monitor answered.
	if _, ok := dark.Attributes["connector"]; !ok {
		t.Errorf("the connector attribute is missing: %+v", dark.Attributes)
	}
	for _, name := range []string{"manufacturer", "model", "serial", pairingAttribute, "widthPixels", "refreshMillihertz", "modes"} {
		if _, ok := dark.Attributes[name]; ok {
			t.Errorf("%s publishes for an output with no monitor: %+v", name, dark.Attributes)
		}
	}

	// The NoExecute taint is what a consumer tolerates with
	// tolerationSeconds, so a five second unplug does not end a video.
	if len(dark.Taints) != 1 {
		t.Fatalf("taints = %+v", dark.Taints)
	}
	if dark.Taints[0].Key != disconnectedTaint || dark.Taints[0].Effect != "NoExecute" {
		t.Errorf("taints[0] = %+v", dark.Taints[0])
	}
}

func TestSliceDevicesLeavesAConnectorThatGainedItsMonitorClear(t *testing.T) {
	// A monitor plugged into a connector that was dark at startup
	// routes like any other, because the config names every
	// connector. A device with a monitor on it has no taint.
	outputs := discoverOutputs(fakeSysfs(t, "card1", map[string]string{
		"HDMI-A-1": "lg-hdr-wqhd",
		"HDMI-A-2": "portable-display",
	}), "card1")
	devices := sliceDevices(outputs)

	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(devices))
	}
	hotplugged := devices[1]
	if hotplugged.Name != "hdmi-a-2" {
		t.Fatalf("devices[1] = %q", hotplugged.Name)
	}
	if _, ok := hotplugged.Attributes["model"]; !ok {
		t.Errorf("the monitor's facts are missing: %+v", hotplugged.Attributes)
	}
	for _, device := range devices {
		if len(device.Taints) != 0 {
			t.Errorf("%s has taints with a monitor on it: %+v", device.Name, device.Taints)
		}
	}
}

// The pod that dies taints every device as its compositor exits. That
// slice stays in the API until the next pod's first reconcile, after
// the socket appears, publishes the devices untainted again. dp-1
// stays tainted through that reconcile, because sysfs still reports
// it dark.
func TestTheFirstReconcileFreesTheScreensThatCameBack(t *testing.T) {
	devices := sliceDevices(testOutputs(t))
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-display.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  compositorDown(devices),
		},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), devices); err != nil {
		t.Fatal(err)
	}
	if fixture.updated == nil {
		t.Fatal("the slice was not replaced, so a stale one says every screen is dark")
	}
	wantTaints := map[string]int{"dp-1": 1, "hdmi-a-1": 0, "hdmi-a-2": 0}
	for _, device := range fixture.updated.Spec.Devices {
		if len(device.Taints) != wantTaints[device.Name] {
			t.Errorf("%s: taints = %+v, want %d of them",
				device.Name, device.Taints, wantTaints[device.Name])
		}
	}
}

// The operator publishes this form on every pass that finds no
// compositor answering, and every device carries the one NoExecute
// taint, because no output on this node can serve a client.
func TestCompositorDownTaintsEveryOutput(t *testing.T) {
	devices := compositorDown(sliceDevices(testOutputs(t)))

	if len(devices) != 3 {
		t.Fatalf("got %d devices, want 3", len(devices))
	}
	for _, device := range devices {
		if len(device.Taints) != 1 {
			t.Fatalf("%s: taints = %+v", device.Name, device.Taints)
		}
		if device.Taints[0].Key != disconnectedTaint || device.Taints[0].Effect != "NoExecute" {
			t.Errorf("%s: taints[0] = %+v", device.Name, device.Taints[0])
		}
		// The monitor's facts stay. What changed is that the output
		// cannot serve a client, not what is plugged in.
		if _, ok := device.Attributes["connector"]; !ok {
			t.Errorf("%s: the connector attribute left with the compositor", device.Name)
		}
	}
}

func TestPublishingCompositorDownEvictsTheClients(t *testing.T) {
	// This is the write the operator makes on the pass that finds the
	// compositor gone, and it is the only thing that ends the clients
	// whose connections died with it: the restarted compositor serves
	// the same devices again, and a slice that never changed would
	// raise no scheduler event.
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-display.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  sliceDevices(testOutputs(t)),
		},
	}}
	client := testClient(t, fixture.handler(t))

	devices := compositorDown(sliceDevices(testOutputs(t)))
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), devices); err != nil {
		t.Fatal(err)
	}
	if fixture.updated == nil {
		t.Fatal("the slice was not replaced, so nothing evicts the clients")
	}
	if fixture.updated.Spec.Pool.Generation != 4 {
		t.Errorf("generation = %d, want 4", fixture.updated.Spec.Pool.Generation)
	}
	for _, device := range fixture.updated.Spec.Devices {
		if len(device.Taints) != 1 {
			t.Errorf("%s went out untainted: %+v", device.Name, device.Taints)
		}
	}
}

func TestSliceDevicesPublishesAPanelWithNoName(t *testing.T) {
	// A built-in panel states no monitor name and no serial. The
	// attributes it does state must still publish, and the two it does
	// not must be absent rather than empty: has() answers false for an
	// absent attribute, and a selector that compares an empty string
	// matches every monitor that stated nothing.
	root := fakeSysfs(t, "card1", map[string]string{"eDP-1": "framework-edp"})
	devices := sliceDevices(discoverOutputs(root, "card1"))

	if len(devices) != 1 || devices[0].Name != "edp-1" {
		t.Fatalf("devices = %+v", devices)
	}
	attributes := devices[0].Attributes
	if _, ok := attributes["model"]; ok {
		t.Errorf("model publishes for a panel with no name: %+v", attributes)
	}
	if _, ok := attributes["serial"]; ok {
		t.Errorf("serial publishes for a panel with no serial: %+v", attributes)
	}
	if got := *attributes[pairingAttribute].String; got != "boe-095f" {
		t.Errorf("%s = %q", pairingAttribute, got)
	}
}

func TestSliceDevicesPublishesNoPairingIDWithoutAManufacturer(t *testing.T) {
	// A pairing identity with no manufacturer in it would match every
	// other monitor that also states none, so the attribute is absent
	// rather than partial. Everything else the monitor states still
	// publishes.
	devices := sliceDevices([]Output{{
		Connector: "HDMI-A-1",
		Connected: true,
		Monitor: EDID{
			ProductCode:  0x095f,
			ModelName:    "Panel",
			WidthPixels:  1920,
			HeightPixels: 1080,
		},
	}})

	attributes := devices[0].Attributes
	if _, ok := attributes[pairingAttribute]; ok {
		t.Errorf("%s publishes with no manufacturer: %+v", pairingAttribute, attributes)
	}
	if _, ok := attributes["model"]; !ok {
		t.Errorf("the model left with the pairing identity: %+v", attributes)
	}
	if _, ok := attributes["widthPixels"]; !ok {
		t.Errorf("the mode left with the pairing identity: %+v", attributes)
	}
}

func TestSameDevicesIgnoresTheServersTimestamp(t *testing.T) {
	// The API server fills TimeAdded in on every taint it stores. A
	// comparison that read it would call every pass a change, and
	// every slice write wakes every DRA-pending pod in the cluster.
	published := []SliceDevice{{
		Name:   "dp-1",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	current := []SliceDevice{{
		Name:   "dp-1",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}},
	}}
	if !sameDevices(published, current) {
		t.Fatal("a stored timestamp counted as a change")
	}
}

func TestSameDevicesSeesRealChanges(t *testing.T) {
	tainted := []SliceDevice{{
		Name:   "dp-1",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	clear := []SliceDevice{{Name: "dp-1"}}
	if sameDevices(tainted, clear) {
		t.Fatal("clearing a taint did not count as a change")
	}
	renamed := []SliceDevice{{Name: "hdmi-a-1"}}
	if sameDevices(clear, renamed) {
		t.Fatal("a different output did not count as a change")
	}
}

func TestEnsureCreatesTheSliceOnFirstPublish(t *testing.T) {
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), sliceDevices(testOutputs(t))); err != nil {
		t.Fatal(err)
	}
	if fixture.created == nil {
		t.Fatal("no slice was created")
	}
	slice := fixture.created
	// The driver name is the suffix, so liken's slice and this
	// operator's slice can both exist for one node.
	if slice.Metadata.Name != "liken-1-display.liken.sh" {
		t.Errorf("name = %q", slice.Metadata.Name)
	}
	if slice.Spec.Driver != DriverName || slice.Spec.NodeName != "liken-1" {
		t.Errorf("spec = %+v", slice.Spec)
	}
	if slice.Spec.Pool.Name != "liken-1" || slice.Spec.Pool.Generation != 1 || slice.Spec.Pool.ResourceSliceCount != 1 {
		t.Errorf("pool = %+v", slice.Spec.Pool)
	}
	// The Node owns the slice, so a node that leaves the cluster takes
	// the slice with it, and the pod's own restarts change nothing.
	if len(slice.Metadata.OwnerReferences) != 1 || slice.Metadata.OwnerReferences[0].UID != "abc-123" {
		t.Errorf("ownerReferences = %+v", slice.Metadata.OwnerReferences)
	}
	if len(slice.Spec.Devices) != 3 {
		t.Errorf("devices = %+v", slice.Spec.Devices)
	}
}

func TestEnsureLeavesAnUnchangedSliceAlone(t *testing.T) {
	devices := sliceDevices(testOutputs(t))
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-display.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  devices,
		},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), devices); err != nil {
		t.Fatal(err)
	}
	if fixture.created != nil || fixture.updated != nil {
		t.Errorf("an unchanged inventory must not write: %v", fixture.requests)
	}
}

func TestEnsureReplacesAChangedSliceAndBumpsTheGeneration(t *testing.T) {
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-display.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices: []SliceDevice{{
				Name:   "hdmi-a-1",
				Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
			}},
		},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), sliceDevices(testOutputs(t))); err != nil {
		t.Fatal(err)
	}
	if fixture.updated == nil {
		t.Fatal("the slice was not replaced")
	}
	if fixture.updated.Spec.Pool.Generation != 4 {
		t.Errorf("generation = %d, want 4", fixture.updated.Spec.Pool.Generation)
	}
	if fixture.updated.Metadata.ResourceVersion != "7" {
		t.Errorf("resourceVersion = %q; the write must carry the one it read",
			fixture.updated.Metadata.ResourceVersion)
	}
	if len(fixture.updated.Spec.Devices) != 3 {
		t.Errorf("devices = %+v", fixture.updated.Spec.Devices)
	}
}

// The next three tests read the line the publisher prints for each
// outcome. A slice that nobody rewrites and a slice that an operator
// died and left behind hold the same resourceVersion and the same pool
// generation, so the log is the only place the two come apart.

func TestEnsureLogsTheSliceItCreated(t *testing.T) {
	capture := captureSliceLog(t)
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), sliceDevices(testOutputs(t))); err != nil {
		t.Fatal(err)
	}
	// DP-1 is the connector with nothing plugged into it.
	want := "slice: created generation 1, 3 devices, 1 tainted: dp-1 has " + disconnectedTaint
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestEnsureLogsTheSliceItWrote(t *testing.T) {
	capture := captureSliceLog(t)
	devices := sliceDevices(testOutputs(t))
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-display.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  devices,
		},
	}}
	client := testClient(t, fixture.handler(t))

	// The compositor died, so every output takes the unservable
	// taint. The device count does not move, and the taints are the
	// whole event.
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), compositorDown(devices)); err != nil {
		t.Fatal(err)
	}
	want := "slice: wrote generation 4, 3 devices, 3 tainted: hdmi-a-1 gained " + disconnectedTaint +
		"; hdmi-a-2 gained " + disconnectedTaint
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestEnsureLogsThatNothingMoved(t *testing.T) {
	capture := captureSliceLog(t)
	devices := sliceDevices(testOutputs(t))
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-display.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  devices,
		},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), devices); err != nil {
		t.Fatal(err)
	}
	if fixture.updated != nil {
		t.Fatalf("an unchanged inventory wrote to the API: %v", fixture.requests)
	}
	want := "slice: unchanged at generation 3, 3 devices, 1 tainted (1 pass)"
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestEnsureRefusesToPublishAnEmptyInventory(t *testing.T) {
	// A card keeps its connectors until it leaves, so a pass that
	// found none read a card that is going away or read the wrong
	// path. Writing that would empty a slice whose devices consumers
	// hold, and an empty slice is a delete of every device in it.
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-display.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  sliceDevices(testOutputs(t)),
		},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), nil); err != ErrNoDevices {
		t.Fatalf("err = %v, want %v", err, ErrNoDevices)
	}
	if len(fixture.requests) != 0 {
		t.Errorf("an empty inventory reached the API server: %v", fixture.requests)
	}
	if fixture.deleted {
		t.Error("the slice was deleted")
	}
}

func TestEnsureRefusesMoreOutputsThanASliceHolds(t *testing.T) {
	devices := make([]SliceDevice, maxSliceDevices+1)
	for i := range devices {
		devices[i] = SliceDevice{Name: "output"}
	}
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), devices); err == nil {
		t.Fatal("an oversized slice was accepted")
	}
	if len(fixture.requests) != 0 {
		t.Errorf("an oversized slice reached the API server: %v", fixture.requests)
	}
}

func TestNodeOwnerReadsTheNodesUID(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/nodes/liken-1" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"metadata":{"name":"liken-1","uid":"abc-123"}}`))
	}))

	owner, err := NodeOwner(client, "liken-1")
	if err != nil {
		t.Fatal(err)
	}
	if owner != testOwner() {
		t.Fatalf("owner = %+v", owner)
	}
}
