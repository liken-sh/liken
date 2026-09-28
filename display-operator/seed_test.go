package main

import (
	"maps"
	"os"
	"strings"
	"testing"
)

// The Display of the portable panel on HDMI-A-2, resting at a mode,
// and with the records its status carries.
func portableResting(t *testing.T, mode string, generation int64, unconfirmed ...DisplayUnconfirmed) Display {
	t.Helper()
	var portable Output
	for _, output := range discoverOutputs(labSysfs(t), "card1") {
		if output.Connector == "HDMI-A-2" {
			portable = output
		}
	}
	return Display{
		Metadata: DisplayMeta{Name: monitorID(portable.Monitor), Generation: generation},
		Spec:     DisplaySpec{Mode: &mode},
		Status:   DisplayStatus{Unconfirmed: unconfirmed},
	}
}

func declinedMode(mode string, generation int64) DisplayUnconfirmed {
	return DisplayUnconfirmed{Control: modeControl, Value: mode, Generation: generation}
}

func TestSeedModesStartsEachScreenAtItsRestingMode(t *testing.T) {
	cases := []struct {
		name    string
		record  map[string]string
		display Display
		want    map[string]string
	}{
		{
			name:    "a resting mode the connector offers",
			record:  map[string]string{},
			display: portableResting(t, "1280x720", 1),
			want:    map[string]string{"HDMI-A-2": "1280x720"},
		},
		{
			name:    "a resting mode with a refresh",
			record:  map[string]string{},
			display: portableResting(t, "1280x720@60", 1),
			want:    map[string]string{"HDMI-A-2": "1280x720@60"},
		},
		{
			name:    "a claim's mode already in the record",
			record:  map[string]string{"HDMI-A-2": "1600x900"},
			display: portableResting(t, "1280x720", 1),
			want:    map[string]string{"HDMI-A-2": "1600x900"},
		},
		{
			name:    "a resting mode the connector does not offer",
			record:  map[string]string{},
			display: portableResting(t, "1234x567", 1),
			want:    map[string]string{},
		},
		{
			name:    "a resting mode the compositor declined in this generation",
			record:  map[string]string{},
			display: portableResting(t, "1280x720", 2, declinedMode("1280x720", 2)),
			want:    map[string]string{},
		},
		{
			name:    "a resting mode the compositor declined in an earlier generation",
			record:  map[string]string{},
			display: portableResting(t, "1280x720", 3, declinedMode("1280x720", 2)),
			want:    map[string]string{"HDMI-A-2": "1280x720"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			outputs := discoverOutputs(labSysfs(t), "card1")

			got := seedModes(c.record, outputs, []Display{c.display})

			if !maps.Equal(got, c.want) {
				t.Errorf("record = %v, want %v", got, c.want)
			}
		})
	}
}

// A new pod's first compositor starts at the resting mode, so the
// screen takes one modeset and the Display pass finds it at the mode
// and restarts nothing.
func TestDeclareStartsTheCompositorAtTheRestingMode(t *testing.T) {
	compositorFixture(t)
	swapDisplays(t, []Display{portableResting(t, "1280x720", 1)})

	declare()

	written, err := os.ReadFile(westonConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "name=HDMI-A-2\nmode=1280x720\n") {
		t.Fatalf("the file holds:\n%s", written)
	}
	record, err := readModeRecord(modeRecordPath)
	if err != nil {
		t.Fatal(err)
	}
	if record["HDMI-A-2"] != "1280x720" {
		t.Errorf("record = %v, want the resting mode", record)
	}
}
