package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDevices(t *testing.T) {
	tests := []struct {
		name    string
		stdout  string
		stderr  string
		want    int
		wantErr string
	}{
		{"devices", string(fixture(t, "devices.json")), "", 3, ""},
		{"nothing found", string(fixture(t, "devices_none.json")), "", 0, ""},
		{"an empty list", string(fixture(t, "updates_none.json")), "", 0, ""},
		{"another error", string(fixture(t, "error.json")), "", 0, "Failed to load plugin"},
		{"no JSON", "", "one\ntwo\n", 0, "two"},
		{"no JSON and no stderr", "", "", 0, "exit status 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDevices([]byte(tt.stdout), []byte(tt.stderr), errExit2)

			if len(got) != tt.want || (err == nil) != (tt.wantErr == "") || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("got %d devices, %v; want %d, error %q", len(got), err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestDiscoverKeepsOnlyUpdatableDevices(t *testing.T) {
	r := newRig(t)

	devices, err := discover(context.Background(), r.tool.run)

	if err != nil || len(devices) != 2 {
		t.Fatalf("got %d devices, %v; want 2", len(devices), err)
	}
	if devices[0].Name != "System Firmware" || devices[1].Name != "UEFI dbx" {
		t.Errorf("devices are %q and %q", devices[0].Name, devices[1].Name)
	}
}

func TestDiscoverSortsGUIDs(t *testing.T) {
	r := newRig(t)

	devices, _ := discover(context.Background(), r.tool.run)

	want := "1606d7b5-6eea-5abc-8a31-90c0e5cc274a d07ff664-b0e1-5f4e-a723-d7fbcbfcb94f f8ba2887-9411-5c36-9cee-88995bb39731"
	if got := strings.Join(devices[1].GUIDs, " "); got != want {
		t.Errorf("got %s", got)
	}
}

func TestExecFwupdtoolFailsWhenTheESPDirectoryCannotBeMade(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, nil, 0o644)

	_, _, err := execFwupdtool(filepath.Join(blocker, "esp"))(context.Background(), "--version")

	if err == nil {
		t.Error("expected an error")
	}
}

func TestExecFwupdtoolRunsTheProgramWithTheESPDirectory(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$FWUPD_UEFI_ESP_PATH $*\"\necho oops >&2\nexit 2\n"
	os.WriteFile(filepath.Join(bin, "fwupdtool"), []byte(script), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	esp := filepath.Join(t.TempDir(), "esp")

	stdout, stderr, err := execFwupdtool(esp)(context.Background(), "get-devices", "--json")

	if string(stdout) != esp+" get-devices --json\n" || string(stderr) != "oops\n" || err == nil {
		t.Errorf("got %q, %q, %v", stdout, stderr, err)
	}
}
