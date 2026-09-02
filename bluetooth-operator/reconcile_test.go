package main

// These tests drive one reconcile pass against the test API server,
// with the paired-set answer supplied directly. That answer is the one
// input that separates a slice write from a delete and from no write
// at all, and the tests supply it directly instead of through a bus.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// pairedSet returns a reader that answers with these controllers.
func pairedSet(controllers map[string]controller) pairedSetReader {
	return func() (map[string]controller, error) { return controllers, nil }
}

// failing returns a reader that answers with err and nothing else.
func failing(err error) pairedSetReader {
	return func() (map[string]controller, error) { return nil, err }
}

func connectedController() map[string]controller {
	return map[string]controller{"a0:ab:51:33:b7:12": {Name: "Player One", Connected: true}}
}

// reconcileFixture wires a publisher to a test API server that holds
// one slice, with sysfs and the CDI directory pointed at directories
// the test owns.
func reconcileFixture(t *testing.T, sysfs string, devices ...fakeHID) (*publisher, *slicePublishFixture) {
	t.Helper()
	cdiTempDir(t)
	previous := draSysfsRoot
	draSysfsRoot = sysfs
	if sysfs == "" {
		draSysfsRoot = fakeSysfs(t, devices...)
	}
	t.Cleanup(func() { draSysfsRoot = previous })

	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))
	return &publisher{
		client:   client,
		nodeName: "liken-1",
		owner:    testOwner(),
		relays:   relaysFor(t, devices...),
	}, fixture
}

// testBus is the media bus device of the adapter these tests run
// against.
const testBus = "14-b4-57-91-2f-c8-media"

// deviceNamed picks one device out of a published slice.
func deviceNamed(t *testing.T, slice *ResourceSlice, name string) SliceDevice {
	t.Helper()
	if slice == nil {
		t.Fatalf("no slice was published, so it holds no %s", name)
	}
	for _, device := range slice.Spec.Devices {
		if device.Name == name {
			return device
		}
	}
	t.Fatalf("the slice holds no %s: %+v", name, slice.Spec.Devices)
	return SliceDevice{}
}

func sliceNames(slice *ResourceSlice) []string {
	names := make([]string, 0, len(slice.Spec.Devices))
	for _, device := range slice.Spec.Devices {
		names = append(names, device.Name)
	}
	return names
}

func TestReconcilePublishesAConnectedController(t *testing.T) {
	publish, fixture := reconcileFixture(t, "",
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))

	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("the pass reported a failure")
	}
	if fixture.created == nil {
		t.Fatal("no slice was created")
	}
	device := deviceNamed(t, fixture.created, "a0-ab-51-33-b7-12")
	if len(device.Taints) != 0 {
		t.Fatalf("device = %+v", device)
	}
}

// The media bus is the adapter's own device: the claimable permission
// to run a sound server on this radio. It publishes as soon as
// bluetoothd names the adapter, beside the paired controllers.
func TestReconcilePublishesTheMediaBus(t *testing.T) {
	publish, fixture := reconcileFixture(t, "",
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))

	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("the pass reported a failure")
	}
	bus := deviceNamed(t, fixture.created, testBus)

	want := map[string]any{
		"sound.liken.sh/supportsSound": true,
		"kind":                         "mediaBus",
		"address":                      "14:B4:57:91:2F:C8",
	}
	if got := publishedAttributes(bus); !reflect.DeepEqual(got, want) {
		t.Fatalf("attributes = %+v, want %+v", got, want)
	}
	// The cluster's input class selects has(input) && input, so an input
	// attribute on the bus would put a sound server's claim on the same
	// device a gamepad's claim reads.
	if _, ok := bus.Attributes["input"]; ok {
		t.Error("the media bus published an input attribute")
	}
	if len(bus.Taints) != 0 {
		t.Errorf("taints = %+v, want none", bus.Taints)
	}
}

// The bus alone is a slice worth publishing: an adapter with nothing
// paired to it can still serve a sound server, and the pairing itself
// needs that sound server registered first.
func TestReconcilePublishesTheBusWithNoControllers(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")

	if !publish.reconcile(pairedSet(map[string]controller{}), adapterIs(t), nil) {
		t.Fatal("the pass reported a failure")
	}
	if fixture.deleted {
		t.Fatal("a node with an adapter and no controller deleted its slice")
	}
	if got := sliceNames(fixture.created); len(got) != 1 || got[0] != testBus {
		t.Fatalf("devices = %v, want [%s]", got, testBus)
	}
}

// A slice with nothing in it at all reaches the delete path, which is
// the node whose bluetoothd has never named an adapter.
func TestReconcileDeletesTheSliceWhileTheAdapterIsUnknown(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")
	noAdapter := func() (bonds.Address, error) { return bonds.Address{}, ErrNoAdapter }
	fixture.existing = &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-bluetooth.liken.sh"},
		Spec:     ResourceSliceSpec{Devices: []SliceDevice{publishedDevice()}},
	}

	if !publish.reconcile(pairedSet(map[string]controller{}), noAdapter, nil) {
		t.Fatal("the pass reported a failure")
	}
	if !fixture.deleted {
		t.Fatal("a node with no adapter and no controller kept its slice")
	}
}

