package main

// These tests run reconcile against a fake API server, with readers of
// the paired set and the adapter address in place of bluetoothd.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

func pairedSet(controllers map[string]controller) pairedSetReader {
	return func() (map[string]controller, error) { return controllers, nil }
}

func failing(err error) pairedSetReader {
	return func() (map[string]controller, error) { return nil, err }
}

func connectedController() map[string]controller {
	return map[string]controller{"a0:ab:51:33:b7:12": {Name: "Player One", Connected: true}}
}

// reconcileFixture returns a publisher whose fake API server records
// the slice that it creates, updates, or deletes. When sysfs is empty,
// the fixture builds a fake sysfs that holds devices.
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

// testBus is the media bus of the adapter that adapterIs returns.
const testBus = "14-b4-57-91-2f-c8-media"

func deviceNamed(t *testing.T, slice *ResourceSlice, name string) SliceDevice {
	t.Helper()
	if slice == nil {
		t.Fatalf("no slice was published, want a slice with the device %s", name)
	}
	for _, device := range slice.Spec.Devices {
		if device.Name == name {
			return device
		}
	}
	t.Fatalf("the slice has no device %s, want one: %+v", name, slice.Spec.Devices)
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
		t.Fatal("reconcile returned false, want true")
	}
	if fixture.created == nil {
		t.Fatal("no slice was created, want one")
	}
	device := deviceNamed(t, fixture.created, "a0-ab-51-33-b7-12")
	if len(device.Taints) != 0 {
		t.Fatalf("the connected controller has taints %+v, want none", device.Taints)
	}
}

func TestReconcilePublishesTheMediaBus(t *testing.T) {
	publish, fixture := reconcileFixture(t, "",
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))

	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("reconcile returned false, want true")
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
	// The cluster's input DeviceClass selects has(input) && input. A bus
	// with an `input` attribute could be allocated to an input claim.
	if _, ok := bus.Attributes["input"]; ok {
		t.Error("the media bus has an input attribute, want none")
	}
	if len(bus.Taints) != 0 {
		t.Errorf("taints are %+v, want none", bus.Taints)
	}
}

// An adapter with no paired controllers still publishes the bus,
// because a speaker can pair only after a sound server has registered
// with bluetoothd.
func TestReconcilePublishesTheBusWithNoControllers(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")

	if !publish.reconcile(pairedSet(map[string]controller{}), adapterIs(t), nil) {
		t.Fatal("reconcile returned false, want true")
	}
	if fixture.deleted {
		t.Fatal("a node with an adapter and no controller deleted its slice, want it kept")
	}
	if got := sliceNames(fixture.created); len(got) != 1 || got[0] != testBus {
		t.Fatalf("device names are %v, want [%s]", got, testBus)
	}
}

// With no adapter and no controller, the slice would have no devices,
// so the pass deletes it.
func TestReconcileDeletesTheSliceWhileTheAdapterIsUnknown(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")
	noAdapter := func() (bonds.Address, error) { return bonds.Address{}, ErrNoAdapter }
	fixture.existing = &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-bluetooth.liken.sh"},
		Spec:     ResourceSliceSpec{Devices: []SliceDevice{publishedDevice()}},
	}

	if !publish.reconcile(pairedSet(map[string]controller{}), noAdapter, nil) {
		t.Fatal("reconcile returned false, want true")
	}
	if !fixture.deleted {
		t.Fatal("a node with no adapter and no controller kept its slice, want it deleted")
	}
}

// When there are more devices than a slice can hold, the pass drops
// controllers and keeps the media bus.
func TestReconcileNeverDropsTheBusInTheOverflow(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")

	// Every controller name sorts before the bus name, so a pass that
	// kept devices in sorted order and cut the end of the list would
	// drop the bus.
	controllers := map[string]controller{}
	for i := range maxSliceDevices + 10 {
		controllers[fmt.Sprintf("00:ab:51:33:%02x:%02x", i/256, i%256)] = controller{}
	}
	if !publish.reconcile(pairedSet(controllers), adapterIs(t), nil) {
		t.Fatal("reconcile returned false, want true")
	}
	if got := len(fixture.created.Spec.Devices); got != maxSliceDevices {
		t.Fatalf("the slice has %d devices, want %d", got, maxSliceDevices)
	}
	deviceNamed(t, fixture.created, testBus)
}

