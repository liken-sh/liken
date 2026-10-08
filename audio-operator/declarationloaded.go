package main

// Whether PipeWire runs the declaration that is on disk.
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
// (layoutdrift.go), and the PipeWire container's first process then
// restarts PipeWire in place (restarts.go). The check holds no state:
// the file and the socket are the whole record, so an operator that
// restarts in the window between the write and the restart changes
// nothing.
//
// The declare init container writes the drop-in before PipeWire
// starts, so a pod that starts reads as current. Every state the check
// cannot read reads as current too, because a restart would not repair
// it and a check that read it as stale would restart PipeWire forever.

import (
	"fmt"
	"os"
	"path/filepath"
)

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

// runningStale is true while the drop-in is newer than the running
// PipeWire. A state it cannot read is not stale.
func runningStale() bool {
	stale, err := declarationNewer(filepath.Join(pipewireConfigDir, dropInName), socketPath)
	return err == nil && stale
}
