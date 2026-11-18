package main

// declare starts each screen at the mode in its Display's spec.mode,
// because weston reads its config only at startup and a later mode
// change restarts the compositor, which blanks every screen on the
// card. A new pod has an empty mode record, so without the Displays
// weston would start at each preferred mode and the Display pass would
// restart it to apply spec.mode.

import (
	"fmt"
	"os"
	"slices"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// addDisplayModes adds the spec.mode of each connected monitor's Display to
// the record and returns the record.
//
// It skips a connector that already has an entry, because a claim set
// that mode and a claim's mode takes precedence. It skips a mode that
// the connector does not list, because weston silently falls back to
// the preferred mode, and the Display pass reports the error with the
// modes that the connector offers. It skips a mode that the compositor
// declined in the Display's current generation, because the Display
// pass does not try that mode again until the spec changes.
func addDisplayModes(record map[string]string, outputs []Output, displays []Display) map[string]string {
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
		if writeRecordsOf(display).isUnconfirmed(modeControl, mode) {
			continue
		}
		record[output.Connector] = mode
		fmt.Printf("%s: %s starts at the mode %s that its Display sets in spec.mode\n",
			DriverName, output.Connector, mode)
	}
	return record
}

// declaredDisplaysFromAPI lists every Display for declare. When the list
// fails it logs the error and returns none, so weston starts at each
// preferred mode and the Display pass applies spec.mode later.
func declaredDisplaysFromAPI() []Display {
	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating the API client to read the declared modes: %v\n", err)
		return nil
	}
	displays, err := listDisplays(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing the Displays to read the declared modes: %v\n", err)
		return nil
	}
	return displays
}
