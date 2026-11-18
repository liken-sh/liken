package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// configEntry is one entry of an allocation's config with a block for
// this driver.
func configEntry(source, requests, parameters string) string {
	return fmt.Sprintf(`{"source": %q, "requests": [%s], "opaque": {"driver": %q, "parameters": %s}}`,
		source, requests, DriverName, parameters)
}

// claimMode is a claim's entry that applies to every request.
func claimMode(parameters string) string {
	return configEntry(configFromClaim, "", parameters)
}

// classMode is a DeviceClass's entry that applies to every request.
func classMode(parameters string) string {
	return configEntry(configFromClass, "", parameters)
}

// resolvedConfig parses entries as the config of a claim's
// status.allocation. The driver reads this list and not the claim's
// spec.devices.config, because the scheduler copies the claim's entries
// here beside the DeviceClass's entries and records the source of each.
func resolvedConfig(t *testing.T, entries string) []AllocatedConfig {
	t.Helper()
	claim := &ResourceClaim{}
	document := fmt.Sprintf(`{"status":{"allocation":{"devices":{"results":[],"config":[%s]}}}}`, entries)
	if err := json.Unmarshal([]byte(document), claim); err != nil {
		t.Fatalf("parsing the claim document: %v", err)
	}
	if claim.Status.Allocation == nil {
		t.Fatal("the claim document has no allocation, want one")
	}
	return claim.Status.Allocation.Devices.Config
}

func TestClaimModesReadsTheOpaqueBlock(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		request string
		want    string
	}{
		{name: "a claim with no config block", request: "screen"},
		{
			name:    "a mode for every request",
			config:  claimMode(`{"mode": "1280x720"}`),
			request: "screen",
			want:    "1280x720",
		},
		{
			name: "another driver's block",
			config: `{"source": "FromClaim", "opaque": {"driver": "audio.liken.sh",
			          "parameters": {"codec": "sbc"}}}`,
			request: "screen",
			want:    "",
		},
		{
			name:    "a block that names the requests it applies to",
			config:  configEntry(configFromClaim, `"screen"`, `{"mode": "1280x720"}`),
			request: "screen",
			want:    "1280x720",
		},
		{
			name:    "a block that names another request",
			config:  configEntry(configFromClaim, `"second-screen"`, `{"mode": "1280x720"}`),
			request: "screen",
			want:    "",
		},
		{
			name: "a request's own block over the claim's",
			config: claimMode(`{"mode": "1920x1080"}`) + "," +
				configEntry(configFromClaim, `"screen"`, `{"mode": "1280x720"}`),
			request: "screen",
			want:    "1280x720",
		},
		{
			name:    "the class's mode, with none in the claim",
			config:  classMode(`{"mode": "1920x1080"}`),
			request: "screen",
			want:    "1920x1080",
		},
		{
			name:    "the claim's mode over the class's",
			config:  classMode(`{"mode": "1920x1080"}`) + "," + claimMode(`{"mode": "1280x720"}`),
			request: "screen",
			want:    "1280x720",
		},
		{
			// The allocator lists the class's entries first, but the
			// result must not depend on the order.
			name:    "the claim's mode, listed before the class's",
			config:  claimMode(`{"mode": "1280x720"}`) + "," + classMode(`{"mode": "1920x1080"}`),
			request: "screen",
			want:    "1280x720",
		},
		{
			name: "the claim's every-request block over the class's named one",
			config: configEntry(configFromClass, `"screen"`, `{"mode": "1920x1080"}`) + "," +
				claimMode(`{"mode": "1280x720"}`),
			request: "screen",
			want:    "1280x720",
		},
		{
			name: "the class's named block, with the claim naming no request",
			config: configEntry(configFromClass, `"screen"`, `{"mode": "1920x1080"}`) + "," +
				classMode(`{"mode": "1280x720"}`),
			request: "screen",
			want:    "1920x1080",
		},
		{
			name:    "a block with empty parameters",
			config:  claimMode(`{}`),
			request: "screen",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			selection, err := claimModes(resolvedConfig(t, c.config))
			if err != nil {
				t.Fatalf("claimModes: %v", err)
			}
			if got := selection.forRequest(c.request); got != c.want {
				t.Errorf("the mode is %q, want %q", got, c.want)
			}
		})
	}
}

