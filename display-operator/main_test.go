package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The settle tests use short windows so that the whole file runs in
// under a second. The assertions leave wide margins, because a test
// that measures a timer measures the scheduler as well.
const (
	testWindow = 40 * time.Millisecond
	testLimit  = 200 * time.Millisecond
)

func TestSettleCollapsesABurst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan struct{}, 16)
	out := settle(ctx, in, testWindow, testLimit)

	// One monitor plugged in produces a burst of uevents, and one
	// write must cover the whole burst. Every ResourceSlice write
	// wakes every DRA-pending pod in the cluster.
	for range 8 {
		in <- struct{}{}
		time.Sleep(testWindow / 4)
	}
	waitForWake(t, out, testLimit)
	assertQuiet(t, out, 3*testWindow)
}

func TestSettleWaitsForQuiet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan struct{}, 16)
	out := settle(ctx, in, testWindow, testLimit)

	in <- struct{}{}
	// Nothing arrives before the window passes.
	select {
	case <-out:
		t.Fatal("settle emitted before the window passed")
	case <-time.After(testWindow / 2):
	}
	waitForWake(t, out, testLimit)
}

func TestSettleEmitsUnderAConstantFlap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan struct{})
	out := settle(ctx, in, testWindow, testLimit)

	// A cable that flaps faster than the quiet window would restart
	// the wait forever. The limit keeps the loop publishing.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(testWindow / 2)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				select {
				case in <- struct{}{}:
				case <-stop:
					return
				}
			}
		}
	}()

	waitForWake(t, out, 2*testLimit)
}

func TestSettleStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	in := make(chan struct{}, 1)
	out := settle(ctx, in, testWindow, testLimit)

	cancel()
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("settle emitted after its context ended")
		}
	case <-time.After(time.Second):
		t.Fatal("settle did not close its channel")
	}
}

func TestWakesCarriesAnEventThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan drmEvent, 1)
	out := wakes(ctx, events, nil, nil)

	events <- drmEvent{Action: "change", DevPath: "/devices/pci0000:00/0000:00:02.0/drm/card1"}
	waitForWake(t, out, time.Second)
}

func TestWakesCarriesTheCompositorsSocketThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sockets := make(chan struct{}, 1)
	out := wakes(ctx, nil, nil, sockets)

	// A compositor that comes back is a pass of its own, because the
	// pass is what removes the taint and re-reads the connectors.
	sockets <- struct{}{}
	waitForWake(t, out, time.Second)
}

func TestWakesCarriesARetryThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	retries := make(chan struct{}, 1)
	out := wakes(ctx, nil, retries, nil)

	// A write that failed schedules one more pass through the same
	// channel every other source uses, so the retry never blocks the
	// loop that watches the compositor.
	retries <- struct{}{}
	waitForWake(t, out, time.Second)
}

func TestEventsEnded(t *testing.T) {
	// A closed wake channel means nothing while the process is
	// stopping, which is how every shutdown ends.
	stopping, cancel := context.WithCancel(context.Background())
	cancel()
	if err := eventsEnded(stopping); err != nil {
		t.Errorf("a shutdown reported an error: %v", err)
	}
	// Any other time the kernel's uevent socket closed under a running
	// operator, and an operator that kept going would publish only on
	// the backstop tick, minutes after a monitor moved.
	if err := eventsEnded(context.Background()); err == nil {
		t.Error("the event stream ended under a running operator with no error")
	}
}

func TestWatchSocketWakesWhenTheCompositorComesAndGoes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	socket := filepath.Join(dir, socketName)
	out := watchSocket(ctx, socket)

	// The compositor's container started and the socket answers.
	listener := listenOnSocket(t, socket)
	waitForWake(t, out, 2*socketWatchInterval)

	// The compositor died and left its socket file behind. Nothing
	// answers on it, so the watch reports the compositor gone.
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	waitForWake(t, out, 2*socketWatchInterval)

	// The kubelet restarted the container, which binds the path again.
	if err := os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	listenOnSocket(t, socket)
	waitForWake(t, out, 2*socketWatchInterval)
}

func TestReconcileTaintsEveryOutputWhileNoCompositorServes(t *testing.T) {
	// The kubelet restarts the compositor's container alone, and this
	// write is what says the screens serve nobody while it is down.
	compositorFixture(t)
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))

	// The socket file is there and nothing answers on it, which is what
	// a compositor killed uncleanly leaves.
	err := reconcile(client, "liken-1", testOwner(), "card1", staleSocket(t, t.TempDir()), noCurrentModes, noPanelControls)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.created == nil {
		t.Fatal("nothing published, so no slice says the screens are dark")
	}
	for _, device := range fixture.created.Spec.Devices {
		if len(device.Taints) != 1 || device.Taints[0].Key != disconnectedTaint {
			t.Errorf("%s: taints = %+v", device.Name, device.Taints)
		}
	}
}