// The overflow truncation drops controllers, never the media bus. One
// radio has one bus, and dropping it would take the machine's
// Bluetooth audio away to publish one more gamepad.
func TestReconcileNeverDropsTheBusInTheOverflow(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")

	// Every one of these sorts ahead of the bus by name, so a bus that
	// took its place in the sorted list would be the device the
	// truncation dropped.
	controllers := map[string]controller{}
	for i := range maxSliceDevices + 10 {
		controllers[fmt.Sprintf("00:ab:51:33:%02x:%02x", i/256, i%256)] = controller{}
	}
	if !publish.reconcile(pairedSet(controllers), adapterIs(t), nil) {
		t.Fatal("the pass reported a failure")
	}
	if got := len(fixture.created.Spec.Devices); got != maxSliceDevices {
		t.Fatalf("devices = %d, want %d", got, maxSliceDevices)
	}
	deviceNamed(t, fixture.created, testBus)
}

// TestReconcileNeverDeletesWhenTheAdapterIsGone is the regression test
// for the two ways an empty answer arrives. bluetoothd publishes no
// device objects in the moments after it starts, and it removes every
// device object when the adapter goes away. Publishing an empty slice
// from either one would retract a controller that a claim still holds.
func TestReconcileNeverDeletesWhenTheAdapterIsGone(t *testing.T) {
	publish, fixture := reconcileFixture(t, "",
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))

	// A first pass with the adapter present, so there is a published
	// slice and a last-known paired set.
	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("the first pass reported a failure")
	}
	fixture.existing = fixture.created
	fixture.created = nil

	// The dongle is unplugged. bluetoothd answers with no adapter and
	// no devices.
	if publish.reconcile(failing(ErrNoAdapter), adapterIs(t), nil) {
		t.Fatal("a departed adapter reported a finished pass")
	}
	if fixture.deleted {
		t.Fatal("a departed adapter deleted the slice")
	}
	if fixture.updated == nil {
		t.Fatal("a departed adapter left the slice saying the controller is connected")
	}

	// Every published controller is out of reach, so the NoExecute taint
	// ends the sessions that are running and holds the next allocation
	// back. The no-input-node taint is absent: the relay holds the
	// controller's virtual node whether the radio is there or not, so a
	// consumer that tolerates the NoExecute taint keeps a node that
	// works when the radio returns.
	device := deviceNamed(t, fixture.updated, "a0-ab-51-33-b7-12")
	wantTaints := []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}}
	if !reflect.DeepEqual(device.Taints, wantTaints) {
		t.Errorf("device %s taints = %+v, want %+v", device.Name, device.Taints, wantTaints)
	}
	if *device.Attributes["connected"].Bool {
		t.Errorf("device %s still says it is connected", device.Name)
	}
}

// A departed adapter takes the bus with it, and the bus is tainted
// NoSchedule and never NoExecute. Evicting the claim holder would end
// that machine's other audio too, and that audio has nothing to do
// with the radio.
func TestReconcileTaintsTheBusWhenTheAdapterIsGone(t *testing.T) {
	publish, fixture := reconcileFixture(t, "",
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))

	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("the first pass reported a failure")
	}
	fixture.existing = fixture.created

	if publish.reconcile(failing(ErrNoAdapter), adapterIs(t), nil) {
		t.Fatal("a departed adapter reported a finished pass")
	}
	bus := deviceNamed(t, fixture.updated, testBus)
	want := []DeviceTaint{{Key: disconnectedTaint, Effect: "NoSchedule"}}
	if !reflect.DeepEqual(bus.Taints, want) {
		t.Fatalf("taints = %+v, want %+v", bus.Taints, want)
	}
}

// An adapter can depart with nothing paired to it. The last publish
// offered an untainted bus, so this pass must not be mistaken for the
// startup window: the startup window is over once bluetoothd has
// named the adapter, and the bus takes its taint.
func TestReconcileTaintsTheBusWhenTheAdapterIsGoneWithNothingPaired(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")
	if !publish.reconcile(pairedSet(map[string]controller{}), adapterIs(t), nil) {
		t.Fatal("the first pass reported a failure")
	}
	fixture.existing = fixture.created

	if publish.reconcile(failing(ErrNoAdapter), adapterIs(t), nil) {
		t.Fatal("a departed adapter reported a finished pass")
	}
	bus := deviceNamed(t, fixture.updated, testBus)
	want := []DeviceTaint{{Key: disconnectedTaint, Effect: "NoSchedule"}}
	if !reflect.DeepEqual(bus.Taints, want) {
		t.Fatalf("taints = %+v, want %+v", bus.Taints, want)
	}
}

