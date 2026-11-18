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

// fwupdErrorNothingToDo is libfwupd's FWUPD_ERROR_NOTHING_TO_DO, which
// fwupdtool returns with messages such as "No detected devices" and "No
// updatable devices". The scan reads it as an empty list, not a failure.
const fwupdErrorNothingToDo = 9

// fwupdtool runs one fwupdtool command and returns its stdout and
// stderr, together with the error of a non-zero exit. get-updates exits
// non-zero when there is nothing to update, so the caller reads stdout
// to tell that from a real failure.
type fwupdtool func(ctx context.Context, args ...string) (stdout, stderr []byte, err error)

// execFwupdtool runs the real fwupdtool and copies its stderr to the
// pod's log. It does not copy stdout, because the operator reads the
// pod's result from a JSON line in the log, and fwupdtool's own JSON
// must not mix with it.
//
// fwupd finds the EFI system partition through udisks on the system
// D-Bus, which a pod does not have. Without an ESP, fwupd marks every
// UEFI device updatable-hidden and offers no update for it.
// FWUPD_UEFI_ESP_PATH skips that search. A scan passes an empty
// directory, because it never stages a capsule.
func execFwupdtool(espDir string, env ...string) fwupdtool {
	return func(ctx context.Context, args ...string) ([]byte, []byte, error) {
		if err := os.MkdirAll(espDir, 0o755); err != nil {
			return nil, nil, err
		}
		cmd := exec.CommandContext(ctx, "fwupdtool", args...)
		cmd.Env = append(append(os.Environ(), "FWUPD_UEFI_ESP_PATH="+espDir), env...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = io.MultiWriter(&stderr, os.Stderr)
		err := cmd.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}
}

// lastLine returns the last line of fwupdtool's stderr, where fwupdtool
// prints why a command failed.
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

// parseDevices parses the JSON that get-devices or get-updates prints.
// fwupdErrorNothingToDo gives an empty list. Another fwupd error gives
// its message. Output that is not JSON gives the last line of stderr,
// or the exit error when stderr is empty.
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
	if out.Error != nil && out.Error.Code != fwupdErrorNothingToDo {
		return nil, errors.New(out.Error.Message)
	}
	return out.Devices, nil
}

func query(ctx context.Context, run fwupdtool, command string) ([]fwupdDevice, error) {
	stdout, stderr, err := run(ctx, command, "--json")
	devices, parseErr := parseDevices(stdout, stderr, err)
	if parseErr != nil {
		return nil, fmt.Errorf("fwupdtool %s: %w", command, parseErr)
	}
	return devices, nil
}

// discover returns the devices whose Flags include updatable, each
// with the releases that get-updates offered for it.
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
