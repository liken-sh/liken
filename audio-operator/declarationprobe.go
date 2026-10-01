package main

// The liveness probe of the PipeWire container: whether PipeWire runs
// the declaration that is on disk.
//
// PipeWire reads the drop-in once, while it loads its configuration,
// and creates its socket after that. A PipeWire that started after the
// last write of the drop-in holds a socket newer than the file, and
// one that started before it holds an older socket. PipeWire removes
// a socket file it finds at its path and binds a new one at every
// start (add_socket in src/modules/module-protocol-native.c), so the
// socket's modification time is the time the running PipeWire
// started.
//
// The operator writes a new drop-in for a layout change
// (layoutdrift.go), and this probe then fails, so the kubelet restarts
// the PipeWire container alone. The probe holds no state: the file and
// the socket are the whole record, so an operator that restarts in
// the window between the write and the restart changes nothing.
//
// The declare init container writes the drop-in before PipeWire
// starts, so a pod that starts passes. Every state the probe cannot
// read passes too, because a restart would not repair it and a probe
// that fails restarts PipeWire forever.

import (
	"fmt"
	"os"
	"path/filepath"
)

// declarationMode is the argument that selects this check. The
// liveness probe on the PipeWire container names it, and the operator
// container reads the same fact with declarationNewer.
const declarationMode = "declaration-loaded"

// declarationProbe is the probe. It exits 1 when the drop-in is newer
// than the running PipeWire, and 0 in every other state, with one line
// that names the state.
func declarationProbe() {
	stale, err := declarationNewer(filepath.Join(pipewireConfigDir, dropInName), socketPath)
	switch {
	case err != nil:
		fmt.Printf("%v; a restart would not change that\n", err)
	case stale:
		fatal("the drop-in %s is newer than the PipeWire that runs; "+
			"only a restart of this container loads it", dropInName)
	default:
		fmt.Println("PipeWire runs the declaration on disk")
	}
}

// declarationNewer reports whether the drop-in was written after the
// socket was created.
func declarationNewer(dropIn, socket string) (bool, error) {
	declared, err := os.Stat(dropIn)
	if err != nil {
		return false, fmt.Errorf("reading the drop-in: %w", err)
	}
	created, err := os.Stat(socket)
	if err != nil {
		return false, fmt.Errorf("reading PipeWire's socket: %w", err)
	}
	return declared.ModTime().After(created.ModTime()), nil
}

// runningStale is the operator container's form of the probe: true
// while the drop-in is newer than the running PipeWire. A state it
// cannot read is not stale, for the probe's reason.
func runningStale() bool {
	stale, err := declarationNewer(filepath.Join(pipewireConfigDir, dropInName), socketPath)
	return err == nil && stale
}
