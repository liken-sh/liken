package main

// These tests run the operator's loop over a controller that wakes
// from sleep, and check when its relay opens the new input node. The
// kernel keeps no backlog for a reader that opens a node late, so each
// press that arrives before the open is lost. The settle window
// batches the ResourceSlice writes, and the open must not wait for it.

import (
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// wakingRemote is the controller these tests wake. It is bonded and
// asleep when the loop starts, so sysfs holds no node for it.
var wakingRemote = dualSense("0001", testMAC, "input/event5")

// wakingController is the operator's loop, running over the walk, the
// relay's fake kernel, and a test API server that holds the slice.
type wakingController struct {
	t       *testing.T
	sysfs   string
	kernel  *fakeKernel
	uevents chan kernelEvent

	// slices answers the publisher from the API server's goroutine, and
	// synctest.Wait does not order the test's read after that write, so
	// the lock does.
	mu     sync.Mutex
	slices *slicePublishFixture
}

// runningLoop starts the loop the way main does: one pass, then the
// wakes of the kernel's uevents through the settle window. The bluetoothd
// that the pass reads reports the remote as connected.
func runningLoop(t *testing.T) *wakingController {
	t.Helper()
	cdiTempDir(t)
	sysfs := fakeSysfs(t)
	previous := draSysfsRoot
	draSysfsRoot = sysfs
	t.Cleanup(func() { draSysfsRoot = previous })

	w := &wakingController{t: t, sysfs: sysfs, slices: &slicePublishFixture{}, uevents: make(chan kernelEvent, 16)}
	answer := w.slices.handler(t)
	server := apiservertest.Start(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		defer w.mu.Unlock()
		answer.ServeHTTP(rw, r)
	}))
	held := relaysFor(t, wakingRemote)
	publish := &publisher{
		client:   apiclient.New(apiservertest.Host, server.Client(), ""),
		nodeName: "liken-1",
		owner:    testOwner(),
		relays:   held,
	}
	paired, adapter := pairedSet(connectedController()), adapterIs(t)
	pass := func() { publish.reconcile(paired, adapter, nil) }
	pass()
	w.slices.existing = w.slices.created
	w.kernel = held.kernel.(*fakeKernel)

	quiet := make(chan struct{})
	woken, attaches := wakes(t.Context(), w.uevents, quiet, quiet, quiet, quiet)
	settled := settle(t.Context(), woken, settleWindow, settleLimit)
	go func() { _ = loop(t.Context(), settled, attaches, nil, pass, publish.attach) }()
	return w
}

// connect registers the remote's input node in sysfs, and sends the
// uevents the kernel sends for it: the HID device, then its evdev node.
func (w *wakingController) connect() {
	addHID(w.t, w.sysfs, wakingRemote)
	w.uevents <- kernelEvent{Subsystem: subsystemHID, Action: "add", MAC: testMAC}
	w.uevents <- kernelEvent{Subsystem: "input", Action: "add", MAC: testMAC}
}

// batteryChanges sends n power supply changes, one each half window,
// so each one restarts the settle window before it ends.
func (w *wakingController) batteryChanges(n int) {
	for range n {
		w.uevents <- kernelEvent{Subsystem: subsystemPowerSupply, Action: "change"}
		time.Sleep(settleWindow / 2)
	}
}

// opened answers whether the relay reads the remote's real node, after
// every goroutine in the bubble has settled.
func (w *wakingController) opened() bool {
	synctest.Wait()
	return w.kernel.opened() == 1
}

// rewritten answers with the slice a pass after the first one wrote,
// or nil, after every goroutine in the bubble has settled.
func (w *wakingController) rewritten() *ResourceSlice {
	synctest.Wait()
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.slices.updated
}

// The relay opens the node as soon as the kernel adds it. The slice
// write waits for the settle window, and the pass at its end publishes
// the remote with no taint.
func TestARelayOpensANewNodeBeforeTheSettleWindowEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		remote := runningLoop(t)

		remote.connect()
		if !remote.opened() {
			t.Fatal("the relay did not open the node when the kernel added it")
		}
		if remote.rewritten() != nil {
			t.Fatal("the slice was written before the settle window ended")
		}

		time.Sleep(settleWindow)
		if device := deviceNamed(t, remote.rewritten(), "a0-ab-51-33-b7-12"); len(device.Taints) != 0 {
			t.Errorf("taints = %+v, want none", device.Taints)
		}
	})
}

// A connect sends battery changes and bluetoothd signals before and
// after the node appears, and each one restarts the settle window. The
// relay opens the node while the window stays open.
func TestARelayOpensANewNodeWhileUeventsHoldTheSettleWindowOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		remote := runningLoop(t)

		remote.batteryChanges(4)
		remote.connect()
		if !remote.opened() {
			t.Fatal("the relay did not open the node while the settle window was open")
		}
		remote.batteryChanges(4)
		if remote.rewritten() != nil {
			t.Fatal("the slice was written while the events went on")
		}
	})
}

// An unpair stops a retired controller's relay, and nothing may start
// it again. attach leaves out the controllers the last pass retired,
// even when the controller's node is still in sysfs.
func TestAttachLeavesARetiredControllerAlone(t *testing.T) {
	publish, _ := reconcileFixture(t, "", wakingRemote)
	sysfs := draSysfsRoot
	draSysfsRoot = fakeSysfs(t)
	retired := map[string]bool{testMAC: true}
	if !publish.reconcile(pairedSet(connectedController()), adapterIs(t), retired) {
		t.Fatal("the pass reported a failure")
	}

	draSysfsRoot = sysfs
	publish.attach()
	if opened := publish.relays.kernel.(*fakeKernel).opened(); opened != 0 {
		t.Fatalf("attach opened %d nodes of a retired controller", opened)
	}
}
