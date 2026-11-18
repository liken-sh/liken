package main

// These tests cover which devices the slice publishes, and when
// EnsureResourceSlice creates, replaces, or leaves the slice alone.

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// slicePublishFixture is a fake API server that stores at most one
// ResourceSlice and records the requests it receives.
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
			t.Errorf("the fake API server received %s %s, want GET, POST, PUT, or DELETE", r.Method, r.URL.Path)
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
	// Three connectors, each with an output device and a draw device.
	if len(devices) != 6 {
		t.Fatalf("sliceDevices returned %d devices, want 6", len(devices))
	}
	// The devices are sorted by name, so the same hardware always
	// produces the same slice.
	wantNames := []string{"dp-1", "dp-1-draw", "hdmi-a-1", "hdmi-a-1-draw", "hdmi-a-2", "hdmi-a-2-draw"}
	if got := deviceNames(devices); !slices.Equal(got, wantNames) {
		t.Fatalf("names = %q, want %q", got, wantNames)
	}

	ultrawide := devices[2].Attributes
	want := map[string]string{
		"connector":      "HDMI-A-1",
		"appId":          "hdmi-a-1",
		"manufacturer":   "GSM",
		"model":          "LG HDR WQHD",
		"serial":         "202NTRLCC070",
		pairingAttribute: "gsm-7716-lg-hdr-wqhd",
		// The card's cable is in the ultrawide's second HDMI input, so
		// the EDID reports the physical address 2.0.0.0.
		"physicalAddress": "2.0.0.0",
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
	if len(devices[2].Taints) != 0 {
		t.Errorf("a connected monitor has taints: %+v", devices[2].Taints)
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
	// Both lists are longer than maxAttributeLength, so each ends on the
	// last whole mode name that fits. The kernel lists the preferred mode
	// first and the others from largest to smallest, so the dropped modes
	// are the smallest ones.
	devices := sliceDevices(testOutputs(t))

	want := "3840x1600 3840x2160 3440x1440 1920x1080 1680x1050 1600x900"
	if got := attributeText(t, devices[2], "modes"); got != want {
		t.Errorf("hdmi-a-1 modes = %q, want %q", got, want)
	}
	want = "1920x1080 1600x900 1280x800 1280x720 1024x768 800x600 720x480"
	if got := attributeText(t, devices[4], "modes"); got != want {
		t.Errorf("hdmi-a-2 modes = %q, want %q", got, want)
	}
}

type attributeListCase struct {
	values []string
	want   string
}

func TestAttributeListEndsOnTheLastWholeEntryThatFits(t *testing.T) {
	// Half of a mode name could match a different mode, so the cut
	// falls between entries.
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

// controlledPanel returns a connected output whose panel replied to the
// DDC/CI probe for the given controls.
func controlledPanel(controls supportedControls) Output {
	output := connectedOutput("HDMI-A-1", labMonitor())
	output.Controls = controls
	return output
}

func deviceNames(devices []SliceDevice) []string {
	names := make([]string, len(devices))
	for i, device := range devices {
		names[i] = device.Name
	}
	return names
}

func TestSliceDevicesPublishesAControlDeviceBesideTheOutput(t *testing.T) {
	devices := sliceDevices([]Output{controlledPanel(supportedControls{Brightness: true, Power: true})})

	if got := deviceNames(devices); !slices.Equal(got, []string{"hdmi-a-1", "hdmi-a-1-control", "hdmi-a-1-draw"}) {
		t.Fatalf("device names are %v, want hdmi-a-1, its control device, and its draw device", got)
	}
	output, control := devices[0], devices[1]
	// Both devices publish the monitor identity, so one claim can
	// request a screen and its control device with a matchAttribute
	// constraint.
	if got, want := attributeText(t, control, pairingAttribute), attributeText(t, output, pairingAttribute); got != want {
		t.Errorf("%s = %q, want %q", pairingAttribute, got, want)
	}
	if got := attributeText(t, control, "connector"); got != "HDMI-A-1" {
		t.Errorf("connector = %q, want HDMI-A-1", got)
	}
	for _, name := range []string{"controlsBrightness", "controlsPower"} {
		attribute, published := control.Attributes[name]
		if !published || attribute.Bool == nil || !*attribute.Bool {
			t.Errorf("%s = %+v, want it published and true", name, attribute)
		}
	}
	// The output DeviceClass selects on has(appId), so the control
	// device publishes no appId.
	if _, published := control.Attributes["appId"]; published {
		t.Errorf("the control device publishes an appId: %+v", control.Attributes)
	}
	// A DeviceClass selects on the control marker, and only the control
	// device has it.
	if attribute, published := control.Attributes["control"]; !published || attribute.Bool == nil || !*attribute.Bool {
		t.Errorf("control = %+v, want it published and true", attribute)
	}
	if _, published := output.Attributes["control"]; published {
		t.Errorf("the output device publishes the control marker: %+v", output.Attributes)
	}
	if len(control.Taints) != 0 {
		t.Errorf("the control device has taints %+v, want none", control.Taints)
	}
}

func TestSliceDevicesPublishesAControlDeviceOnlyForAPanelThatAnswered(t *testing.T) {
	cases := []struct {
		name      string
		output    Output
		published bool
	}{
		{
			name:      "a panel that supports both controls",
			output:    controlledPanel(supportedControls{Brightness: true, Power: true}),
			published: true,
		},
		{
			// One control is enough to publish a control device.
			name:      "a panel that supports only the brightness",
			output:    controlledPanel(supportedControls{Brightness: true}),
			published: true,
		},
		{
			name:      "a panel that supports only the power",
			output:    controlledPanel(supportedControls{Power: true}),
			published: true,
		},
		{
			name:   "a panel that does not respond to DDC/CI",
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

func TestSliceDevicesPublishesADrawDeviceBesideTheOutput(t *testing.T) {
	monitor := EDID{Manufacturer: "GSM", ProductCode: 0x7716, ModelName: "LG HDR WQHD", Serial: "202NTRLCC070"}
	devices := sliceDevices([]Output{connectedOutput("HDMI-A-1", monitor)})

	if got := deviceNames(devices); !slices.Equal(got, []string{"hdmi-a-1", "hdmi-a-1-draw"}) {
		t.Fatalf("device names are %v, want hdmi-a-1 and hdmi-a-1-draw", got)
	}
	output, draw := devices[0], devices[1]

	if draw.AllowMultipleAllocations == nil || !*draw.AllowMultipleAllocations {
		t.Errorf("allowMultipleAllocations = %+v, want it published and true", draw.AllowMultipleAllocations)
	}
	if output.AllowMultipleAllocations != nil {
		t.Errorf("the output device is shared: %+v", output.AllowMultipleAllocations)
	}
	// A DeviceClass selects on the draw marker, and only the draw
	// device has it.
	if attribute, published := draw.Attributes["draw"]; !published || attribute.Bool == nil || !*attribute.Bool {
		t.Errorf("draw = %+v, want it published and true", attribute)
	}
	if _, published := output.Attributes["draw"]; published {
		t.Errorf("the output device publishes the draw marker: %+v", output.Attributes)
	}
	for _, name := range []string{pairingAttribute, "manufacturer", "model", "serial"} {
		if got, want := attributeText(t, draw, name), attributeText(t, output, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	// The output DeviceClass selects on has(appId), so the draw device
	// publishes no appId.
	if _, published := draw.Attributes["appId"]; published {
		t.Errorf("the draw device publishes an appId: %+v", draw.Attributes)
	}
	if len(draw.Taints) != 0 {
		t.Errorf("the draw device has taints %+v, want none", draw.Taints)
	}
}

func TestDrawDeviceCarriesTheOutputsTaints(t *testing.T) {
	// A connector with no monitor has the disconnected taint on its
	// output device and its draw device.
	devices := sliceDevices([]Output{{Connector: "DP-1"}})

	if got := deviceNames(devices); !slices.Equal(got, []string{"dp-1", "dp-1-draw"}) {
		t.Fatalf("device names are %v, want dp-1 and dp-1-draw", got)
	}
	draw := devices[1]
	if len(draw.Taints) != 1 || draw.Taints[0].Key != disconnectedTaint {
		t.Errorf("the draw device has taints %+v, want none", draw.Taints)
	}
}

func TestControlDeviceCarriesTheOutputsTaints(t *testing.T) {
	control, published := controlDevice(controlledPanel(supportedControls{Power: true}), disconnectedTaints())

	if !published {
		t.Fatal("a panel that supports the power control published no control device")
	}
	if len(control.Taints) != 1 || control.Taints[0].Key != disconnectedTaint {
		t.Errorf("the control device has taints %+v, want none", control.Taints)
	}
}

func TestSliceDevicesTaintsAnOutputThatServesNobody(t *testing.T) {
	devices := sliceDevices(testOutputs(t))
	empty := devices[0]

	if empty.Name != "dp-1" {
		t.Fatalf("devices[0] is %q, want dp-1", empty.Name)
	}
	// The connector attribute comes from the card, so it is published
	// with no monitor. The attributes in the loop come from the EDID.
	if _, ok := empty.Attributes["connector"]; !ok {
		t.Errorf("the connector attribute is missing: %+v", empty.Attributes)
	}
	for _, name := range []string{"manufacturer", "model", "serial", pairingAttribute, "widthPixels", "refreshMillihertz", "modes"} {
		if _, ok := empty.Attributes[name]; ok {
			t.Errorf("%s publishes for an output with no monitor: %+v", name, empty.Attributes)
		}
	}

	if len(empty.Taints) != 1 {
		t.Fatalf("dp-1 has taints %+v, want exactly one", empty.Taints)
	}
	if empty.Taints[0].Key != disconnectedTaint || empty.Taints[0].Effect != "NoExecute" {
		t.Errorf("taints[0] = %+v, want key %s with effect NoExecute", empty.Taints[0], disconnectedTaint)
	}
}

// The physical address comes from the EDID on the wire, never from the
// monitor that a disconnected connector remembers.
func TestSliceDevicesPublishesThePhysicalAddressOnlyFromTheWire(t *testing.T) {
	monitor := EDID{Manufacturer: "DON", ProductCode: 0x0001, ModelName: "DENON-AVR", PhysicalAddress: "1.2.0.0"}
	cases := []struct {
		name   string
		output Output
		want   string
	}{
		{
			name:   "a connector that serves the EDID",
			output: Output{Connector: "HDMI-A-1", Connected: true, Monitor: monitor},
			want:   "1.2.0.0",
		},
		{
			name:   "a disconnected connector that remembers the monitor",
			output: Output{Connector: "HDMI-A-1", Remembered: monitor},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			device := sliceDevices([]Output{c.output})[0]

			// An absent attribute reads as the empty string, which is
			// the expected value for a case that wants none.
			got := device.Attributes["physicalAddress"].String
			if got == nil {
				got = new(string)
			}
			if *got != c.want {
				t.Errorf("physicalAddress = %q, want %q", *got, c.want)
			}
		})
	}
}

// Two connectors report the same monitor with different physical
// addresses when one cable runs to a receiver input and a second cable
// runs through a Pulse-Eight CEC adapter to another input of the same
// receiver. A CEC client that read either address could switch the
// receiver to the wrong input, so neither device publishes one.
func TestSliceDevicesPublishesNoAddressForAnAmbiguousMonitor(t *testing.T) {
	monitor := EDID{Manufacturer: "DON", ProductCode: 0x0001, ModelName: "DENON-AVR"}
	served := monitor
	served.PhysicalAddress = "1.2.0.0"
	adapted := monitor
	adapted.PhysicalAddress = "1.1.0.0"
	outputs := []Output{
		{Connector: "HDMI-A-1", Connected: true, Monitor: served},
		{Connector: "HDMI-A-2", Connected: true, Monitor: adapted},
	}

	devices := sliceDevices(outputs)

	for _, name := range []string{"hdmi-a-1", "hdmi-a-2"} {
		index := slices.IndexFunc(devices, func(d SliceDevice) bool { return d.Name == name })
		if index < 0 {
			t.Fatalf("sliceDevices published no %s", name)
		}
		if got := devices[index].Attributes["physicalAddress"].String; got != nil {
			t.Errorf("%s publishes physicalAddress %q, want none while the two connectors disagree", name, *got)
		}
	}
}

// When both connectors read the same address, each publishes it.
func TestSliceDevicesPublishesTheAddressWhenTwoConnectorsAgree(t *testing.T) {
	monitor := EDID{Manufacturer: "DON", ProductCode: 0x0001, ModelName: "DENON-AVR", PhysicalAddress: "1.2.0.0"}
	outputs := []Output{
		{Connector: "HDMI-A-1", Connected: true, Monitor: monitor},
		{Connector: "HDMI-A-2", Connected: true, Monitor: monitor},
	}

	devices := sliceDevices(outputs)

	for _, name := range []string{"hdmi-a-1", "hdmi-a-2"} {
		index := slices.IndexFunc(devices, func(d SliceDevice) bool { return d.Name == name })
		if index < 0 {
			t.Fatalf("sliceDevices published no %s", name)
		}
		got := devices[index].Attributes["physicalAddress"].String
		if got == nil || *got != "1.2.0.0" {
			t.Errorf("%s publishes physicalAddress %v, want 1.2.0.0", name, got)
		}
	}
}

func TestSliceDevicesLeavesAConnectorThatGainedItsMonitorClear(t *testing.T) {
	// A monitor connected to a connector that had none at startup gets no
	// taint, because the compositor configuration has a section for every
	// connector.
	outputs := discoverOutputs(fakeSysfs(t, "card1", map[string]string{
		"HDMI-A-1": "lg-hdr-wqhd",
		"HDMI-A-2": "portable-display",
	}), "card1")
	devices := sliceDevices(outputs)

	// Two connectors, each with an output device and a draw device.
	if len(devices) != 4 {
		t.Fatalf("sliceDevices returned %d devices, want 4", len(devices))
	}
	hotplugged := devices[2]
	if hotplugged.Name != "hdmi-a-2" {
		t.Fatalf("devices[2] = %q", hotplugged.Name)
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

// When the operator pod stops, its compositor exits and the slice keeps
// the taint on every output and draw device. The next pod's first pass
// after the compositor socket appears removes the taint from each
// output with a monitor. dp-1 keeps its taint, because sysfs still
// reports it disconnected.
func TestTheFirstReconcileFreesTheScreensThatCameBack(t *testing.T) {
	devices := sliceDevices(testOutputs(t))
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-display.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  compositorDown(devices, nil),
		},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), devices); err != nil {
		t.Fatalf("publishing the slice: %v", err)
	}
	if fixture.updated == nil {
		t.Fatal("the slice was not replaced, so a stale one reports every screen as disconnected")
	}
	wantTaints := map[string]int{
		"dp-1": 1, "dp-1-draw": 1,
		"hdmi-a-1": 0, "hdmi-a-1-draw": 0,
		"hdmi-a-2": 0, "hdmi-a-2-draw": 0,
	}
	for _, device := range fixture.updated.Spec.Devices {
		if len(device.Taints) != wantTaints[device.Name] {
			t.Errorf("%s: taints = %+v, want %d of them",
				device.Name, device.Taints, wantTaints[device.Name])
		}
	}
}

// This fixture has no control device, so every device gets the
// NoExecute taint.
func TestCompositorDownTaintsEveryOutput(t *testing.T) {
	devices := compositorDown(sliceDevices(testOutputs(t)), nil)

	if len(devices) != 6 {
		t.Fatalf("sliceDevices returned %d devices, want 6", len(devices))
	}
	for _, device := range devices {
		if len(device.Taints) != 1 {
			t.Fatalf("%s: taints = %+v, want exactly one", device.Name, device.Taints)
		}
		if device.Taints[0].Key != disconnectedTaint || device.Taints[0].Effect != "NoExecute" {
			t.Errorf("%s: taints[0] = %+v, want the compositor-down taint", device.Name, device.Taints[0])
		}
		// compositorDown changes only the taints.
		if _, ok := device.Attributes["connector"]; !ok {
			t.Errorf("%s: the connector attribute left with the compositor", device.Name)
		}
	}
}

func TestPublishingCompositorDownEvictsTheClients(t *testing.T) {
	// The write must happen even though the restarted compositor
	// serves the same devices, because the taint is what makes the
	// taint-eviction controller delete the clients that lost their
	// connections.
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

	devices := compositorDown(sliceDevices(testOutputs(t)), nil)
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), devices); err != nil {
		t.Fatalf("publishing the slice: %v", err)
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
	// A built-in panel reports no monitor name and no serial. The model
	// and serial attributes are absent, not empty, and the others are
	// published.
	root := fakeSysfs(t, "card1", map[string]string{"eDP-1": "framework-edp"})
	devices := sliceDevices(discoverOutputs(root, "card1"))

	if len(devices) != 2 || devices[0].Name != "edp-1" {
		t.Fatalf("devices = %+v, want edp-1 and one more", devices)
	}
	attributes := devices[0].Attributes
	if _, ok := attributes["model"]; ok {
		t.Errorf("model publishes for a panel with no name: %+v", attributes)
	}
	if _, ok := attributes["serial"]; ok {
		t.Errorf("serial publishes for a panel with no serial: %+v", attributes)
	}
	if got := *attributes[pairingAttribute].String; got != "boe-095f" {
		t.Errorf("%s = %q, want boe-095f", pairingAttribute, got)
	}
}

func TestSliceDevicesPublishesNoPairingIDWithoutAManufacturer(t *testing.T) {
	// An identity with no manufacturer would match every other monitor
	// that has none, so the pairing attribute is absent.
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
	published := []SliceDevice{{
		Name:   "dp-1",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	current := []SliceDevice{{
		Name:   "dp-1",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}},
	}}
	if !sameDevices(published, current) {
		t.Fatal("sameDevices is false when only the server timestamp differs, want true")
	}
}

func TestSameDevicesSeesRealChanges(t *testing.T) {
	tainted := []SliceDevice{{
		Name:   "dp-1",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	clear := []SliceDevice{{Name: "dp-1"}}
	if sameDevices(tainted, clear) {
		t.Fatal("sameDevices is true after a taint was cleared, want false")
	}
	renamed := []SliceDevice{{Name: "hdmi-a-1"}}
	if sameDevices(clear, renamed) {
		t.Fatal("sameDevices is true for a different output, want false")
	}
}

func TestEnsureCreatesTheSliceOnFirstPublish(t *testing.T) {
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), sliceDevices(testOutputs(t))); err != nil {
		t.Fatalf("publishing the slice: %v", err)
	}
	if fixture.created == nil {
		t.Fatal("EnsureResourceSlice created no slice, want a POST")
	}
	slice := fixture.created
	// The driver name is the suffix, so the name differs from the slice
	// that `liken` publishes for the same node.
	if slice.Metadata.Name != "liken-1-display.liken.sh" {
		t.Errorf("name = %q, want liken-1-display.liken.sh", slice.Metadata.Name)
	}
	if slice.Spec.Driver != DriverName || slice.Spec.NodeName != "liken-1" {
		t.Errorf("spec = %+v, want driver %s on node liken-1", slice.Spec, DriverName)
	}
	if slice.Spec.Pool.Name != "liken-1" || slice.Spec.Pool.Generation != 1 || slice.Spec.Pool.ResourceSliceCount != 1 {
		t.Errorf("pool = %+v, want name liken-1, generation 1, and 1 slice", slice.Spec.Pool)
	}
	// The Node owns the slice, so Kubernetes deletes it with the Node.
	if len(slice.Metadata.OwnerReferences) != 1 || slice.Metadata.OwnerReferences[0].UID != "abc-123" {
		t.Errorf("ownerReferences = %+v, want the Node with UID abc-123", slice.Metadata.OwnerReferences)
	}
	if len(slice.Spec.Devices) != 6 {
		t.Errorf("the slice has devices %+v, want 6", slice.Spec.Devices)
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
		t.Fatalf("publishing the slice: %v", err)
	}
	if fixture.created != nil || fixture.updated != nil {
		t.Errorf("an unchanged inventory sent %v, want no write", fixture.requests)
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
		t.Fatalf("publishing the slice: %v", err)
	}
	if fixture.updated == nil {
		t.Fatal("EnsureResourceSlice did not replace the slice, want a PUT")
	}
	if fixture.updated.Spec.Pool.Generation != 4 {
		t.Errorf("generation = %d, want 4", fixture.updated.Spec.Pool.Generation)
	}
	if fixture.updated.Metadata.ResourceVersion != "7" {
		t.Errorf("resourceVersion = %q, want the one the GET returned",
			fixture.updated.Metadata.ResourceVersion)
	}
	if len(fixture.updated.Spec.Devices) != 6 {
		t.Errorf("the slice has devices %+v, want 6", fixture.updated.Spec.Devices)
	}
}

// The tests below check the log line for each outcome. A slice that
// the operator checked and left unchanged has the same resourceVersion
// and pool generation as one that a stopped operator left behind, so
// only the log line tells them apart.

func TestEnsureLogsTheSliceItCreated(t *testing.T) {
	capture := captureSliceLog(t)
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), sliceDevices(testOutputs(t))); err != nil {
		t.Fatalf("publishing the slice: %v", err)
	}
	// DP-1 has no monitor, so its output device and its draw device
	// have the taint.
	want := "slice: created generation 1, 6 devices, 2 tainted: dp-1 has " + disconnectedTaint +
		"; dp-1-draw has " + disconnectedTaint
	if got := capture.only(t); got != want {
		t.Errorf("log line = %q, want %q", got, want)
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

	// The compositor exited, so every output and draw device gets the
	// disconnected taint.
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), compositorDown(devices, nil)); err != nil {
		t.Fatalf("publishing the slice: %v", err)
	}
	want := "slice: wrote generation 4, 6 devices, 6 tainted: hdmi-a-1 gained " + disconnectedTaint +
		"; hdmi-a-1-draw gained " + disconnectedTaint +
		"; hdmi-a-2 gained " + disconnectedTaint +
		"; hdmi-a-2-draw gained " + disconnectedTaint
	if got := capture.only(t); got != want {
		t.Errorf("log line = %q, want %q", got, want)
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
		t.Fatalf("publishing the slice: %v", err)
	}
	if fixture.updated != nil {
		t.Fatalf("an unchanged inventory sent %v, want no write", fixture.requests)
	}
	want := "slice: unchanged at generation 3, 6 devices, 2 tainted (1 pass)"
	if got := capture.only(t); got != want {
		t.Errorf("log line = %q, want %q", got, want)
	}
}

func TestEnsureRefusesToPublishAnEmptyInventory(t *testing.T) {
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
		t.Errorf("an empty inventory sent %v, want no request", fixture.requests)
	}
	if fixture.deleted {
		t.Error("an empty inventory deleted the slice, want it kept")
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
		t.Fatal("EnsureResourceSlice returned no error for more devices than one slice holds")
	}
	if len(fixture.requests) != 0 {
		t.Errorf("an oversized inventory sent %v, want no request", fixture.requests)
	}
}

func TestNodeOwnerReadsTheNodesUID(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/nodes/liken-1" {
			t.Errorf("path = %q, want /api/v1/nodes/liken-1", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"metadata":{"name":"liken-1","uid":"abc-123"}}`))
	}))

	owner, err := NodeOwner(client, "liken-1")
	if err != nil {
		t.Fatalf("reading the owner of the Node: %v", err)
	}
	if owner != testOwner() {
		t.Fatalf("owner = %+v, want %+v", owner, testOwner())
	}
}
