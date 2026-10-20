package main

// The world a test runs the operator in: the fake API server, the
// operator's loop, and builders for the objects that machine-operator,
// a person, and the kubelet write. The server answers over the
// in-memory connections of apiservertest, so a test runs in a synctest
// bubble, and a five-minute deadline takes no real time.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
)

// world is one running operator, the server it talks to, and the LVFS
// it reads catalogs from.
type world struct {
	api  *fakeAPI
	lvfs *fakeLVFS
}

// discovery answers the operator's settings for discovery, with the
// world's LVFS as the catalogs' server.
func (w *world) discovery() discoverySettings {
	if w.lvfs == nil {
		w.lvfs = newFakeLVFS()
	}
	return discoverySettings{scanImage: testScanImage, interval: time.Hour, http: &http.Client{Transport: w.lvfs}}
}

// testScanImage is the scan image of the test operator.
const testScanImage = "ghcr.io/liken-sh/firmware-operator-scan:test"

// startWorld runs the operator until the test ends. The test runs in a
// synctest bubble.
func startWorld(t *testing.T) *world {
	w := &world{api: startFakeAPI(t)}
	w.run(t)
	return w
}

// run starts a copy of the operator's loop against the world's server,
// and answers the function that stops it and waits for it to end.
func (w *world) run(t *testing.T) (stop func()) {
	client := apiclient.New(apiservertest.Host, w.api.server.Client(), "").WithContext(t.Context())
	watcher, err := dynamic.NewForConfig(w.api.server.Config())
	if err != nil {
		t.Fatal(err)
	}
	recorder := events.New(t.Context(), client, component, events.Options{Instance: operatorPodName, Log: io.Discard})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		newOperator(client, operatorNS, recorder, w.discovery()).run(ctx, watcher)
	}()
	stop = func() {
		cancel()
		<-done
	}
	t.Cleanup(stop)
	w.settle()
	return stop
}

// settle lets the operator do all it can at the present moment.
func (w *world) settle() {
	time.Sleep(time.Second)
	synctest.Wait()
}

// qhy5lii is the rule for the bare FX2 chip of a QHY5L-II camera.
func qhy5lii() firmware {
	f := firmware{Spec: firmwareSpec{
		Selector: `device.attributes["liken.sh"].vendor == "1618" && device.attributes["liken.sh"].product == "0920"`,
		Upload: &upload{
			Image:   "ghcr.io/liken-sh/indi-qhy:test",
			Command: []string{"fxload", "-t", "fx2", "-I", "/lib/firmware/qhy/QHY5II.HEX", "-D", "$(" + deviceVariable + ")"},
		},
	}}
	f.Metadata.Name = "qhy5l-ii"
	return f
}

// named answers a copy of a rule under another name.
func named(f firmware, name string) firmware {
	f.Metadata.Name = name
	return f
}

// usb is one USB device as machine-operator publishes it.
type usb struct {
	address              string
	vendor, product      string
	busNumber, devNumber int64
}

// bareChip is the QHY5L-II before its firmware, on bus 1.
func bareChip(address string, number int64) usb {
	return usb{address: address, vendor: "1618", product: "0920", busNumber: 1, devNumber: number}
}

// camera is the QHY5L-II after its firmware: the same port, a new
// product ID, and a new device number.
func camera(address string, number int64) usb {
	return usb{address: address, vendor: "1618", product: "0921", busNumber: 1, devNumber: number}
}

// name is the device's name in the slice, as machine-operator derives
// it from the bus and the address.
func (u usb) name() string {
	out := []byte("usb-" + u.address)
	for i, c := range out {
		if c == '.' || c == ':' {
			out[i] = '-'
		}
	}
	return string(out)
}

func (u usb) sliceDevice() sliceDevice {
	text := func(s string) resourceapi.DeviceAttribute { return resourceapi.DeviceAttribute{StringValue: &s} }
	number := func(n int64) resourceapi.DeviceAttribute { return resourceapi.DeviceAttribute{IntValue: &n} }
	return sliceDevice{Name: u.name(), Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
		"bus": text("usb"), "address": text(u.address), "vendor": text(u.vendor), "product": text(u.product),
		"busNumber": number(u.busNumber), "deviceNumber": number(u.devNumber),
	}}
}

// publish plays machine-operator: the machine's slice now holds these
// devices, at the next pool generation.
func (a *fakeAPI) publish(machine string, devices ...usb) {
	a.mu.Lock()
	defer a.mu.Unlock()
	name := machine + "-" + driverName
	generation := 1.0
	kind := "ADDED"
	if held, exists := a.objects[slicesPath][name]; exists {
		generation = held["spec"].(map[string]any)["pool"].(map[string]any)["generation"].(float64) + 1
		kind = "MODIFIED"
	}
	listed := []any{}
	for _, d := range devices {
		listed = append(listed, toMap(d.sliceDevice()))
	}
	a.store(slicesPath, map[string]any{
		"apiVersion": "resource.k8s.io/v1", "kind": "ResourceSlice",
		"metadata": map[string]any{"name": name, "uid": "uid-" + name},
		"spec": map[string]any{
			"driver": driverName, "nodeName": machine,
			"pool":    map[string]any{"name": machine, "generation": generation, "resourceSliceCount": 1},
			"devices": listed,
		},
	}, kind)
}