// bluetoothd has no device objects for a short time after it starts,
// and it removes every device object when the adapter goes away. Both
// look like an empty paired set. Publishing an empty slice in either
// case would remove a controller that a claim still holds.
func TestReconcileNeverDeletesWhenTheAdapterIsGone(t *testing.T) {
	publish, fixture := reconcileFixture(t, "",
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))

	// The first pass runs with the adapter present, so there is a
	// published slice and a last known paired set.
	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("the first reconcile returned false, want true")
	}
	fixture.existing = fixture.created
	fixture.created = nil

	// The adapter is unplugged. bluetoothd reports no adapter and no
	// devices.
	if publish.reconcile(failing(ErrNoAdapter), adapterIs(t), nil) {
		t.Fatal("reconcile with a departed adapter returned true, want false")
	}
	if fixture.deleted {
		t.Fatal("a departed adapter deleted the slice, want it kept")
	}
	if fixture.updated == nil {
		t.Fatal("a departed adapter left the slice with connected = true, want false")
	}

	// The no-input-node taint is absent, because the relay keeps the
	// controller's virtual node while the radio is gone.
	device := deviceNamed(t, fixture.updated, "a0-ab-51-33-b7-12")
	wantTaints := []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}}
	if !reflect.DeepEqual(device.Taints, wantTaints) {
		t.Errorf("device %s has taints %+v, want %+v", device.Name, device.Taints, wantTaints)
	}
	if *device.Attributes["connected"].Bool {
		t.Errorf("device %s has connected = true, want false", device.Name)
	}
}

func TestReconcileTaintsTheBusWhenTheAdapterIsGone(t *testing.T) {
	publish, fixture := reconcileFixture(t, "",
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))

	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("the first reconcile returned false, want true")
	}
	fixture.existing = fixture.created

	if publish.reconcile(failing(ErrNoAdapter), adapterIs(t), nil) {
		t.Fatal("reconcile with a departed adapter returned true, want false")
	}
	bus := deviceNamed(t, fixture.updated, testBus)
	want := []DeviceTaint{{Key: disconnectedTaint, Effect: "NoSchedule"}}
	if !reflect.DeepEqual(bus.Taints, want) {
		t.Fatalf("taints are %+v, want %+v", bus.Taints, want)
	}
}

// The adapter can go away when nothing is paired. This is not the
// startup case, which ends once bluetoothd has reported the adapter,
// so the pass taints the published bus.
func TestReconcileTaintsTheBusWhenTheAdapterIsGoneWithNothingPaired(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")
	if !publish.reconcile(pairedSet(map[string]controller{}), adapterIs(t), nil) {
		t.Fatal("the first reconcile returned false, want true")
	}
	fixture.existing = fixture.created

	if publish.reconcile(failing(ErrNoAdapter), adapterIs(t), nil) {
		t.Fatal("reconcile with a departed adapter returned true, want false")
	}
	bus := deviceNamed(t, fixture.updated, testBus)
	want := []DeviceTaint{{Key: disconnectedTaint, Effect: "NoSchedule"}}
	if !reflect.DeepEqual(bus.Taints, want) {
		t.Fatalf("taints are %+v, want %+v", bus.Taints, want)
	}
}

func TestReconcileWritesNothingBeforeBluetoothdPublishesAnAdapter(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")

	// At startup, both readers fail until bluetoothd publishes the
	// adapter. There is no earlier paired set to taint and no adapter
	// address to name a media bus, so there is nothing correct to write.
	noAdapter := func() (bonds.Address, error) { return bonds.Address{}, ErrNoAdapter }
	if publish.reconcile(failing(ErrNoAdapter), noAdapter, nil) {
		t.Fatal("reconcile before the adapter appears returned true, want false")
	}
	if len(fixture.requests) != 0 {
		t.Fatalf("reconcile before the adapter appears sent API requests %v, want none", fixture.requests)
	}
}

func TestReconcileLeavesTheSliceAloneOnAReadFailure(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")

	if publish.reconcile(failing(errors.New("the bus went away")), adapterIs(t), nil) {
		t.Fatal("reconcile after a failed read returned true, want false")
	}
	if len(fixture.requests) != 0 {
		t.Fatalf("reconcile after a failed read sent API requests %v, want none", fixture.requests)
	}
}

// Unpairing the last controller removes its device. The slice stays,
// because it still has the media bus.
func TestReconcileKeepsTheSliceWhenTheLastControllerIsUnpaired(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")
	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("the first reconcile returned false, want true")
	}
	fixture.existing = fixture.created

	if !publish.reconcile(pairedSet(map[string]controller{}), adapterIs(t), nil) {
		t.Fatal("reconcile after the unpair returned false, want true")
	}
	if fixture.deleted {
		t.Fatal("unpairing the last controller deleted the slice, want it kept")
	}
	if got := sliceNames(fixture.updated); len(got) != 1 || got[0] != testBus {
		t.Fatalf("device names are %v, want [%s]", got, testBus)
	}
}

