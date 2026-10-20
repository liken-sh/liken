package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// fwupdErrorNothingFound is the fwupd error code for "No detected
// devices" and "No updatable devices". Both mean an empty answer, so
// they are not failures of the scan.
const fwupdErrorNothingFound = 9

// fwupdtool runs one fwupdtool command and returns what it printed on
// stdout and stderr. The error is non-nil when the command exits
// non-zero, and the output is still returned: get-updates exits
// non-zero when there is nothing to update, and the scan reads its
// stdout to tell that from a real failure.
type fwupdtool func(ctx context.Context, args ...string) (stdout, stderr []byte, err error)

// execFwupdtool runs the real program. Its stderr goes to the pod's log
// as well, and its stdout never does, because the last line of stdout
// is the scan's result.
//
// FWUPD_UEFI_ESP_PATH points the uefi_capsule plugin at an empty
// directory. Without an ESP it reports the device as not updatable, and
// with the machine's real ESP the plugin would read it. The scan never
// stages a capsule, so an empty directory is all it needs.
func execFwupdtool(espDir string) fwupdtool {
	return func(ctx context.Context, args ...string) ([]byte, []byte, error) {
		if err := os.MkdirAll(espDir, 0o755); err != nil {
			return nil, nil, err
		}
		cmd := exec.CommandContext(ctx, "fwupdtool", args...)
		cmd.Env = append(os.Environ(), "FWUPD_UEFI_ESP_PATH="+espDir)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = io.MultiWriter(&stderr, os.Stderr)
		err := cmd.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}
}

// lastLine is the last non-empty line of fwupdtool's stderr, which is
// where it states why a command failed.
func lastLine(stderr []byte) string {
	lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

type fwupdError struct {
	Code    int    `json:"Code"`
	Message string `json:"Message"`
}

type fwupdDevice struct {
	Name     string         `json:"Name"`
	DeviceID string         `json:"DeviceId"`
	Plugin   string         `json:"Plugin"`
	GUIDs    []string       `json:"Guid"`
	Vendor   string         `json:"Vendor"`
	Version  string         `json:"Version"`
	Flags    []string       `json:"Flags"`
	Releases []fwupdRelease `json:"Releases"`
}

// parseDevices reads the JSON of get-devices or get-updates. A fwupd
// "nothing found" error is an empty list. Any other error object, and
// output that is not JSON, is a failure that carries stderr's last line.
func parseDevices(stdout, stderr []byte, runErr error) ([]fwupdDevice, error) {
	var out struct {
		Devices []fwupdDevice `json:"Devices"`
		Error   *fwupdError   `json:"Error"`
	}
	if err := json.Unmarshal(stdout, &out); err != nil {
		reason := lastLine(stderr)
		if reason == "" && runErr != nil {
			reason = runErr.Error()
		}
		return nil, errors.New(reason)
	}
	if out.Error != nil && out.Error.Code != fwupdErrorNothingFound {
		return nil, errors.New(out.Error.Message)
	}
	return out.Devices, nil
}

// query runs get-devices or get-updates and returns the devices it names.
func query(ctx context.Context, run fwupdtool, command string) ([]fwupdDevice, error) {
	stdout, stderr, err := run(ctx, command, "--json")
	devices, parseErr := parseDevices(stdout, stderr, err)
	if parseErr != nil {
		return nil, fmt.Errorf("fwupdtool %s: %w", command, parseErr)
	}
	return devices, nil
}

// discover lists the devices that fwupd can update and attaches to each
// the releases that get-updates offered for it. A device whose Flags
// lack updatable is left out: no release could apply to it.
func discover(ctx context.Context, run fwupdtool) ([]Device, error) {
	found, err := query(ctx, run, "get-devices")
	if err != nil {
		return nil, err
	}
	offered, err := query(ctx, run, "get-updates")
	if err != nil {
		return nil, err
	}
	releases := map[string][]Release{}
	for _, d := range offered {
		for _, r := range d.Releases {
			releases[d.DeviceID] = append(releases[d.DeviceID], toRelease(r))
		}
	}
	devices := []Device{}
	for _, d := range found {
		if !slices.Contains(d.Flags, "updatable") {
			continue
		}
		guids := slices.Clone(d.GUIDs)
		slices.Sort(guids)
		devices = append(devices, Device{
			Name: d.Name, DeviceID: d.DeviceID, Plugin: d.Plugin, GUIDs: guids,
			Version: d.Version, Vendor: d.Vendor, Releases: releases[d.DeviceID],
		})
	}
	return devices, nil
}
