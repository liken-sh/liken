package main

// Reconciles /etc/hosts with spec.network.hostEntries of the Machine,
// in the same pass that applies sysctls.
//
// Init writes /etc/hosts at boot, so the entries are in place before
// k3s starts. The operator writes the file again whenever it differs
// from the spec, so an edit to the spec needs no reboot. When another
// process rewrites the file, the inotify watch on it starts a pass that
// writes the entries back (machineevents.go). The pass's own write
// starts one more pass, which finds the file unchanged and writes
// nothing. Init and the operator both render the file with
// machine.HostsFile, so they write the same bytes.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/liken/machine"
)

// hostsPath is the host's /etc/hosts, through the /host/etc hostPath
// mount. The mount is the directory and not the file, because a bind
// mount of the file would keep the old inode after the rename in
// writeFileAtomically (manifests/machine-operator.yaml).
var hostsPath = "/host/etc/hosts"

// applyHostEntries makes the hosts file at path match the desired
// entries, and writes only when the file differs from the render, so a
// pass that changes nothing leaves the mtime alone and sends no inotify
// event. A missing file counts as different, so the pass creates it.
//
// It returns the entries read back from the file after the write, so
// the status reports what the file has.
func applyHostEntries(path, hostname string, desired []machine.HostEntry, out *passOutcome) ([]machine.HostEntry, error) {
	entries, err := reconcileHostsFile(path, hostname, desired, out)
	out.fail("writing the host entries", err)
	return entries, err
}

func reconcileHostsFile(path, hostname string, desired []machine.HostEntry, out *passOutcome) ([]machine.HostEntry, error) {
	render := machine.HostsFile(hostname, desired)

	actual, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("reading the hosts file: %w", err)
	}

	if string(actual) != render {
		if err := writeFileAtomically(path, render); err != nil {
			return nil, fmt.Errorf("writing the hosts file: %w", err)
		}
		out.wrote("writing the host entries")
	}

	written, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the hosts file: %w", err)
	}
	return parseHostEntries(written), nil
}

// writeFileAtomically replaces the contents of path so that a reader
// sees either the old bytes or the new ones. It writes a temporary file
// in the same directory, so the rename is atomic and not a copy.
func writeFileAtomically(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // fails harmlessly after a successful rename
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// parseHostEntries returns the entries of a file that machine.HostsFile
// rendered: every line after its three fixed lines. It does not parse
// an arbitrary hosts file, because applyHostEntries calls it only on a
// file that it has just made match the render.
func parseHostEntries(raw []byte) []machine.HostEntry {
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) <= 3 {
		return nil
	}
	var entries []machine.HostEntry
	for _, line := range lines[3:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		entries = append(entries, machine.HostEntry{Address: fields[0], Names: fields[1:]})
	}
	return entries
}
