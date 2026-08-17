package main

// These tests cover two decisions: what one output publishes as a
// device, and when the operator writes the slice at all. The second
// set runs against a small API server that holds one ResourceSlice
// and remembers the requests it received.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	// The credentials are empty, so the client sends no bearer token
	// and reads no file from disk.
	return NewClient(server.URL, server.Client(), "")
}

func testOwner() OwnerReference {
	return OwnerReference{APIVersion: "v1", Kind: "Node", Name: "liken-1", UID: "abc-123"}
}

func hdmiOutput(t *testing.T, card, pcm int) alsaOutput {
	t.Helper()
	block, err := parseELD(fixture(t, "eld-hdmi-lg-ultrawide.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return alsaOutput{Card: card, PCM: pcm, HDMI: true, Monitor: true, ELD: block}
}

func TestSliceDevicesPublishesEachOutput(t *testing.T) {
	outputs := []alsaOutput{
		{Card: 0, PCM: 0},
		hdmiOutput(t, 0, 3),
	}
	sinks := map[pcmAddress]string{
		{Card: 0, PCM: 0}: "alsa_output.pci-0000_00_1f.3.analog-stereo",
		{Card: 0, PCM: 3}: "alsa_output.pci-0000_00_1f.3.hdmi-stereo",
	}

	devices := sliceDevices(outputs, sinks)
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(devices))
	}
	// The list is sorted, so the same hardware always makes the same
	// slice and the change detection sees real changes only.
	if devices[0].Name != "card0-pcm0" || devices[1].Name != "card0-pcm3" {
		t.Fatalf("names = %q, %q", devices[0].Name, devices[1].Name)
	}

	analog := devices[0]
	if got := *analog.Attributes["connectionType"].String; got != "analog" {
		t.Errorf("the analog output's connection type = %q", got)
	}
	// A selector cannot read a device's name, so the name is an
	// attribute as well, and its two halves are numbers.
	if got := stringAttribute(t, analog, "output"); got != "card0-pcm0" {
		t.Errorf("output = %q", got)
	}
	if got := intAttribute(t, analog, "card"); got != 0 {
		t.Errorf("card = %d", got)
	}
	if got := intAttribute(t, analog, "pcm"); got != 0 {
		t.Errorf("pcm = %d", got)
	}
	if _, published := analog.Attributes[PairingAttribute]; published {
		t.Error("the analog output published a pairing attribute")
	}
	if len(analog.Taints) != 0 {
		t.Errorf("the analog output carries taints: %+v", analog.Taints)
	}

	hdmi := devices[1]
	attributes := map[string]string{
		"output":         "card0-pcm3",
		"connectionType": "hdmi",
		"manufacturer":   "GSM",
		"product":        "5b09",
		"monitorName":    "LG ULTRAWIDE",
		"speakers":       "FL/FR",
		"sinkName":       "alsa_output.pci-0000_00_1f.3.hdmi-stereo",
		PairingAttribute: "gsm-5b09-lg-ultrawide",
	}
	for name, want := range attributes {
		attribute, published := hdmi.Attributes[name]
		if !published {
			t.Errorf("the HDMI output published no %s", name)
			continue
		}
		if got := *attribute.String; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := intAttribute(t, hdmi, "lpcmChannels"); got != 2 {
		t.Errorf("lpcmChannels = %d, want 2", got)
	}
	if got := intAttribute(t, hdmi, "pcm"); got != 3 {
		t.Errorf("pcm = %d, want 3", got)
	}
	if len(hdmi.Taints) != 0 {
		t.Errorf("the HDMI output carries taints: %+v", hdmi.Taints)
	}
}

func TestSliceDevicesTaintsAnOutputThatCannotPlay(t *testing.T) {
	// Each case names the taints the output must carry, in the order
	// the slice publishes them. The NoExecute taint says the output
	// cannot serve a stream now, and each NoSchedule taint names one
	// reason, so the set states the whole condition.
	cases := []struct {
		name   string
		output alsaOutput
		sink   bool
		taints []DeviceTaint
	}{
		{
			name:   "an HDMI output with a monitor and a sink",
			output: alsaOutput{Card: 0, PCM: 3, HDMI: true, Monitor: true},
			sink:   true,
		},
		{
			name:   "the analog jack with a sink",
			output: alsaOutput{Card: 0, PCM: 0},
			sink:   true,
		},
		{
			name:   "an unplugged monitor whose sink node is still there",
			output: alsaOutput{Card: 0, PCM: 3, HDMI: true},
			sink:   true,
			taints: []DeviceTaint{
				{Key: disconnectedTaint, Effect: "NoExecute"},
				{Key: noMonitorTaint, Effect: "NoSchedule"},
			},
		},
		{
			name:   "an output with a monitor and no sink node",
			output: alsaOutput{Card: 0, PCM: 3, HDMI: true, Monitor: true},
			taints: []DeviceTaint{
				{Key: disconnectedTaint, Effect: "NoExecute"},
				{Key: noSinkTaint, Effect: "NoSchedule"},
			},
		},
		{
			name:   "the analog jack with no sink node",
			output: alsaOutput{Card: 0, PCM: 0},
			taints: []DeviceTaint{
				{Key: disconnectedTaint, Effect: "NoExecute"},
				{Key: noSinkTaint, Effect: "NoSchedule"},
			},
		},
		{
			name:   "an unplugged monitor with no sink node either",
			output: alsaOutput{Card: 0, PCM: 3, HDMI: true},
			taints: []DeviceTaint{
				{Key: disconnectedTaint, Effect: "NoExecute"},
				{Key: noMonitorTaint, Effect: "NoSchedule"},
				{Key: noSinkTaint, Effect: "NoSchedule"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sinks := map[pcmAddress]string{}
			if c.sink {
				sinks[pcmAddress{Card: c.output.Card, PCM: c.output.PCM}] = "alsa_output.test"
			}

			devices := sliceDevices([]alsaOutput{c.output}, sinks)
			if got := devices[0].Taints; !reflect.DeepEqual(got, c.taints) {
				t.Fatalf("taints = %+v, want %+v", got, c.taints)
			}
		})
	}
}

// The sink name and the no-sink taint come from one fact, so a device
// that publishes the name of its sink must never also say it has none.
// The pair is what a reader of the slice cannot resolve: the name says
// a claim would play, and the taint says a claim would reach nothing.
func TestSliceDevicesNeverPublishesASinkNameAndTheNoSinkTaint(t *testing.T) {
	outputs := []alsaOutput{
		{Card: 0, PCM: 0},
		{Card: 0, PCM: 3, HDMI: true, Monitor: true},
		{Card: 0, PCM: 8, HDMI: true},
		{Card: 0, PCM: 9, HDMI: true},
	}
	// Every declared node is in the graph, which is what PipeWire holds
	// once it has loaded the operator's drop-in, whether a monitor is on
	// the port or not.
	sinks := map[pcmAddress]string{}
	for _, output := range outputs {
		sinks[pcmAddress{Card: output.Card, PCM: output.PCM}] = sinkNodeName(output.Card, output.PCM)
	}

	for _, device := range sliceDevices(outputs, sinks) {
		name, named := device.Attributes["sinkName"]
		if !named {
			t.Errorf("%s published no sink name for a node in the graph", device.Name)
			continue
		}
		for _, taint := range device.Taints {
			if taint.Key == noSinkTaint {
				t.Errorf("%s published sinkName %q and the no-sink taint",
					device.Name, *name.String)
			}
		}
	}
}

func TestSliceDevicesOmitsASinkNameTooLongToPublish(t *testing.T) {
	// A truncated name would name nothing, and the delivery reads the
	// current name from PipeWire rather than from the slice.
	long := ""
	for range maxAttributeLength + 1 {
		long += "x"
	}
	devices := sliceDevices(
		[]alsaOutput{{Card: 0, PCM: 0}},
		map[pcmAddress]string{{Card: 0, PCM: 0}: long},
	)
	if _, published := devices[0].Attributes["sinkName"]; published {
		t.Fatal("a name past the attribute limit published anyway")
	}
	if len(devices[0].Taints) != 0 {
		t.Fatalf("an output with a sink was tainted: %+v", devices[0].Taints)
	}
}

func TestSameDevicesIgnoresTheServersTimestamp(t *testing.T) {
	// The API server fills TimeAdded in on every taint it stores. A
	// comparison that read it would call every pass a change, and every
	// slice write wakes every DRA-pending pod in the cluster.
	published := []SliceDevice{{
		Name:   "card0-pcm3",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	current := []SliceDevice{{
		Name:   "card0-pcm3",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}},
	}}
	if !sameDevices(published, current) {
		t.Fatal("a stored timestamp counted as a change")
	}
}

func TestSameDevicesSeesRealChanges(t *testing.T) {
	tainted := []SliceDevice{{
		Name:   "card0-pcm3",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	clear := []SliceDevice{{Name: "card0-pcm3"}}
	if sameDevices(tainted, clear) {
		t.Fatal("clearing a taint did not count as a change")
	}
	renamed := []SliceDevice{{Name: "card0-pcm7"}}
	if sameDevices(clear, renamed) {
		t.Fatal("a different output did not count as a change")
	}
	relabeled := []SliceDevice{{
		Name:       "card0-pcm3",
		Attributes: map[string]DeviceAttribute{"sinkName": AttrString("alsa_output.test")},
	}}
	if sameDevices(clear, relabeled) {
		t.Fatal("a new attribute did not count as a change")
	}
}

// slicePublishFixture is a small API server that holds at most one
// ResourceSlice. It remembers the requests it received.
type slicePublishFixture struct {
	existing *ResourceSlice
	requests []string
	created  *ResourceSlice
	updated  *ResourceSlice
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
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

func testDevices() []SliceDevice {
	return []SliceDevice{{
		Name:       "card0-pcm3",
		Attributes: map[string]DeviceAttribute{"connectionType": AttrString("hdmi")},
	}}
}

func TestEnsureCreatesTheSliceOnFirstPublish(t *testing.T) {
	api := &slicePublishFixture{}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), testDevices()); err != nil {
		t.Fatal(err)
	}
	if api.created == nil {
		t.Fatal("no slice was created")
	}
	slice := api.created
	if slice.Metadata.Name != "liken-1-audio.liken.sh" {
		t.Errorf("name = %q", slice.Metadata.Name)
	}
	if slice.Spec.Driver != "audio.liken.sh" || slice.Spec.NodeName != "liken-1" {
		t.Errorf("spec = %+v", slice.Spec)
	}
	if slice.Spec.Pool.Name != "liken-1" || slice.Spec.Pool.Generation != 1 || slice.Spec.Pool.ResourceSliceCount != 1 {
		t.Errorf("pool = %+v", slice.Spec.Pool)
	}
	// The Node owns the slice, not the pod, so the slice outlives an
	// operator restart and leaves with the machine.
	if len(slice.Metadata.OwnerReferences) != 1 || slice.Metadata.OwnerReferences[0].UID != "abc-123" {
		t.Errorf("ownerReferences = %+v", slice.Metadata.OwnerReferences)
	}
}

func TestEnsureLeavesAnUnchangedSliceAlone(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), testDevices()); err != nil {
		t.Fatal(err)
	}
	if api.created != nil || api.updated != nil {
		t.Errorf("an unchanged inventory must not write: %v", api.requests)
	}
}

func TestEnsureReplacesAChangedSliceAndBumpsTheGeneration(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	changed := testDevices()
	changed[0].Taints = []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}}
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), changed); err != nil {
		t.Fatal(err)
	}
	if api.updated == nil {
		t.Fatal("a changed inventory did not write")
	}
	if got := api.updated.Spec.Pool.Generation; got != 4 {
		t.Errorf("generation = %d, want 4", got)
	}
	if len(api.updated.Spec.Devices[0].Taints) != 1 {
		t.Errorf("devices = %+v", api.updated.Spec.Devices)
	}
}