func TestReconcileFreesTheScreensWhenTheSocketReturns(t *testing.T) {
	// The pass that follows the compositor's return re-reads the
	// connectors, so the slice carries whatever the kernel says now.
	compositorFixture(t)
	socket := servingSocket(t, t.TempDir())
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-display.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  compositorDown(sliceDevices(testOutputs(t))),
		},
	}}
	client := testClient(t, fixture.handler(t))

	if err := reconcile(client, "liken-1", testOwner(), "card1", socket, noCurrentModes, noPanelControls); err != nil {
		t.Fatal(err)
	}
	if fixture.updated == nil {
		t.Fatal("the slice was not replaced, so a stale one says every screen is dark")
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

// noPanelControls is a pass with no probe wired: it publishes no
// control attribute and puts nothing on any i2c wire, which is what
// every pass in this file runs.
var noPanelControls *panelControls

// noCurrentModes is the readback of a card that reports no mode on any
// connector, which is what a machine whose compositor is down answers.
func noCurrentModes() (map[string]string, error) {
	return nil, nil
}

// publishedModes runs one pass and answers what currentMode each
// device carries, with the devices that publish none left out.
func publishedModes(t *testing.T, current func() (map[string]string, error)) map[string]string {
	t.Helper()
	compositorFixture(t)
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))

	socket := servingSocket(t, t.TempDir())
	if err := reconcile(client, "liken-1", testOwner(), "card1", socket, current, noPanelControls); err != nil {
		t.Fatal(err)
	}
	if fixture.created == nil {
		t.Fatal("nothing published")
	}
	published := map[string]string{}
	for _, device := range fixture.created.Spec.Devices {
		attribute, stated := device.Attributes["currentMode"]
		if !stated {
			continue
		}
		published[device.Name] = *attribute.String
	}
	return published
}

func TestReconcilePublishesTheModeEachScreenRunsNow(t *testing.T) {
	// The slice says what each output runs right now, which is what
	// makes a mode a claim left behind visible instead of hidden.
	// DP-1 has no monitor, so a mode read for it publishes nothing, and
	// HDMI-A-2 answered no mode at all.
	published := publishedModes(t, func() (map[string]string, error) {
		return map[string]string{"HDMI-A-1": "3840x1600", "DP-1": "1920x1080"}, nil
	})

	if published["hdmi-a-1"] != "3840x1600" {
		t.Errorf("currentMode = %v, want hdmi-a-1 at 3840x1600", published)
	}
	if _, dark := published["dp-1"]; dark {
		t.Errorf("a connector with no monitor published a currentMode: %v", published)
	}
	if _, stated := published["hdmi-a-2"]; stated {
		t.Errorf("a connector the readback skipped published a currentMode: %v", published)
	}
}

func TestReconcilePublishesNoCurrentModeWhenTheCardCannotAnswer(t *testing.T) {
	// The attribute is absent rather than wrong when the read fails,
	// and the pass still publishes everything else the slice carries.
	published := publishedModes(t, func() (map[string]string, error) {
		return nil, errors.New("the card node is not there")
	})

	if len(published) != 0 {
		t.Errorf("currentMode = %v, want none", published)
	}
}

func TestEnvOr(t *testing.T) {
	if got := envOr("DISPLAY_OPERATOR_UNSET_IN_TESTS", "fallback"); got != "fallback" {
		t.Fatalf("envOr = %q", got)
	}
	t.Setenv("DISPLAY_OPERATOR_SET_IN_TESTS", "value")
	if got := envOr("DISPLAY_OPERATOR_SET_IN_TESTS", "fallback"); got != "value" {
		t.Fatalf("envOr = %q", got)
	}
}

func waitForWake(t *testing.T, out <-chan struct{}, within time.Duration) {
	t.Helper()
	select {
	case _, ok := <-out:
		if !ok {
			t.Fatal("the settle channel closed instead of emitting")
		}
	case <-time.After(within + time.Second):
		t.Fatal("settle never emitted")
	}
}

func assertQuiet(t *testing.T, out <-chan struct{}, within time.Duration) {
	t.Helper()
	select {
	case <-out:
		t.Fatal("settle emitted a second time for one burst")
	case <-time.After(within):
	}
}
