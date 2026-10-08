package main

// These tests cover the turns on a panel's DDC/CI bus. A panel holds
// one reply at a time, so two exchanges that overlap read each other's
// replies, and the claim's prepare, the Display pass, and the probe
// all reach the same panel from goroutines of their own.

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// The bus of the lab's LG on HDMI-A-1, with the protocol's own delays
// running on the bubble's clock.
func turnBench(t *testing.T) (*panelControls, *fakeMonitor) {
	t.Helper()
	panel := drillPanel(t, "lg-hdr-wqhd")
	controls, _ := benchPanels(t, t.TempDir(), "card1", map[string]*fakeMonitor{"HDMI-A-1": panel})
	controls.sleep = time.Sleep
	return controls, panel
}

// Two callers that read the same panel at once each get the reply to
// their own request, because each waits for the other's exchange to
// end, and the panel gets its gap between the two.
func TestTwoReadersTakeThePanelsBusInTurn(t *testing.T) {
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

// A probe that finds no answer in the cache reads the panel while the
// poll reads it too. The probe does not hold the cache while it waits
// for the bus, because the poll records each answer in the cache while
// it holds the bus.
func TestTheProbeAndThePollShareThePanelWithoutDeadlock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		controls, panel := turnBench(t)
		output := litOutput("HDMI-A-1", labMonitor())
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

// Two passes that miss the cache at once probe the panel once, and both
// read the one answer.
func TestTwoPassesThatMissTheCacheProbeOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		controls, panel := turnBench(t)
		output := litOutput("HDMI-A-1", labMonitor())
		var wg sync.WaitGroup
		answers := make([]supportedControls, 2)

		for i := range answers {
			wg.Go(func() { answers[i] = controls.of(output) })
		}
		wg.Wait()

		if panel.opens != 1 {
			t.Errorf("the panel was opened %d times, want one probe", panel.opens)
		}
		if answers[0] != answers[1] || !answers[0].Brightness {
			t.Errorf("the passes read %+v and %+v, want the same probe's answer", answers[0], answers[1])
		}
	})
}
