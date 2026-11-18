package main

// Tests of a status write from a Display in the display store that
// another writer has changed since. The API server rejects the write
// with a conflict, and the writer reads the Display again.

import (
	"testing"
)

func setStatusNode(display *Display, node string) {
	display.Status.Node = node
}

// After a conflict, the Display pass's sweep writes the disconnected
// status only when the fresh copy still has this node in status.node.
func TestTheSweepRechecksTheNodeAfterAConflict(t *testing.T) {
	for _, c := range []struct {
		name      string
		node      string
		wantWrite bool
	}{
		{"it is still this node's", "liken-1", true},
		{"another node has it", "node-2", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
			if err := fixture.pass(); err != nil {
				t.Fatalf("running the pass: %v", err)
			}
			fixture.control.displays = newDisplayStore(fixture.client, storeHolding(t, displaysOf(fixture.displays)...))
			display := fixture.display()
			setStatusNode(display, c.node)
			fixture.store(display)
			fixture.present["HDMI-A-1"] = false

			if err := fixture.pass(); err != nil {
				t.Fatalf("running the pass: %v", err)
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

// When the API server rejects a Display's status write with a
// conflict, the controller wakes the next pass at once. A status write
// triggers no pass, so without the wake the next pass would wait for
// the poll interval.
func TestAConflictOnAPanelWakesTheNextPass(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	// The display store's copy is older than the API server's, and its
	// status differs from what the pass writes.
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

// After a conflict, the placement report writes its surfaces onto the
// fresh status and keeps the connector that the other writer set.
func TestAPlacementReportAfterAConflictKeepsTheFreshStatus(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "film-0", map[string]string{"media.liken.sh/focus": "true"})
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

// After a conflict, reportNoCompositor does not write a Display whose
// fresh copy has another node in status.node.
func TestReportNoCompositorLeavesADisplayAnotherNodeTook(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	fixture.pass.displays = newDisplayStore(fixture.client, storeHolding(t, displaysOf(fixture.displays)...))
	display := fixture.displays[labDisplayName()]
	setStatusNode(display, "node-2")
	fixture.store(display)
	fixture.probes(compositorLiveness{reason: CompositorHungReason, detail: "i/o timeout"})

	fixture.run()

	if serving := conditionByType(fixture.status(labMonitor()), CompositorServingCondition); serving.Type != "" {
		t.Errorf("the report of the hung compositor wrote %+v on another node's Display", serving)
	}
}
