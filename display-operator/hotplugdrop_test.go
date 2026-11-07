package main

// A short hotplug drop, as each reader of the card sees it through the
// hold. An A/V receiver in standby drops hot plug detect for a fraction
// of a second, several times a minute, and the monitor behind it comes
// back unchanged each time. These tests prove that the Display
// controller, the placement pass, and the slice publisher write
// nothing for such a drop, and that each one still reports a monitor
// that stays away.

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// statusWrites counts the status writes the Display fixture's API
// server took.
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
// drop shorter than the settle time. A drop that lasts the settle time
// is a monitor that left, and the pass after the deadline reports it.
func TestTheDisplayOfAShortDropKeepsItsStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
		fixture.control.outputs = newHotplugHold(nil).wrap(fixture.outputs)
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
		if want := []string{"Normal NoPanel no panel on HDMI-A-1"}; !slices.Equal(got, want) {
			t.Errorf("a monitor that stayed away posted %q, want %q", got, want)
		}
	})
}

// The placement pass keeps a screen's windows and layout through a
// short drop: the compositor destroys the output and creates it again,
// and the Display reports the same windows before and after. A
// monitor that stays away past the settle time loses them.
func TestTheWindowsOfAShortDropStayReported(t *testing.T) {
	fixture := newPlacementFixture(t)
	clock := time.Unix(0, 0)
	hold := newHotplugHold(nil)
	hold.now = func() time.Time { return clock }
	fixture.pass.outputs = hold.wrap(fixture.outputs)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "film-0", nil)
	fixture.surface(film, 1920, 1080)
	fixture.run()
	before := fixture.status(labMonitor())
	writes := displayStatusWrites(fixture)

	fixture.darken("HDMI-A-1")
	fixture.run()
	clock = clock.Add(hotplugSettle / 2)
	fixture.run()
	fixture.light(livingRoomScreen())
	fixture.run()

	if got := displayStatusWrites(fixture) - writes; got != 0 {
		t.Errorf("the drop made %d status writes, want none", got)
	}
	if after := fixture.status(labMonitor()); !reflect.DeepEqual(after.Surfaces, before.Surfaces) {
		t.Errorf("the screen reports %+v after the drop, want %+v", after.Surfaces, before.Surfaces)
	}

	fixture.darken("HDMI-A-1")
	fixture.run()
	clock = clock.Add(hotplugSettle)
	fixture.run()

	if status := fixture.status(labMonitor()); status.Surfaces != nil || status.Layout != nil {
		t.Errorf("a monitor that stayed away reports %+v and %+v", status.Surfaces, status.Layout)
	}
}

// displayStatusWrites counts the Display status writes the placement
// fixture's API server took.
func displayStatusWrites(f *placementFixture) int {
	writes := 0
	for _, request := range f.requests {
		if strings.HasPrefix(request, http.MethodPut+" ") && strings.HasSuffix(request, "/status") {
			writes++
		}
	}
	return writes
}

// The slice publishes the same device for a connector inside a short
// drop, so the drop wakes no pod that waits on a device. A connector
// that stays dark past the settle time publishes what sysfs says.
func TestTheSliceOfAShortDropIsTheSame(t *testing.T) {
	compositorFixture(t)
	socket := servingSocket(t, t.TempDir())
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))
	clock := time.Unix(0, 0)
	hold := newHotplugHold(nil)
	hold.now = func() time.Time { return clock }
	current := func() (map[string]string, error) {
		return map[string]string{"HDMI-A-1": "3840x1600"}, nil
	}
	pass := func() SliceDevice {
		t.Helper()
		fixture.created = nil
		if err := reconcile(client, "liken-1", testOwner(), "card1", socket, current, noPanelControls,
			newLinkHistory(), hold, nil); err != nil {
			t.Fatal(err)
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

	writeConnector(t, sysRoot, "card1", "HDMI-A-1", "")
	clock = clock.Add(hotplugSettle / 2)
	if dark := pass(); !reflect.DeepEqual(dark, lit) {
		t.Errorf("the connector inside a drop publishes\n%+v\nwant\n%+v", dark, lit)
	}

	writeConnector(t, sysRoot, "card1", "HDMI-A-1", "lg-hdr-wqhd")
	pass()
	writeConnector(t, sysRoot, "card1", "HDMI-A-1", "")
	pass()
	clock = clock.Add(hotplugSettle)
	if dark := pass(); reflect.DeepEqual(dark, lit) {
		t.Error("a connector dark past the settle time still publishes its lit device")
	}
}
