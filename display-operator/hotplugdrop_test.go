package main

// An A/V receiver in standby can drop hot plug detect for a moment and
// then serve the same monitor again. The hotplug hold hides a drop
// shorter than hotplugSettle, so the Display controller, the placement
// pass, and the slice publisher write nothing for it. Each of them
// still reports a monitor that stays away for hotplugSettle.

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// statusWrites counts the status writes that the Display fixture's
// API server received.
func statusWrites(f *displayFixture) int {
	writes := 0
	for _, line := range f.journal.read() {
		if strings.HasPrefix(line, "status ") {
			writes++
		}
	}
	return writes
}

// The Display controller writes no status and posts no Event for a
// drop shorter than hotplugSettle. A drop that lasts hotplugSettle is
// a monitor that left, and the next pass reports it.
func TestTheDisplayOfAShortDropKeepsItsStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
		fixture.control.outputs = newHotplugHold(nil, nil).view().wrap(fixture.outputs)
		fixture.passes(2)
		synctest.Wait()
		writes := statusWrites(fixture)
		events := len(fixture.events.About("Display", labDisplayName()))

		fixture.present["HDMI-A-1"] = false
		fixture.passes(2)
		time.Sleep(hotplugSettle / 2)
		fixture.passes(1)
		fixture.present["HDMI-A-1"] = true
		fixture.passes(2)
		synctest.Wait()

		if got := statusWrites(fixture) - writes; got != 0 {
			t.Errorf("the drop made %d status writes, want none", got)
		}
		if got := fixture.events.About("Display", labDisplayName())[events:]; len(got) != 0 {
			t.Errorf("the drop posted %q, want nothing", eventLines(got))
		}

		fixture.present["HDMI-A-1"] = false
		fixture.passes(1)
		time.Sleep(hotplugSettle)
		fixture.passes(1)
		synctest.Wait()

		got := eventLines(fixture.events.About("Display", labDisplayName())[events:])
		if want := []string{"Normal NoPanel No panel is connected to HDMI-A-1"}; !slices.Equal(got, want) {
			t.Errorf("a monitor that stayed away posted %q, want %q", got, want)
		}
	})
}

// An operator that starts during a drop has never read a monitor on the
// connector. assumeConnected fills the hotplug view from the Displays
// that the earlier process published, so the new process waits hotplugSettle
// for this drop like any other before it reports the panel gone.
func TestAnOperatorThatStartsDuringADropKeepsTheDisplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
		fixture.passes(2)
		synctest.Wait()
		writes := statusWrites(fixture)
		events := len(fixture.events.About("Display", labDisplayName()))

		fixture.present["HDMI-A-1"] = false
		links := newLinkHistory()
		var published []Display
		for _, display := range fixture.displays {
			published = append(published, *display)
		}
		links.loadFromDisplays(published, "liken-1", fixture.outputs())
		view := newHotplugHold(nil, nil).view()
		view.assumeConnected(rememberedScreens(fixture.outputs(), links))
		fixture.control.outputs = view.wrap(fixture.outputs)
		fixture.passes(1)
		synctest.Wait()

		if got := statusWrites(fixture) - writes; got != 0 {
			t.Errorf("the first passes of the new operator made %d status writes, want none", got)
		}

		time.Sleep(hotplugSettle)
		fixture.passes(1)
		synctest.Wait()

		got := eventLines(fixture.events.About("Display", labDisplayName())[events:])
		if want := []string{"Normal NoPanel No panel is connected to HDMI-A-1"}; !slices.Equal(got, want) {
			t.Errorf("a panel that stayed away posted %q, want %q", got, want)
		}
	})
}

// The compositor removes the output during a drop and adds it again
// after. The Display reports the same windows before and after a short
// drop, and none after a monitor stays away for hotplugSettle.
func TestTheWindowsOfAShortDropStayReported(t *testing.T) {
	fixture := newPlacementFixture(t)
	clock := time.Unix(0, 0)
	hold := newHotplugHold(nil, nil)
	hold.now = func() time.Time { return clock }
	fixture.pass.outputs = hold.view().wrap(fixture.outputs)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "film-0", nil)
	fixture.surface(film, 1920, 1080)
	fixture.run()
	before := fixture.status(labMonitor())
	writes := displayStatusWrites(fixture)

	fixture.disconnect("HDMI-A-1")
	fixture.run()
	clock = clock.Add(hotplugSettle / 2)
	fixture.run()
	fixture.reconnect(primaryScreen())
	fixture.run()

	if got := displayStatusWrites(fixture) - writes; got != 0 {
		t.Errorf("the drop made %d status writes, want none", got)
	}
	if after := fixture.status(labMonitor()); !reflect.DeepEqual(after.Surfaces, before.Surfaces) {
		t.Errorf("the screen reports %+v after the drop, want %+v", after.Surfaces, before.Surfaces)
	}

	fixture.disconnect("HDMI-A-1")
	fixture.run()
	clock = clock.Add(hotplugSettle)
	fixture.run()

	if status := fixture.status(labMonitor()); status.Surfaces != nil || status.Layout != nil {
		t.Errorf("a monitor that stayed away reports surfaces %+v and layout %+v, want both nil", status.Surfaces, status.Layout)
	}
}

