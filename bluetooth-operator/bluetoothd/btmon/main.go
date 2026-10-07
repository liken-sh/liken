// start-btmon runs btmon in the pod's btmon container while the
// Adapter's spec.btmon is true, and runs nothing while it is false.
//
// btmon traces the radio's HCI link and the management channel into
// the container log. The trace is the evidence of a stall of a bonded
// controller, which can last for hours, and whose fix is a roll of the
// pod. btmon decodes each packet in full, so the trace also prints key
// material in plain text: the link keys, the long term keys, and the
// radio's identity key. Anybody who can read the pod's logs can read
// those keys. So the trace is off until a person turns it on for one
// radio.
//
// The setting reaches this program as the file btmon in the pod's
// settings volume, which holds true or false. bondfetch writes it
// before this container starts, and the operator writes it again when
// spec.btmon changes. The container stays in the pod either way,
// because a DaemonSet's pod template is the same for every radio. A
// change starts or stops the trace in seconds, with no restart of the
// pod, so no controller disconnects.
//
// This program is Go rather than a shell script because the image has
// no shell.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

// settingsRoot is the pod's settings volume, which this container
// mounts read-only.
const settingsRoot = "/var/run/bluetooth.liken.sh/settings"

func main() {
	// The kubelet's TERM ends the context, and the supervisor stops
	// btmon before this program exits.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "start-btmon: %v\n", err)
		os.Exit(1)
	}
}

// run opens the watch on the settings directory before the first read
// of the file, so a write between the two is not lost. A watch that
// fails ends the program, and the kubelet starts the container again,
// which opens a new watch and reads the file again.
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
