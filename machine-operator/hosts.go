package main

// Host entries, reconciled live from the Machine spec's
// spec.network.hostEntries, alongside sysctls in the same pass.
//
// Init writes /etc/hosts once, at boot, so the entries hold before
// k3s starts and every boot proves the cold-start order on its own.
// This file is the second writer: the operator reconciles the same
// file on every pass, so a later edit lands within one reconcile
// pass, with no reboot. The two writers share one renderer,
// machine.HostsFile, so they can only ever produce one shape of the
// file.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/machine"
)

// hostsPath is the file this operator reconciles: the host's
// /etc/hosts, reached through the /host/etc hostPath mount
// (manifests/machine-operator.yaml explains why the mount is the
// directory rather than the file). It is a package variable, the
// same pattern init's sysctlDir uses, so a test can point it at a
// tempdir's file instead of a real path under /host.
var hostsPath = "/host/etc/hosts"

// applyHostEntries reconciles /etc/hosts against the desired entries.
// This is the write-on-divergence rule this milestone applies to
// every live reconciliation: render the desired file with the shared
// renderer, read the actual file, and write only when the bytes
// differ. A converged machine is the common case, and skipping the
// write leaves no false modification signal, an mtime bump or an
// inotify event, for whatever else watches the file. The same rule is
// what gives the file its healing property: a file that an outside
// edit changed differs from the render, so the next pass rewrites it.
//
// A missing file reads as maximally divergent rather than as an
// error, because the first pass on a machine, or a pass right after
// an unrelated file loss, must still converge the file instead of
// giving up. Any other read failure comes back to the caller, the
// same as a write failure does.
//
// The returned entries come from a fresh read of the file after this
// pass, not from the desired list, so the caller's status report is
// observed, not assumed.
func applyHostEntries(path, hostname string, desired []machine.HostEntry) ([]machine.HostEntry, error) {
	render := machine.HostsFile(hostname, desired)

	actual, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	if string(actual) != render {
		if err := writeFileAtomically(path, render); err != nil {
			return nil, fmt.Errorf("writing %s: %w", path, err)
		}
	}

	holds, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return parseHostEntries(holds), nil
}

// writeFileAtomically replaces path's contents without ever exposing
// a reader to a partial write. It creates a temporary file beside the
// target, on the same filesystem, so the rename that follows is one
// atomic directory-entry swap rather than a copy. A reader that opens
// path mid-write always sees either the old bytes or the new ones,
// never a mix. The rename also shapes the operator's mount: a bind
// mount of the file itself would hold the inode the rename discards,
// so the hostPath mount covers the /etc directory instead
// (manifests/machine-operator.yaml).
func writeFileAtomically(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename below succeeds
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

// parseHostEntries recovers the host entries from a rendered hosts
// file: the lines below the three fixed lines that machine.HostsFile
// always writes first. It is the inverse of HostsFile, close enough
// for status, because applyHostEntries only ever calls it against a
// file this pass already brought into the shared renderer's shape.
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
