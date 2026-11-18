// start-btmon runs btmon in the pod's btmon container while the
// Adapter's spec.btmon is true, and runs nothing while it is false.
//
// btmon writes a trace of the radio's HCI traffic and management
// channel to the container log, which a person reads to diagnose a
// bonded controller that stalls. btmon decodes every packet in full,
// so the trace prints the link keys, the long term keys, and the
// radio's identity key in plain text to anyone who can read the pod's
// logs. So the trace is off until a person turns it on for one radio.
//
// The setting is the file btmon in the pod's settings volume, which
// contains true or false. bondfetch writes it before this container
// starts, and the operator writes it again when spec.btmon changes.
// The container is in every pod, because a DaemonSet uses one pod
// template for every radio. A change starts or stops the trace without
// a pod restart, so no controller disconnects.
//
// This program is written in Go because the image has no shell.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

// settingsRoot is the pod's settings volume, mounted read-only in this
// container.
const settingsRoot = "/var/run/bluetooth.liken.sh/settings"

func main() {
	// When the kubelet sends SIGTERM, the supervisor stops btmon before
	// the program exits.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "start-btmon: %v\n", err)
		os.Exit(1)
	}
}

// run opens a watch on the settings directory before it first reads
// the file, so a change between the open and the read is not missed.
// When the watch fails, run returns the error, and the kubelet restarts
// the container, which opens a new watch and reads the file again.
func run(ctx context.Context) error {
	w, err := openWatch(settingsRoot)
	if err != nil {
		return err
	}
	defer w.close()
	s := &supervisor{
		setting: filepath.Join(settingsRoot, "btmon"),
		start:   startBtmon,
		delay:   restartDelay,
	}
	return s.run(ctx, w.changes, w.failed)
}