// putMachine stores a Machine with its labels.
func (a *fakeAPI) putMachine(name string, labels map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	kind := "ADDED"
	if _, exists := a.objects[machinesPath][name]; exists {
		kind = "MODIFIED"
	}
	a.store(machinesPath, map[string]any{
		"apiVersion": "liken.sh/v1alpha1", "kind": "Machine",
		"metadata": map[string]any{"name": name, "uid": "uid-" + name, "labels": toMap(labels)},
	}, kind)
}

// putFirmware creates a rule, or replaces its spec the way kubectl
// apply does. A change of the spec moves the generation.
func (a *fakeAPI) putFirmware(f firmware) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object := toMap(f)
	object["apiVersion"], object["kind"] = firmwareAPIVersion, firmwareKind
	metadata := object["metadata"].(map[string]any)
	kind := "ADDED"
	if held, exists := a.objects[firmwaresPath][f.Metadata.Name]; exists {
		kind = "MODIFIED"
		heldMeta := held["metadata"].(map[string]any)
		metadata["uid"], metadata["generation"] = heldMeta["uid"], heldMeta["generation"]
		object["status"] = held["status"]
		if string(mustJSON(held["spec"])) != string(mustJSON(object["spec"])) {
			metadata["generation"] = heldMeta["generation"].(float64) + 1
		}
	} else {
		metadata["uid"], metadata["generation"] = "uid-"+f.Metadata.Name, 1
		delete(object, "status")
	}
	a.store(firmwaresPath, object, kind)
}

// firmware answers the stored rule.
func (a *fakeAPI) firmware(t *testing.T, name string) firmware {
	t.Helper()
	var out firmware
	a.mu.Lock()
	object := clone(a.objects[firmwaresPath][name])
	a.mu.Unlock()
	a.decode(t, object, &out)
	return out
}

// uploads answers every stored upload pod.
func (a *fakeAPI) uploads(t *testing.T) []pod {
	t.Helper()
	var out []pod
	for _, object := range a.held(uploadPodsPath) {
		var p pod
		a.decode(t, object, &p)
		out = append(out, p)
	}
	return out
}

// claims answers every stored claim.
func (a *fakeAPI) claims(t *testing.T) []resourceClaim {
	t.Helper()
	var out []resourceClaim
	for _, object := range a.held(claimsPath) {
		var c resourceClaim
		a.decode(t, object, &c)
		out = append(out, c)
	}
	return out
}

func (a *fakeAPI) held(collection string) []map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []map[string]any
	for _, object := range a.objects[collection] {
		out = append(out, clone(object))
	}
	return out
}

func (a *fakeAPI) created(collection string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.creates[collection]
}

func (a *fakeAPI) decode(t *testing.T, object map[string]any, out any) {
	t.Helper()
	body, _ := json.Marshal(object)
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatal(err)
	}
}

// finish plays the kubelet: the upload pod's loader ended with the exit
// code and the end of its log, now, and the pod ended in the phase.
func (a *fakeAPI) finish(t *testing.T, name, phase string, exitCode int, log string) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[uploadPodsPath][name])
	if next == nil {
		t.Fatalf("there is no upload pod %s", name)
	}
	next["status"] = map[string]any{
		"phase":      phase,
		"conditions": []any{map[string]any{"type": "PodScheduled", "status": "True"}},
		"containerStatuses": []any{map[string]any{
			"name": uploadContainer,
			"state": map[string]any{"terminated": map[string]any{
				"exitCode": exitCode, "message": log, "finishedAt": time.Now().UTC().Format(time.RFC3339),
			}},
		}},
	}
	a.store(uploadPodsPath, next, "MODIFIED")
}

// onlyUpload answers the one upload pod, and fails the test when there
// is not exactly one.
func (w *world) onlyUpload(t *testing.T) pod {
	t.Helper()
	uploads := w.api.uploads(t)
	if len(uploads) != 1 {
		t.Fatalf("%d upload pods, want 1", len(uploads))
	}
	return uploads[0]
}

// entry answers a rule's one status entry, and fails the test when
// there is not exactly one.
func (w *world) entry(t *testing.T, rule string) deviceEntry {
	t.Helper()
	devices := w.api.firmware(t, rule).Status.Devices
	if len(devices) != 1 {
		t.Fatalf("status.devices = %+v, want one entry", devices)
	}
	return devices[0]
}

// posted is the part of an Event a test compares: its type and its
// reason.
type posted struct {
	kind, reason string
}

// postedAbout answers what the operator posted about one rule, in the
// order it posted it.
func (w *world) postedAbout(name string) []posted {
	var out []posted
	for _, event := range w.api.recorded.About(firmwareKind, name) {
		out = append(out, posted{event.Type, event.Reason})
	}
	return out
}

// uploadCountsOf answers an upload rule's counts, and fails the test
// when the status holds no upload counts or holds an update rule's.
func uploadCountsOf(t *testing.T, f firmware) UploadCounts {
	t.Helper()
	if f.Status.Counts.UploadCounts == nil || f.Status.Counts.UpdateCounts != nil {
		t.Fatalf("status.counts of %s is %+v, want only the upload counts", f.Metadata.Name, f.Status.Counts)
	}
	return *f.Status.Counts.UploadCounts
}

// updateCountsOf answers an update rule's counts, and fails the test
// when the status holds no update counts or holds an upload rule's.
func updateCountsOf(t *testing.T, f *firmware) UpdateCounts {
	t.Helper()
	if f.Status.Counts.UpdateCounts == nil || f.Status.Counts.UploadCounts != nil {
		t.Fatalf("status.counts of %s is %+v, want only the update counts", f.Metadata.Name, f.Status.Counts)
	}
	return *f.Status.Counts.UpdateCounts
}