// An empty list is never a fact about a machine this operator runs
// on, because the pod holds an exclusive claim on a card and a card
// has playback PCM devices. Publishing one would retract devices that
// prepared claims still name.
func TestEnsureRefusesToPublishNothing(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), nil); err != ErrNoDevices {
		t.Fatalf("error = %v, want %v", err, ErrNoDevices)
	}
	if len(api.requests) != 0 {
		t.Errorf("an empty inventory reached the API server: %v", api.requests)
	}
}

func publishedSlice(devices []SliceDevice, generation int64) *ResourceSlice {
	return &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-audio.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: generation, ResourceSliceCount: 1},
			Devices:  devices,
		},
	}
}

func TestSliceName(t *testing.T) {
	// The driver name is the suffix, so liken's slice and this
	// operator's slice can both exist for one node.
	if got := sliceName("liken-1"); got != "liken-1-audio.liken.sh" {
		t.Fatalf("sliceName = %q", got)
	}
}

// stringAttribute reads one string attribute, and reports a device
// that does not publish it as a failure rather than a panic.
func stringAttribute(t *testing.T, device SliceDevice, name string) string {
	t.Helper()
	attribute, published := device.Attributes[name]
	if !published || attribute.String == nil {
		t.Fatalf("%s publishes no string attribute %q", device.Name, name)
	}
	return *attribute.String
}

// intAttribute reads one number attribute.
func intAttribute(t *testing.T, device SliceDevice, name string) int64 {
	t.Helper()
	attribute, published := device.Attributes[name]
	if !published || attribute.Int == nil {
		t.Fatalf("%s publishes no number attribute %q", device.Name, name)
	}
	return *attribute.Int
}
