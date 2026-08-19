package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configEntry is one resolved config block of this driver's own, from
// the given source, for the given requests.
func configEntry(source, requests, parameters string) string {
	return fmt.Sprintf(`{"source": %q, "requests": [%s], "opaque": {"driver": %q, "parameters": %s}}`,
		source, requests, DriverName, parameters)
}

// claimMode is what the claim's author wrote, applying to every
// request in the claim.
func claimMode(parameters string) string {
	return configEntry(configFromClaim, "", parameters)
}

// classMode is the same block written in the DeviceClass, which is
// cluster policy rather than one workload's choice.
func classMode(parameters string) string {
	return configEntry(configFromClass, "", parameters)
}

// resolvedConfig reads the config array out of the allocation the
// scheduler wrote on a claim's status. A claim's own
// spec.devices.config is not what the driver reads: the scheduler
// copies each block here, beside the DeviceClass's own, and marks
// where it came from.
func resolvedConfig(t *testing.T, entries string) []AllocatedConfig {
	t.Helper()
	claim := &ResourceClaim{}
	document := fmt.Sprintf(`{"status":{"allocation":{"devices":{"results":[],"config":[%s]}}}}`, entries)
	if err := json.Unmarshal([]byte(document), claim); err != nil {
		t.Fatal(err)
	}
	if claim.Status.Allocation == nil {
		t.Fatal("the claim document holds no allocation")
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
			// The class is cluster policy, and it answers for a claim
			// that states nothing of its own.
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
			// The allocator lists the class's config first, and the
			// precedence reads the source rather than the order, so the
			// answer does not move when the order does.
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
				t.Fatal(err)
			}
			if got := selection.forRequest(c.request); got != c.want {
				t.Errorf("mode = %q, want %q", got, c.want)
			}
		})
	}
}

// A typo in the parameters is a mode nobody asked for, driven with
// nothing said anywhere, so the parse refuses what it does not know.
// The source does not soften it: a typo in cluster policy is as wrong
// as one in a claim.
func TestClaimModesRefusesParametersItCannotRead(t *testing.T) {
	cases := []struct {
		name   string
		config string
		says   string
	}{
		{
			name:   "a key this driver does not read",
			config: claimMode(`{"resolution": "1280x720"}`),
			says:   `"resolution"`,
		},
		{
			name:   "a key the class does not read either",
			config: classMode(`{"resolution": "1280x720"}`),
			says:   `"resolution"`,
		},
		{
			name:   "a mode that is not a string",
			config: claimMode(`{"mode": 720}`),
			says:   "not a string",
		},
		{
			name:   "parameters that are not an object",
			config: claimMode(`["1280x720"]`),
			says:   "parameters",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := claimModes(resolvedConfig(t, c.config))
			if err == nil {
				t.Fatal("the parse accepted parameters it cannot read")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("error = %q, want it to say %q", err, c.says)
			}
		})
	}
}

func TestReadModeRecordOfAFileThatIsNotThere(t *testing.T) {
	// A pod whose declare container never ran leaves no record, and a
	// claim that states a mode must still be able to write one.
	record, err := readModeRecord(filepath.Join(t.TempDir(), "modes.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(record) != 0 {
		t.Fatalf("record = %v", record)
	}
}

func TestModeRecordSurvivesAWriteAndARead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weston", "modes.json")

	if err := writeModeRecord(path, map[string]string{"HDMI-A-2": "1280x720"}); err != nil {
		t.Fatal(err)
	}
	record, err := readModeRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if record["HDMI-A-2"] != "1280x720" {
		t.Fatalf("record = %v", record)
	}
}

func TestReadModeRecordRefusesAFileItCannotParse(t *testing.T) {
	// The operator is the only writer of this file, so unreadable
	// content means something else wrote it, and a prepare that
	// overwrote it would hide that.
	path := filepath.Join(t.TempDir(), "modes.json")
	if err := os.WriteFile(path, []byte("1280x720"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := readModeRecord(path); err == nil {
		t.Fatal("the read accepted a file it cannot parse")
	}
}
