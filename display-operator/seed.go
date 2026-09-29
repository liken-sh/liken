package main

// The resting modes a new compositor starts at.
//
// Weston reads its config once, at startup, and a mode change after
// that is a restart that blanks every screen on the card. A new pod
// starts with an empty mode record, so a config built from the record
// alone starts every screen at its preferred mode, and the Display
// pass then restarts the compositor to reach the mode spec.mode
// states. That is two modesets where one does. So the declare
// container reads each monitor's Display before it writes the config,
// and the first compositor starts at the resting mode.

import (
	"fmt"
	"os"
	"slices"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// seedModes adds the resting mode of each monitor on the card to the
// record, and returns the record.
//
// An entry the record already holds stays: it is a claim's mode, and
// a claim's mode wins for the claim's lifetime. A mode the connector
// does not offer by name is left out, because weston falls back to
// the preferred mode with no log line, and the Display pass reports
// the mode against the card's full list. A mode the compositor
// declined in this spec generation is left out too, because the
// compositor would decline it again.
func seedModes(record map[string]string, outputs []Output, displays []Display) map[string]string {
	byName := map[string]*Display{}
	for i := range displays {
		byName[displays[i].Metadata.Name] = &displays[i]
	}
	for _, output := range outputs {
		if !output.Connected || record[output.Connector] != "" {
			continue
		}
		display := byName[monitorID(output.Monitor)]
		if display == nil || display.Spec.Mode == nil {
			continue
		}
		mode := *display.Spec.Mode
		requested, err := parseMode(mode)
		if err != nil || !slices.Contains(output.Modes, requested.Name) {
			continue
		}
		if ledgerOf(display).declined(modeControl, mode) {
			continue
		}
		record[output.Connector] = mode
		fmt.Printf("%s: %s starts at the mode %s that its Display states\n",
			DriverName, output.Connector, mode)
	}
	return record
}

// restingDisplays reads every Display for the declare container. A
// read that fails is reported and answers none: the compositor then
// starts at each monitor's preferred mode, and the Display pass
// applies the resting mode later with one restart.
func restingDisplays() []Display {
	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the resting modes: %v\n", err)
		return nil
	}
	displays, err := listDisplays(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the resting modes: %v\n", err)
		return nil
	}
	return displays
}