func TestClaimModesRefusesParametersItCannotRead(t *testing.T) {
	cases := []struct {
		name        string
		config      string
		wantInError string
	}{
		{
			name:        "a key this driver does not read",
			config:      claimMode(`{"resolution": "1280x720"}`),
			wantInError: `"resolution"`,
		},
		{
			name:        "a key the class does not read either",
			config:      classMode(`{"resolution": "1280x720"}`),
			wantInError: `"resolution"`,
		},
		{
			name:        "a mode that is not a string",
			config:      claimMode(`{"mode": 720}`),
			wantInError: "not a string",
		},
		{
			name:        "parameters that are not an object",
			config:      claimMode(`["1280x720"]`),
			wantInError: "parameters",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := claimModes(resolvedConfig(t, c.config))
			if err == nil {
				t.Fatal("claimModes accepted parameters it cannot read, want an error")
			}
			if !strings.Contains(err.Error(), c.wantInError) {
				t.Errorf("error is %q, want it to contain %q", err, c.wantInError)
			}
		})
	}
}

func TestParseModeReadsTheRefresh(t *testing.T) {
	cases := []struct {
		mode        string
		wantName    string
		wantRefresh int
	}{
		{mode: "1280x720", wantName: "1280x720"},
		{mode: "3840x1600@24", wantName: "3840x1600", wantRefresh: 24},
		{mode: "1920x1080@60", wantName: "1920x1080", wantRefresh: 60},
	}
	for _, c := range cases {
		t.Run(c.mode, func(t *testing.T) {
			parsed, err := parseMode(c.mode)
			if err != nil {
				t.Fatalf("parseMode(%q): %v", c.mode, err)
			}
			if parsed.Name != c.wantName || parsed.Refresh != c.wantRefresh {
				t.Errorf("parseMode(%q) = %+v, want %s at %d", c.mode, parsed, c.wantName, c.wantRefresh)
			}
		})
	}
}

func TestParseModeRefusesARefreshThatIsNotAWholeNumber(t *testing.T) {
	cases := []string{"3840x1600@59.94", "1280x720@sixty", "1280x720@", "1280x720@0", "1280x720@-60"}
	for _, mode := range cases {
		t.Run(mode, func(t *testing.T) {
			if _, err := parseMode(mode); err == nil {
				t.Fatalf("parseMode(%q) returned no error, want one", mode)
			}
		})
	}
}

func TestModeMatchesTheReadback(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		current   string
		want      bool
	}{
		{name: "a name against the refresh the card reports", requested: "1920x1080", current: "1920x1080@60", want: true},
		{name: "the same name and refresh", requested: "1920x1080@60", current: "1920x1080@60", want: true},
		{name: "another refresh of the same name", requested: "1920x1080@50", current: "1920x1080@60"},
		{name: "another name", requested: "1280x720", current: "1920x1080@60"},
		{name: "an output that runs nothing", requested: "1920x1080", current: ""},
		{name: "a card that reports no refresh", requested: "1920x1080", current: "1920x1080", want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := modeMatches(c.requested, c.current); got != c.want {
				t.Errorf("modeMatches(%q, %q) = %v, want %v", c.requested, c.current, got, c.want)
			}
		})
	}
}

// portableModes is the GETCONNECTOR list of the lab's portable panel.
// A name with two refreshes appears twice, and an aspect ratio variant
// repeats a name and refresh.
func portableModes() []drmMode {
	return []drmMode{
		{Name: "1920x1080", Refresh: 60},
		{Name: "1920x1080", Refresh: 50},
		{Name: "1280x720", Refresh: 60},
		{Name: "1280x720", Refresh: 60},
		{Name: "1280x720", Refresh: 24},
		{Name: "640x480", Refresh: 60},
	}
}

func TestValidateModeAcceptsWhatTheConnectorOffers(t *testing.T) {
	cases := []struct {
		name string
		mode string
	}{
		{name: "a name with no refresh", mode: "1920x1080"},
		{name: "a name and the refresh it runs", mode: "1920x1080@60"},
		{name: "a second refresh of the same name", mode: "1920x1080@50"},
		{name: "a name that appears under several entries", mode: "1280x720@24"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validateMode("HDMI-A-2", portableModes(), c.mode); err != nil {
				t.Errorf("validateMode(%q) = %v, want no error", c.mode, err)
			}
		})
	}
}

