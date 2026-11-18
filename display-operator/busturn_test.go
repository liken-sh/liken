package main

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// turnBench returns the controls and the fake panel on HDMI-A-1, with
// the DDC delays on the synctest bubble's clock.
func turnBench(t *testing.T) (*panelControls, *fakeMonitor) {
	t.Helper()
	panel := recordedPanel(t, "lg-hdr-wqhd")
	controls, _ := benchPanels(t, t.TempDir(), "card1", map[string]*fakeMonitor{"HDMI-A-1": panel})
	controls.sleep = time.Sleep
	return controls, panel
}

func TestTwoReadersGetTheirOwnRepliesFromOnePanel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		controls, panel := turnBench(t)
		panel.values[vcpBrightness], panel.values[vcpContrast] = 70, 30
		var wg sync.WaitGroup
		read := map[byte]uint16{}
		var failures []error
		var mu sync.Mutex
		for _, code := range []byte{vcpBrightness, vcpContrast} {
			wg.Go(func() {
				current, _, err := controls.readControl("HDMI-A-1", code)
				mu.Lock()
				defer mu.Unlock()
				read[code] = current
				if err != nil {
					failures = append(failures, err)
				}
			})
		}
		wg.Wait()

		if len(failures) != 0 {
			t.Fatalf("the reads failed: %v", failures)
		}
		if read[vcpBrightness] != 70 || read[vcpContrast] != 30 {
			t.Errorf("brightness read %d and contrast %d, want 70 and 30", read[vcpBrightness], read[vcpContrast])
		}
		if panel.overlaps != 0 || panel.rushed != 0 {
			t.Errorf("the panel saw %d overlapping holders and %d messages inside the gap, want none",
				panel.overlaps, panel.rushed)
		}
	})
}

// The probe must not hold the cache lock while it waits for the bus,
// because the poll takes that lock to record each value while it has
// the bus open.
func TestTheProbeAndThePollReadOnePanelWithoutDeadlock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		controls, panel := turnBench(t)
		output := connectedOutput("HDMI-A-1", labMonitor())
		controls.factsFor(output)
		controls.probed["HDMI-A-1"] = probedPanel{monitor: EDID{}, facts: controls.probed["HDMI-A-1"].facts}
		var wg sync.WaitGroup

		wg.Go(func() { controls.factsFor(output) })
		wg.Go(func() { _, _ = controls.pollControls("HDMI-A-1") })
		wg.Wait()

		if panel.overlaps != 0 {
			t.Errorf("the panel saw %d overlapping holders, want none", panel.overlaps)
		}
	})
}

func TestTwoPassesThatMissTheCacheProbeThePanelOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		controls, panel := turnBench(t)
		output := connectedOutput("HDMI-A-1", labMonitor())
		var wg sync.WaitGroup
		results := make([]supportedControls, 2)

		for i := range results {
			wg.Go(func() { results[i] = controls.of(output) })
		}
		wg.Wait()

		if panel.opens != 1 {
			t.Errorf("the panel was opened %d times, want one probe", panel.opens)
		}
		if results[0] != results[1] || !results[0].Brightness {
			t.Errorf("the passes read %+v and %+v, want the result of the same probe", results[0], results[1])
		}
	})
}
