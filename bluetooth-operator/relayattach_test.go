package main

// These tests run the operator's loop with a controller that wakes
// from sleep, and check when the relay opens the controller's new input
// node. The kernel does not queue events for a reader that opens a node
// late, so every key press before the open is lost. The settle window
// delays ResourceSlice writes, and the relay must not wait for it.

import (
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// wakingRemote is the controller these tests wake. It is bonded but
// asleep when the loop starts, so sysfs has no input node for it.
var wakingRemote = dualSense("0001", testMAC, "input/event5")

// wakingController is the operator's loop running against a fake
// sysfs, a fake kernel for the relay, and a test API server that
// stores the ResourceSlice.
type wakingController struct {
	t       *testing.T
	sysfs   string
	kernel  *fakeKernel
	uevents chan kernelEvent

	// The API server's goroutine writes slices, and synctest.Wait does
	// not make the test's read wait for that write, so mu guards it.
	mu     sync.Mutex
	slices *slicePublishFixture
}

// runningLoop starts the loop the way main does: one pass, then a
// pass after each batch of kernel uevents, delayed by the settle
// window. The fake bluetoothd reports the remote as connected.
func runningLoop(t *testing.T) *wakingController {
	t.Helper()
	cdiTempDir(t)
	sysfs := fakeSysfs(t)
	previous := draSysfsRoot
	draSysfsRoot = sysfs
	t.Cleanup(func() { draSysfsRoot = previous })

	w := &wakingController{t: t, sysfs: sysfs, slices: &slicePublishFixture{}, uevents: make(chan kernelEvent, 16)}
	slicesAPI := w.slices.handler(t)
	server := apiservertest.Start(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		defer w.mu.Unlock()
		slicesAPI.ServeHTTP(rw, r)
	}))
	relays := relaysFor(t, wakingRemote)
	publish := &publisher{
		client:   apiclient.New(apiservertest.Host, server.Client(), ""),
		nodeName: "liken-1",
		owner:    testOwner(),
		relays:   relays,
	}
	paired, adapter := pairedSet(connectedController()), adapterIs(t)
	pass := func() { publish.reconcile(paired, adapter, nil) }
	pass()
	w.slices.existing = w.slices.created
	w.kernel = relays.kernel.(*fakeKernel)

	quiet := make(chan struct{})
	woken, attaches := wakes(t.Context(), w.uevents, quiet, quiet, quiet, quiet)
	settled := settle(t.Context(), woken, settleWindow, settleLimit)
	go func() { _ = loop(t.Context(), settled, attaches, nil, pass, publish.attach) }()
	return w
}

// connect adds the remote's input node to sysfs and sends the uevents
// the kernel sends for it: the HID device, then its evdev node.
func (w *wakingController) connect() {
	addHID(w.t, w.sysfs, wakingRemote)
	w.uevents <- kernelEvent{Subsystem: subsystemHID, Action: "add", MAC: testMAC}
	w.uevents <- kernelEvent{Subsystem: "input", Action: "add", MAC: testMAC}
}

// batteryChanges sends n power supply uevents, one every half settle
// window, so each one restarts the window before it ends.
func (w *wakingController) batteryChanges(n int) {
	for range n {
		w.uevents <- kernelEvent{Subsystem: subsystemPowerSupply, Action: "change"}
		time.Sleep(settleWindow / 2)
	}
}

// opened reports whether the relay has opened the remote's input node,
// after every goroutine in the bubble is blocked.
func (w *wakingController) opened() bool {
	synctest.Wait()
	return w.kernel.opened() == 1
}

// rewritten returns the ResourceSlice that a pass after the first one
// updated, or nil if there was none, after every goroutine in the
// bubble is blocked.
func (w *wakingController) rewritten() *ResourceSlice {
	synctest.Wait()
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.slices.updated
}

// The relay opens the node as soon as the kernel adds it. The slice
// update waits for the settle window, and the pass at its end publishes
// the remote with no taint.
func TestARelayOpensANewNodeBeforeTheSettleWindowEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		remote := runningLoop(t)

		remote.connect()
		if !remote.opened() {
			t.Fatal("the relay did not open the node after the kernel added it, want it opened")
		}
		if remote.rewritten() != nil {
			t.Fatal("the ResourceSlice was written before the settle window ended, want no write until it ends")
		}

		time.Sleep(settleWindow)
		if device := deviceNamed(t, remote.rewritten(), "a0-ab-51-33-b7-12"); len(device.Taints) != 0 {
			t.Errorf("the device taints are %+v, want none", device.Taints)
		}
	})
}

// Power supply uevents arrive before and after the node appears, and
// each one restarts the settle window. The relay must still open the
// node while the window stays open.
func TestARelayOpensANewNodeWhileUeventsHoldTheSettleWindowOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		remote := runningLoop(t)

		remote.batteryChanges(4)
		remote.connect()
		if !remote.opened() {
			t.Fatal("the relay did not open the node during the settle window, want it opened")
		}
		remote.batteryChanges(4)
		if remote.rewritten() != nil {
			t.Fatal("the ResourceSlice was written while events kept arriving, want no write")
		}
	})
}

// An unpair stops the relay of a controller that it removed from the
// slice, and nothing may start it again. attach skips the controllers
// that the last pass excluded,
// even when the controller's node is still in sysfs.
func TestAttachLeavesAnExcludedControllerAlone(t *testing.T) {
	publish, _ := reconcileFixture(t, "", wakingRemote)
	sysfs := draSysfsRoot
	draSysfsRoot = fakeSysfs(t)
	excluded := map[string]bool{testMAC: true}
	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), excluded) {
		t.Fatal("the pass returned false, want true")
	}

	draSysfsRoot = sysfs
	publish.attach()
	if opened := publish.relays.kernel.(*fakeKernel).opened(); opened != 0 {
		t.Fatalf("attach opened %d nodes of an excluded controller", opened)
	}
}