// displayStatusWrites counts the Display status writes that the
// placement fixture's API server received.
func displayStatusWrites(f *placementFixture) int {
	writes := 0
	for _, request := range f.requests {
		if strings.HasPrefix(request, http.MethodPut+" ") && strings.HasSuffix(request, "/status") {
			writes++
		}
	}
	return writes
}

// slicePass runs the slice publisher's pass with the same hotplug hold
// and link history on each run, as one operator process keeps them. run
// returns the device it published for HDMI-A-1.
type slicePass struct {
	t       *testing.T
	fixture *slicePublishFixture
	client  *apiclient.Client
	socket  string
	view    *hotplugView
	links   *linkHistory
}

func newSlicePass(t *testing.T, clock *time.Time) *slicePass {
	t.Helper()
	compositorFixture(t)
	fixture := &slicePublishFixture{}
	hold := newHotplugHold(nil, nil)
	hold.now = func() time.Time { return *clock }
	links := newLinkHistory()
	links.now = func() time.Time { return *clock }
	return &slicePass{t: t, fixture: fixture, client: testClient(t, fixture.handler(t)),
		socket: servingSocket(t, t.TempDir()), view: hold.view(), links: links}
}

func (p *slicePass) run() SliceDevice {
	p.t.Helper()
	p.fixture.created = nil
	current := func() (map[string]string, error) {
		return map[string]string{"HDMI-A-1": "3840x1600"}, nil
	}
	if err := reconcile(p.client, "liken-1", testOwner(), "card1", p.socket, current, noPanelControls,
		p.links, p.view, nil); err != nil {
		p.t.Fatalf("reconciling the ResourceSlice: %v", err)
	}
	for _, device := range p.fixture.created.Spec.Devices {
		if device.Name == "hdmi-a-1" {
			return device
		}
	}
	p.t.Fatal("the ResourceSlice has no device hdmi-a-1, want one")
	return SliceDevice{}
}

// The slice publishes the same device at the start of a short drop,
// during it, and after it, so the drop does not wake a pod that waits
// for a device. After hotplugSettle with no monitor, the slice
// publishes what sysfs reports.
func TestTheSliceOfAShortDropIsTheSame(t *testing.T) {
	clock := time.Unix(0, 0)
	slice := newSlicePass(t, &clock)
	lit := slice.run()

	writeConnector(t, sysRoot, "card1", "HDMI-A-1", "")
	start := slice.run()
	clock = clock.Add(hotplugSettle / 2)
	during := slice.run()
	writeConnector(t, sysRoot, "card1", "HDMI-A-1", "lg-hdr-wqhd")
	back := slice.run()
	for name, device := range map[string]SliceDevice{"as it starts": start, "while it lasts": during, "after it": back} {
		if !reflect.DeepEqual(device, lit) {
			t.Errorf("the connector %s publishes\n%+v\nwant\n%+v", name, device, lit)
		}
	}

	writeConnector(t, sysRoot, "card1", "HDMI-A-1", "")
	slice.run()
	clock = clock.Add(hotplugSettle)
	if dark := slice.run(); reflect.DeepEqual(dark, lit) {
		t.Error("a connector disconnected past the settle time still publishes its connected device")
	}
}

// A different monitor that arrives during a drop is a swap. The device
// gets the disconnected taint, as for any swap, because the claim on
// the connector asked for the monitor that left.
func TestAMonitorSwappedInsideTheHoldTaintsTheDevice(t *testing.T) {
	clock := time.Unix(0, 0)
	slice := newSlicePass(t, &clock)
	slice.run()

	writeConnector(t, sysRoot, "card1", "HDMI-A-1", "")
	slice.run()
	clock = clock.Add(time.Second)
	writeConnector(t, sysRoot, "card1", "HDMI-A-1", "portable-display")
	swapped := slice.run()

	if !slices.ContainsFunc(swapped.Taints, func(taint DeviceTaint) bool { return taint.Key == disconnectedTaint }) {
		t.Errorf("the swapped monitor publishes with %+v, want the %s taint", swapped.Taints, disconnectedTaint)
	}
}