func TestValidateModeRefusesWhatTheConnectorDoesNotOffer(t *testing.T) {
	cases := []struct {
		name        string
		modes       []drmMode
		mode        string
		wantInError []string
	}{
		{
			name:        "a name the connector does not offer",
			modes:       portableModes(),
			mode:        "1234x567",
			wantInError: []string{"1234x567", "1920x1080 1280x720 640x480"},
		},
		{
			name:        "a refresh the connector does not offer",
			modes:       portableModes(),
			mode:        "1920x1080@24",
			wantInError: []string{"1920x1080@24", "60", "50"},
		},
		{
			name:        "a refresh that is not a whole number",
			modes:       portableModes(),
			mode:        "1920x1080@59.94",
			wantInError: []string{"59.94"},
		},
		{
			name:        "a connector the card reports no modes for",
			mode:        "1920x1080",
			wantInError: []string{"HDMI-A-2"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateMode("HDMI-A-2", c.modes, c.mode)
			if err == nil {
				t.Fatalf("validateMode(%q) returned no error, want one", c.mode)
			}
			for _, want := range c.wantInError {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error is %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestReadModeRecordOfAFileThatIsNotThere(t *testing.T) {
	record, err := readModeRecord(filepath.Join(t.TempDir(), "modes.json"))
	if err != nil {
		t.Fatalf("reading a record that does not exist: %v", err)
	}
	if len(record) != 0 {
		t.Fatalf("the record is %v, want no entries", record)
	}
}

func TestModeRecordSurvivesAWriteAndARead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weston", "modes.json")

	if err := writeModeRecord(path, map[string]string{"HDMI-A-2": "1280x720"}); err != nil {
		t.Fatalf("writing the mode record: %v", err)
	}
	record, err := readModeRecord(path)
	if err != nil {
		t.Fatalf("reading the mode record: %v", err)
	}
	if record["HDMI-A-2"] != "1280x720" {
		t.Fatalf("the record is %v, want HDMI-A-2 at 1280x720", record)
	}
}

func TestReadModeRecordRefusesAFileItCannotParse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "modes.json")
	if err := os.WriteFile(path, []byte("1280x720"), 0o644); err != nil {
		t.Fatalf("writing the record file: %v", err)
	}

	if _, err := readModeRecord(path); err == nil {
		t.Fatal("readModeRecord returned no error for a file it cannot parse, want one")
	}
}

func TestEndingAHungCompositorKillsItAndCountsItAsHung(t *testing.T) {
	var killed []syscall.Signal
	readings := newMetrics(componentName, "dev")
	plugin := &draPlugin{
		compositors: func() []int { return []int{14} },
		signal: func(_ int, signal syscall.Signal) error {
			killed = append(killed, signal)
			return nil
		},
		restartRequests: restartRequests(t.TempDir()),
		metrics:         readings,
	}

	if err := plugin.killHungCompositor(); err != nil {
		t.Fatalf("killHungCompositor: %v", err)
	}

	if !slices.Equal(killed, []syscall.Signal{syscall.SIGKILL}) {
		t.Errorf("killHungCompositor sent %v, want one SIGKILL", killed)
	}
	if got := testutil.ToFloat64(readings.compositorRestarts.WithLabelValues("hung")); got != 1 {
		t.Errorf("display_compositor_restarts_total{reason=\"hung\"} = %v, want 1", got)
	}
}

func TestAKillThatFoundNoCompositorCountsNothing(t *testing.T) {
	readings := newMetrics(componentName, "dev")
	plugin := &draPlugin{
		compositors: func() []int { return nil },
		metrics:     readings,
	}

	if err := plugin.killHungCompositor(); err == nil {
		t.Fatal("killHungCompositor returned no error when it found no compositor, want one")
	}
	if got := testutil.ToFloat64(readings.compositorRestarts.WithLabelValues("hung")); got != 0 {
		t.Errorf("display_compositor_restarts_total{reason=\"hung\"} = %v, want 0", got)
	}
}

// The Display pass tests for errModeDeclined, so both declines return
// it: the switch whose restart left another mode, and the next switch,
// which p.restarted refuses without a restart.
func TestADeclinedModeReturnsErrModeDeclined(t *testing.T) {
	plugin, compositor := labPluginWithConfig(t, screenRequest(), "")
	compositor.declines = true
	var portable Output
	for _, output := range discoverOutputs(plugin.sysRoot, plugin.card) {
		if output.Connector == "HDMI-A-2" {
			portable = output
		}
	}

	first := plugin.applyMode(t.Context(), portable, "1280x720")
	second := plugin.applyMode(t.Context(), portable, "1280x720")

	if !errors.Is(first, errModeDeclined) {
		t.Errorf("the switch that restarted the compositor returned %v, want errModeDeclined", first)
	}
	if !errors.Is(second, errModeDeclined) {
		t.Errorf("the second switch, which p.restarted refuses, returned %v, want errModeDeclined", second)
	}
	if compositor.ended() != 1 {
		t.Errorf("the compositor was ended %d times, want once", compositor.ended())
	}
}
