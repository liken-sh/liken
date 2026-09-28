package main

// These tests cover a status write from a Display copy in the store
// that another writer changed since. The API server refuses the write,
// and each writer reads the Display again: the sweep and the dark
// report write once more only when the fresh copy still names this
// node, the placement report composes its fields onto the fresh
// status, and the Display controller wakes its next pass.

import (
	"testing"
)

// A copy that another writer changed after the store took it: the
// fixture's Display at a newer version, with its status node set.
func changedSince(display *Display, node string) {
	display.Status.Node = node
}

// The sweep reads a Display again after a conflict, and writes the
// absence only when the fresh copy still names this node. A monitor
// that moved to another node since the store's copy keeps the status
// that node wrote.
func TestTheSweepRechecksTheNodeAfterAConflict(t *testing.T) {
	for _, c := range []struct {
		name      string
		node      string
		wantWrite bool
	}{
		{"it is still this node's", "liken-1", true},
		{"another node took it", "node-2", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
			if err := fixture.pass(); err != nil {
				t.Fatal(err)
			}
			fixture.control.displays = newDisplayStore(fixture.client, storeHolding(t, displaysOf(fixture.displays)...))
			display := fixture.display()
			changedSince(display, c.node)
			fixture.store(display)
			fixture.present["HDMI-A-1"] = false

			if err := fixture.pass(); err != nil {
				t.Fatal(err)
			}

			connected := condition(fixture.display(), ConnectedCondition)
			if wrote := connected.Status == conditionFalse; wrote != c.wantWrite {
				t.Errorf("the sweep wrote the absence: %t, want %t", wrote, c.wantWrite)
			}
			if got := fixture.display().Status.Node; got != c.node {
				t.Errorf("status.node = %q, want %q", got, c.node)
			}
		})
	}
}

// A panel whose status write is refused with a conflict wakes the next
// pass at once, because a status write wakes no pass and the next
// would wait for the poll's tick.
func TestAConflictOnAPanelWakesTheNextPass(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	// The store's copy is older, with a status the pass writes over, and
	// the API server holds a newer version.
	older := *fixture.display()
	older.Status.Connector = ""
	fixture.control.displays = newDisplayStore(fixture.client, storeHolding(t, older))
	fixture.store(fixture.display())

	_ = fixture.pass()

	select {
	case <-fixture.control.wakes:
	default:
		t.Fatal("the conflict woke no pass")
	}
}

// The placement report reads the Display again after a conflict and
// composes its fields onto the fresh status, so the surfaces land and
// the field another writer wrote stays.
func TestAPlacementReportAfterAConflictLandsOnTheFreshStatus(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	fixture.pass.displays = newDisplayStore(fixture.client, storeHolding(t, displaysOf(fixture.displays)...))
	display := fixture.displays[labDisplayName()]
	display.Status.Connector = "HDMI-A-1"
	fixture.store(display)
	fixture.surface(film, 1920, 1080)

	fixture.run()

	status := fixture.status(labMonitor())
	if len(status.Surfaces) != 1 {
		t.Errorf("status holds %+v, want the one surface", status.Surfaces)
	}
	if status.Connector != "HDMI-A-1" {
		t.Errorf("connector = %q, want the one the other writer wrote", status.Connector)
	}
}

// The dark report reads the Display again after a conflict, and
// leaves it when the fresh copy names another node.
func TestTheDarkReportLeavesADisplayAnotherNodeTook(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	fixture.pass.displays = newDisplayStore(fixture.client, storeHolding(t, displaysOf(fixture.displays)...))
	display := fixture.displays[labDisplayName()]
	changedSince(display, "node-2")
	fixture.store(display)
	fixture.probes(compositorLiveness{reason: CompositorHungReason, detail: "i/o timeout"})

	fixture.run()

	if serving := conditionByType(fixture.status(labMonitor()), CompositorServingCondition); serving.Type != "" {
		t.Errorf("the dark report wrote %+v on another node's Display", serving)
	}
}