func TestReconcileWritesNothingBeforeBluetoothdPublishesAnAdapter(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")

	// The startup window: no read of any kind has succeeded yet. Both
	// readers answer from the same bluetoothd object tree, so before
	// bluetoothd publishes the adapter, both fail together. There is no
	// last-known set to taint, no adapter to name a media bus for, and
	// nothing true to say.
	noAdapter := func() (bonds.Address, error) { return bonds.Address{}, ErrNoAdapter }
	if publish.reconcile(failing(ErrNoAdapter), noAdapter, nil) {
		t.Fatal("the startup window reported a finished pass")
	}
	if len(fixture.requests) != 0 {
		t.Fatalf("the startup window talked to the API: %v", fixture.requests)
	}
}

func TestReconcileLeavesTheSliceAloneOnAReadFailure(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")

	if publish.reconcile(failing(errors.New("the bus went away")), adapterIs(t), nil) {
		t.Fatal("a failed read reported a finished pass")
	}
	if len(fixture.requests) != 0 {
		t.Fatalf("a failed read talked to the API: %v", fixture.requests)
	}
}

// Unpairing the last controller removes that one device and leaves the
// slice, because the adapter's media bus is still there to claim.
func TestReconcileKeepsTheSliceWhenTheLastControllerIsUnpaired(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")
	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("the first pass reported a failure")
	}
	fixture.existing = fixture.created

	if !publish.reconcile(pairedSet(map[string]controller{}), adapterIs(t), nil) {
		t.Fatal("the unpair pass reported a failure")
	}
	if fixture.deleted {
		t.Fatal("unpairing the last controller deleted the slice")
	}
	if got := sliceNames(fixture.updated); len(got) != 1 || got[0] != testBus {
		t.Fatalf("devices = %v, want [%s]", got, testBus)
	}
}

func TestReconcileReportsAFailedWrite(t *testing.T) {
	publish, _ := reconcileFixture(t, "")
	// A write that the API server refuses must not read as a finished
	// pass. The unfinished report schedules the one quick retry.
	publish.client = testClient(t, failingAPI(t))
	if publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("a refused write reported a finished pass")
	}
}

func TestExitReason(t *testing.T) {
	running, keepRunning := context.WithCancel(context.Background())
	defer keepRunning()
	stopped, stop := context.WithCancel(context.Background())
	stop()

	cases := []struct {
		name string
		ctx  context.Context
		ok   bool
		want error
	}{
		{name: "a value keeps the loop going", ctx: running, ok: true, want: nil},
		{name: "a value during shutdown still keeps it going", ctx: stopped, ok: true, want: nil},
		{name: "closed during shutdown ends cleanly", ctx: stopped, ok: false, want: errShutdown},
		{name: "closed while running is the lost bus", ctx: running, ok: false, want: errSourcesClosed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := exitReason(c.ctx, c.ok); !errors.Is(got, c.want) {
				t.Fatalf("exitReason = %v, want %v", got, c.want)
			}
		})
	}
}

// failingAPI refuses every request, the way an API server does when
// RBAC denies the write.
func failingAPI(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"resourceslices is forbidden"}`)
	})
}

// A bond that has never connected has no relay, so a claim on it would
// deliver nothing. That is the whole meaning of the no-input-node
// taint, and it is what holds a claim back until somebody switches the
// controller on once.
func TestReconcileTaintsABondThatHasNeverConnected(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")

	if !publish.reconcile(pairedSet(map[string]controller{"a0:ab:51:33:b7:12": {}}), adapterIs(t), nil) {
		t.Fatal("the pass reported a failure")
	}

	device := deviceNamed(t, fixture.created, "a0-ab-51-33-b7-12")
	want := []DeviceTaint{
		{Key: disconnectedTaint, Effect: "NoExecute"},
		{Key: noInputNodeTaint, Effect: "NoSchedule"},
	}
	if !reflect.DeepEqual(device.Taints, want) {
		t.Errorf("taints = %+v, want %+v", device.Taints, want)
	}
}

// A controller that connected once and then went to sleep keeps the
// virtual node its relay holds open, so the no-input-node taint goes
// away and stays away. This is the state a Low Energy remote rests in
// between presses.
func TestReconcileKeepsTheNodeOfAControllerThatWentToSleep(t *testing.T) {
	publish, fixture := reconcileFixture(t, "",
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))

	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("the first pass reported a failure")
	}
	fixture.existing = fixture.created

	// The controller slept, so the kernel took its evdev node away and
	// the walk finds nothing under it.
	draSysfsRoot = fakeSysfs(t)
	if !publish.reconcile(pairedSet(map[string]controller{"a0:ab:51:33:b7:12": {}}), adapterIs(t), nil) {
		t.Fatal("the second pass reported a failure")
	}

	device := deviceNamed(t, fixture.updated, "a0-ab-51-33-b7-12")
	want := []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}}
	if !reflect.DeepEqual(device.Taints, want) {
		t.Errorf("taints = %+v, want %+v", device.Taints, want)
	}
}
