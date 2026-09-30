package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// The settle tests run in synctest bubbles at the operator's own
// window and limit, so each one checks the moment the wake arrives to
// the nanosecond, and takes no real time.

// emitted answers whether settle has emitted a wake, once every
// goroutine in the bubble has done all it can do.
func emitted(t *testing.T, out <-chan struct{}) bool {
	t.Helper()
	synctest.Wait()
	select {
	case _, open := <-out:
		if !open {
			t.Fatal("the settle channel closed instead of emitting")
		}
		return true
	default:
		return false
	}
}

func TestSettleCollapsesABurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := make(chan struct{}, 16)
		out := settle(t.Context(), in, settleWindow, settleLimit)

		// A monitor plugged in produces a burst of uevents, and one
		// write must cover the whole burst. Every ResourceSlice write
		// wakes every DRA-pending pod in the cluster.
		for range 8 {
			in <- struct{}{}
			time.Sleep(settleWindow / 4)
		}
		// The last event came a quarter window ago, so the wake comes
		// one window after it.
		time.Sleep(settleWindow*3/4 - 1)
		if emitted(t, out) {
			t.Fatal("settle emitted before the burst was quiet for a window")
		}
		time.Sleep(1)
		if !emitted(t, out) {
			t.Fatal("settle did not emit one window after the burst")
		}
		time.Sleep(10 * settleLimit)
		if emitted(t, out) {
			t.Fatal("settle emitted a second time for one burst")
		}
	})
}

func TestSettleWaitsForQuiet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := make(chan struct{}, 16)
		out := settle(t.Context(), in, settleWindow, settleLimit)

		in <- struct{}{}
		time.Sleep(settleWindow - 1)
		if emitted(t, out) {
			t.Fatal("settle emitted before the window passed")
		}
		time.Sleep(1)
		if !emitted(t, out) {
			t.Fatal("settle did not emit when the window passed")
		}
	})
}

func TestSettleEmitsUnderAConstantFlap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := make(chan struct{})
		out := settle(t.Context(), in, settleWindow, settleLimit)

		// A cable that flaps faster than the quiet window would restart
		// the wait forever. The limit keeps the loop publishing.
		go func() {
			tick := time.NewTicker(settleWindow / 2)
			defer tick.Stop()
			for {
				select {
				case <-t.Context().Done():
					return
				case <-tick.C:
					select {
					case in <- struct{}{}:
					case <-t.Context().Done():
						return
					}
				}
			}
		}()

		// The first event arrives half a window in, and the limit runs
		// from it.
		time.Sleep(settleWindow/2 + settleLimit - 1)
		if emitted(t, out) {
			t.Fatal("settle emitted under the flap before the limit")
		}
		time.Sleep(1)
		if !emitted(t, out) {
			t.Fatal("settle did not emit when the limit passed")
		}
	})
}

func TestSettleStopsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		in := make(chan struct{}, 1)
		out := settle(ctx, in, settleWindow, settleLimit)

		cancel()
		synctest.Wait()
		select {
		case _, open := <-out:
			if open {
				t.Fatal("settle emitted after its context ended")
			}
		default:
			t.Fatal("settle did not close its channel")
		}
	})
}

func TestWakesCarriesAnEventThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan drmEvent, 1)
	out := wakes(ctx, events, nil, nil, nil, nil, nil)

	events <- drmEvent{Action: "change", DevPath: "/devices/pci0000:00/0000:00:02.0/drm/card1"}
	waitForWake(t, out, time.Second)
}

func TestWakesCarriesTheCompositorsSocketThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sockets := make(chan struct{}, 1)
	out := wakes(ctx, nil, nil, sockets, nil, nil, nil)

	// A compositor that comes back is a pass of its own, because the
	// pass is what removes the taint and re-reads the connectors.
	sockets <- struct{}{}
	waitForWake(t, out, time.Second)
}

func TestWakesCarriesTheCompositorsConnectionThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connections := make(chan struct{}, 1)
	out := wakes(ctx, nil, nil, nil, connections, nil, nil)

	// The card gate opens when the output watch connects, and the pass
	// that follows reads the fields the closed gate cost.
	connections <- struct{}{}
	waitForWake(t, out, time.Second)
}

func TestWakesCarriesARetryThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	retries := make(chan struct{}, 1)
	out := wakes(ctx, nil, retries, nil, nil, nil, nil)

	// A write that failed schedules one more pass through the same
	// channel every other source uses, so the retry never blocks the
	// loop that watches the compositor.
	retries <- struct{}{}
	waitForWake(t, out, time.Second)
}

func TestWakesCarriesTheLayoutModulesReportsThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reports := make(chan struct{}, 1)
	out := wakes(ctx, nil, nil, nil, nil, reports, nil)

	// A surface that arrives, changes size, or goes is a pass of its
	// own, because the pass is what places what the module reports.
	reports <- struct{}{}
	waitForWake(t, out, time.Second)
}

func TestWakesCarriesTheResourceWatchesThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resources := make(chan struct{}, 1)
	out := wakes(ctx, nil, nil, nil, nil, nil, resources)

	// A pod that gained a label, an edited Layout, and a Display that
	// names another one all reach the loop here, because each of them
	// changes where a surface is drawn.
	resources <- struct{}{}
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

// scriptedProbe answers the compositor's state that the test sets, in
// place of a probe of a real socket, so the socket watch's ticker runs
// on the bubble's fake clock. probeCompositor's own tests read real
// sockets.
type scriptedProbe struct {
	mu    sync.Mutex
	live  compositorLiveness
	paths []string
}

func (p *scriptedProbe) set(live compositorLiveness) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.live = live
}

func (p *scriptedProbe) probe(socketPath string) compositorLiveness {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths = append(p.paths, socketPath)
	return p.live
}

// The three readings a probe answers.
var (
	probeServing = compositorLiveness{serving: true, reason: CompositorServingReason}
	probeDown    = compositorLiveness{reason: CompositorDownReason, detail: "connection refused"}
	probeHung    = compositorLiveness{reason: CompositorHungReason, detail: "i/o timeout"}
)

// Each change of the probe's reading wakes the loop at the first tick
// that reads it, and a tick that reads no change wakes nothing.
func TestWatchSocketWakesWhenTheCompositorComesAndGoes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := filepath.Join(t.TempDir(), socketName)
		compositor := &scriptedProbe{live: probeDown}
		out := watchSocket(t.Context(), socket, compositor.probe, nil)

		for _, step := range []struct {
			what string
			live compositorLiveness
		}{
			// The compositor's container started and the socket answers.
			{"the compositor started", probeServing},
			// The compositor died and left its socket file behind.
			{"the compositor died", probeDown},
			// The kubelet restarted the container.
			{"the compositor started again", probeServing},
		} {
			compositor.set(step.live)
			if woke(out) {
				t.Fatalf("%s: the watch woke before its next tick", step.what)
			}
			time.Sleep(socketWatchInterval)
			if !woke(out) {
				t.Fatalf("%s: the tick after it woke nothing", step.what)
			}
			time.Sleep(10 * socketWatchInterval)
			if woke(out) {
				t.Fatalf("%s: ticks that read no change woke the loop", step.what)
			}
		}
		for _, path := range compositor.paths {
			if path != socket {
				t.Fatalf("the watch probed %s, want %s", path, socket)
			}
		}
	})
}

func TestReconcileTaintsEveryOutputWhileNoCompositorServes(t *testing.T) {
	// The kubelet restarts the compositor's container alone, and this
	// write is what says the screens serve nobody while it is down.
	compositorFixture(t)
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))

	// The socket file is there and nothing answers on it, which is what
	// a compositor killed uncleanly leaves.
	err := reconcile(client, "liken-1", testOwner(), "card1", staleSocket(t, t.TempDir()), noCurrentModes, noPanelControls, newLinkHistory(), nil)
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