func TestReconcileReportsAFailedWrite(t *testing.T) {
	publish, _ := reconcileFixture(t, "")
	// A refused write must not count as a finished pass, so that the
	// caller schedules another pass after retryDelay.
	publish.client = testClient(t, failingAPI(t))
	if publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("reconcile after a refused write returned true, want false")
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
				t.Fatalf("exitReason is %v, want %v", got, c.want)
			}
		})
	}
}

// failingAPI returns an HTTP handler that responds to every request
// with 403 Forbidden, as an API server does when RBAC denies the write.
func failingAPI(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"resourceslices is forbidden"}`)
	})
}

// A bond that has never connected has no virtual input device, so a
// claim on it would deliver nothing.
func TestReconcileTaintsABondThatHasNeverConnected(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")

	if !publish.reconcile(pairedSet(map[string]controller{"a0:ab:51:33:b7:12": {}}), adapterIs(t), nil) {
		t.Fatal("reconcile returned false, want true")
	}

	device := deviceNamed(t, fixture.created, "a0-ab-51-33-b7-12")
	want := []DeviceTaint{
		{Key: disconnectedTaint, Effect: "NoExecute"},
		{Key: noInputNodeTaint, Effect: "NoSchedule"},
	}
	if !reflect.DeepEqual(device.Taints, want) {
		t.Errorf("taints are %+v, want %+v", device.Taints, want)
	}
}

// A controller that connected once and then went to sleep keeps the
// virtual node that its relay holds open, so the no-input-node taint
// stays off. A Low Energy remote sleeps between button presses.
func TestReconcileKeepsTheNodeOfAControllerThatWentToSleep(t *testing.T) {
	publish, fixture := reconcileFixture(t, "",
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))

	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("the first reconcile returned false, want true")
	}
	fixture.existing = fixture.created

	// The controller is asleep, so the kernel removed its evdev node
	// and a scan of sysfs finds no node for it.
	draSysfsRoot = fakeSysfs(t)
	if !publish.reconcile(pairedSet(map[string]controller{"a0:ab:51:33:b7:12": {}}), adapterIs(t), nil) {
		t.Fatal("the second reconcile returned false, want true")
	}

	device := deviceNamed(t, fixture.updated, "a0-ab-51-33-b7-12")
	want := []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}}
	if !reflect.DeepEqual(device.Taints, want) {
		t.Errorf("taints are %+v, want %+v", device.Taints, want)
	}
}

// A consumer's CDI file lists the device nodes that its container
// received. When the relay now delivers different nodes for the same
// controller, that container has the wrong devices, and only that
// controller gets the node-moved taint.
func TestReconcileTaintsAControllerWhosePreparedNodesMoved(t *testing.T) {
	publish, fixture := reconcileFixture(t, "",
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"),
		dualSense("0002", "b4:8c:9d:11:22:33", "input/event6"))
	paired := map[string]controller{
		"a0:ab:51:33:b7:12": {Name: "Player One", Connected: true},
		"b4:8c:9d:11:22:33": {Name: "Player Two", Connected: true},
	}

	// The first pass creates each controller's virtual devices. The
	// prepare calls below deliver these devices.
	if !publish.reconcile(pairedSet(paired), adapterIs(t), nil) {
		t.Fatal("the first reconcile returned false, want true")
	}
	fixture.existing = fixture.created

	// One consumer has a node that the relay does not deliver now, as
	// it would after an operator restart. The other consumer has the
	// nodes that the relay delivers now.
	prepareClaim(t, "0f1e2d3c-0000-4000-8000-000000000001", "a0-ab-51-33-b7-12", "/dev/input/event99")
	prepareClaim(t, "0f1e2d3c-0000-4000-8000-000000000002", "b4-8c-9d-11-22-33",
		publish.relays.virtualNodes("b4:8c:9d:11:22:33")...)

	if !publish.reconcile(pairedSet(paired), adapterIs(t), nil) {
		t.Fatal("the second reconcile returned false, want true")
	}

	stale := deviceNamed(t, fixture.updated, "a0-ab-51-33-b7-12")
	want := []DeviceTaint{{Key: nodeMovedTaint, Effect: "NoExecute"}}
	if !reflect.DeepEqual(stale.Taints, want) {
		t.Errorf("taints are %+v, want %+v", stale.Taints, want)
	}
	current := deviceNamed(t, fixture.updated, "b4-8c-9d-11-22-33")
	if len(current.Taints) != 0 {
		t.Errorf("the controller whose consumer holds the current nodes has taints %+v, want none", current.Taints)
	}
	if bus := deviceNamed(t, fixture.updated, testBus); len(bus.Taints) != 0 {
		t.Errorf("the media bus has taints %+v, want none", bus.Taints)
	}
}

// After the eviction, the kubelet unprepares the claim, which deletes
// the CDI file. The next pass removes the taint, so the replacement pod
// can be scheduled.
func TestReconcileClearsTheMovedTaintWhenTheClaimIsUnprepared(t *testing.T) {
	publish, fixture := reconcileFixture(t, "",
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))
	const claimUID = "0f1e2d3c-0000-4000-8000-000000000001"
	prepareClaim(t, claimUID, "a0-ab-51-33-b7-12", "/dev/input/event99")

	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("the first reconcile returned false, want true")
	}
	if taints := deviceNamed(t, fixture.created, "a0-ab-51-33-b7-12").Taints; len(taints) != 1 {
		t.Fatalf("taints are %+v, want the node-moved taint", taints)
	}
	fixture.existing = fixture.created

	if err := removeCDISpec(claimUID); err != nil {
		t.Fatalf("removing the CDI spec: %v", err)
	}
	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("the second reconcile returned false, want true")
	}
	if taints := deviceNamed(t, fixture.updated, "a0-ab-51-33-b7-12").Taints; len(taints) != 0 {
		t.Errorf("taints are %+v, want none", taints)
	}
}

// movedControllers compares the node paths in each CDI file with the
// relay's nodes as sets, so order does not matter. A missing node is a
// move. A controller with no relay whose CDI file lists nodes is a
// move. The media bus, and a CDI file with no controller address, are
// never moves.
func TestMovedControllers(t *testing.T) {
	const mac = "a0:ab:51:33:b7:12"
	cases := []struct {
		name      string
		device    string
		delivered []string
		virtual   []string
		want      bool
	}{
		{
			name:      "the same nodes",
			device:    "a0-ab-51-33-b7-12",
			delivered: []string{"/dev/input/event5", "/dev/input/event6"},
			virtual:   []string{"/dev/input/event5", "/dev/input/event6"},
		},
		{
			name:      "the same nodes in another order",
			device:    "a0-ab-51-33-b7-12",
			delivered: []string{"/dev/input/event6", "/dev/input/event5"},
			virtual:   []string{"/dev/input/event5", "/dev/input/event6"},
		},
		{
			name:      "the numbers moved",
			device:    "a0-ab-51-33-b7-12",
			delivered: []string{"/dev/input/event11", "/dev/input/event12"},
			virtual:   []string{"/dev/input/event8", "/dev/input/event9"},
			want:      true,
		},
		{
			name:      "one node fewer than the file names",
			device:    "a0-ab-51-33-b7-12",
			delivered: []string{"/dev/input/event5", "/dev/input/event6"},
			virtual:   []string{"/dev/input/event5"},
			want:      true,
		},
		{
			name:      "no relay at all",
			device:    "a0-ab-51-33-b7-12",
			delivered: []string{"/dev/input/event5"},
			want:      true,
		},
		{
			name:      "the media bus, which grants no node",
			device:    "14-b4-57-91-2f-c8-media",
			delivered: nil,
		},
		{
			name:      "a device name that is no address",
			device:    "some-other-drivers-device",
			delivered: []string{"/dev/input/event5"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cdiTempDir(t)
			prepareClaim(t, "0f1e2d3c-0000-4000-8000-000000000001", c.device, c.delivered...)
			virtual := map[string][]string{}
			if len(c.virtual) > 0 {
				virtual[mac] = c.virtual
			}
			if got := movedControllers(virtual)[mac]; got != c.want {
				t.Errorf("moved is %v, want %v", got, c.want)
			}
		})
	}
}

// When a pass cannot read the CDI directory, it cannot tell which
// nodes a consumer has, so it taints nothing.
func TestMovedControllersTaintsNothingWithoutTheCDIDirectory(t *testing.T) {
	previous := cdiDir
	cdiDir = filepath.Join(t.TempDir(), "absent")
	t.Cleanup(func() { cdiDir = previous })

	if moved := movedControllers(map[string][]string{"a0:ab:51:33:b7:12": {"/dev/input/event5"}}); len(moved) != 0 {
		t.Errorf("moved is %v, want none", moved)
	}
}
