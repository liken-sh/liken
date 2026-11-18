package main

// PipeWire reads the drop-in when it loads its configuration, and then
// removes any socket file at its path and binds a new one (add_socket
// in src/modules/module-protocol-native.c). So the socket's
// modification time is when the running PipeWire started, and a
// drop-in newer than the socket has not been loaded.
//
// The operator rewrites the drop-in for a layout change (layoutdrift.go),
// and the first process of the PipeWire container then restarts
// PipeWire (restarts.go). The check compares two files and keeps no
// state, so an operator restart between the write and the PipeWire
// restart does not lose the pending restart.
//
// The declare init container writes the drop-in before PipeWire
// starts, so a new pod's drop-in counts as loaded. A file that cannot
// be read also counts as loaded, because a PipeWire restart would not
// repair it and would repeat in a loop.

import (
	"fmt"
	"os"
	"path/filepath"
)

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

// runningStale returns false when it cannot read either file.
func runningStale() bool {
	stale, err := declarationNewer(filepath.Join(pipewireConfigDir, dropInName), socketPath)
	return err == nil && stale
}