// The pass that writes the slice is the same pass this operator's own
// hardware-triple and observation gauges read, so a reconcile with a
// real registry behind it proves the wiring, not only the recorder.
func TestReconcileRecordsTheHardwareTripleAndTheObservations(t *testing.T) {
	compositorFixture(t)
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))
	readings := newMetrics("display-operator", "dev")

	err := reconcile(client, "liken-1", testOwner(), "card1", staleSocket(t, t.TempDir()),
		noCurrentModes, noPanelControls, newLinkHistory(), readings)
	if err != nil {
		t.Fatal(err)
	}

	connected := map[string]float64{"DP-1": 0, "HDMI-A-1": 1, "HDMI-A-2": 1}
	for output, want := range connected {
		if got := testutil.ToFloat64(readings.outputConnected.WithLabelValues(output)); got != want {
			t.Errorf("display_output_connected{output=%q} = %v, want %v", output, got, want)
		}
		if got := testutil.ToFloat64(readings.outputClaimed.WithLabelValues(output)); got != 0 {
			t.Errorf("display_output_claimed{output=%q} = %v, want 0, no claim is prepared", output, got)
		}
	}
	if got := testutil.ToFloat64(readings.observationValid.WithLabelValues("card")); got != 1 {
		t.Errorf(`display_observation_valid{source="card"} = %v, want 1`, got)
	}
	if got := testutil.ToFloat64(readings.observationValid.WithLabelValues("compositor")); got != 0 {
		t.Errorf(`display_observation_valid{source="compositor"} = %v, want 0, the socket is stale`, got)
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

	if err := reconcile(client, "liken-1", testOwner(), "card1", socket, noCurrentModes, noPanelControls, newLinkHistory(), nil); err != nil {
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

// The link on a monitor's connector goes down, because the panel shows
// another input or the receiver is renegotiating. The connector keeps
// the monitor it left with: no pass taints it, however long it stays
// dark, and every pass keeps publishing the monitor's identity, so the
// claim on that screen still allocates and the pod on it keeps drawing.
func TestAMonitorThatGoesDarkKeepsItsScreenPublished(t *testing.T) {
	compositorFixture(t)
	socket := servingSocket(t, t.TempDir())
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))
	links := newLinkHistory()
	clock := time.Now()
	links.now = func() time.Time { return clock }

	pass := func() SliceDevice {
		t.Helper()
		fixture.created = nil
		if err := reconcile(client, "liken-1", testOwner(), "card1", socket, noCurrentModes, noPanelControls, links, nil); err != nil {
			t.Fatal(err)
		}
		if fixture.created == nil {
			t.Fatal("nothing published")
		}
		for _, device := range fixture.created.Spec.Devices {
			if device.Name == "hdmi-a-1" {
				return device
			}
		}
		t.Fatal("the slice published no hdmi-a-1 device")
		return SliceDevice{}
	}

	lit := pass()
	screen := lit.Attributes[pairingAttribute].String
	if screen == nil || *screen == "" {
		t.Fatalf("the lit connector published no %s", pairingAttribute)
	}

	writeConnector(t, sysRoot, "card1", "HDMI-A-1", "")
	clock = clock.Add(disconnectGrace + time.Hour)
	dark := pass()

	if len(dark.Taints) != 0 {
		t.Errorf("the dark connector taints, and its monitor is expected back: %+v", dark.Taints)
	}
	got := dark.Attributes[pairingAttribute].String
	if got == nil || *got != *screen {
		t.Errorf("the dark connector publishes %v for %s, want %q", got, pairingAttribute, *screen)
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
	if err := reconcile(client, "liken-1", testOwner(), "card1", socket, current, noPanelControls, newLinkHistory(), nil); err != nil {
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

// A compositor that restarts is not a broken card. The card
// observation keeps what the last read measured while the gate is
// closed, and a read that fails for any other reason marks it stale.
func TestAnAbsentCompositorIsNoFailedCardObservation(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want float64
	}{
		{"the compositor is absent", errCompositorAbsent, 1},
		{"the card cannot answer", errors.New("the card node is not there"), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			compositorFixture(t)
			fixture := &slicePublishFixture{}
			client := testClient(t, fixture.handler(t))
			readings := newMetrics("display-operator", "dev")
			socket := servingSocket(t, t.TempDir())
			pass := func(current func() (map[string]string, error)) {
				t.Helper()
				if err := reconcile(client, "liken-1", testOwner(), "card1", socket,
					current, noPanelControls, newLinkHistory(), readings); err != nil {
					t.Fatal(err)
				}
			}

			pass(noCurrentModes)
			pass(func() (map[string]string, error) { return nil, c.err })

			if got := testutil.ToFloat64(readings.observationValid.WithLabelValues("card")); got != c.want {
				t.Errorf(`display_observation_valid{source="card"} = %v, want %v`, got, c.want)
			}
		})
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
