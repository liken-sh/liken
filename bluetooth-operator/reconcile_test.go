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
	"testing"
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
	return &publisher{client: client, nodeName: "liken-1", owner: testOwner()}, fixture
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
	device := fixture.created.Spec.Devices[0]
	if device.Name != "a0-ab-51-33-b7-12" || len(device.Taints) != 0 {
		t.Fatalf("device = %+v", device)
	}
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

	// Every published controller is out of reach, so every one of them
	// carries both taints: NoExecute ends the sessions that are
	// running, and NoSchedule keeps the next claim parked.
	for _, device := range fixture.updated.Spec.Devices {
		keys := map[string]string{}
		for _, taint := range device.Taints {
			keys[taint.Key] = taint.Effect
		}
		if keys[disconnectedTaint] != "NoExecute" || keys[noInputNodeTaint] != "NoSchedule" {
			t.Errorf("device %s taints = %+v", device.Name, device.Taints)
		}
		if *device.Attributes["connected"].Bool {
			t.Errorf("device %s still says it is connected", device.Name)
		}
	}
}

func TestReconcileWritesNothingBeforeBluetoothdPublishesAnAdapter(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")

	// The startup window: no successful read has happened, so there is
	// no last-known set to taint and nothing true to say.
	if publish.reconcile(failing(ErrNoAdapter), adapterIs(t), nil) {
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

func TestReconcileDeletesTheSliceWhenTheLastControllerIsUnpaired(t *testing.T) {
	publish, fixture := reconcileFixture(t, "")
	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), nil) {
		t.Fatal("the first pass reported a failure")
	}
	fixture.existing = fixture.created

	// An adapter that answers with no paired devices is the one
	// sanctioned removal.
	if !publish.reconcile(pairedSet(map[string]controller{}), adapterIs(t), nil) {
		t.Fatal("the unpair pass reported a failure")
	}
	if !fixture.deleted {
		t.Fatal("unpairing the last controller did not delete the slice")
	}
}

func TestReconcileReportsAFailedWrite(t *testing.T) {
	publish, _ := reconcileFixture(t, "")
	// A write that the API server refuses must not read as a finished
	// pass, because that is what buys the one quick retry.
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
