package main

// serve-blocks forwards the presentation blocks to the display on its own,
// against a plain mpv IPC socket. The pod runs the whole command sidecar,
// which also reports to the bus. A person iterating on the display locally
// wants the blocks with no cluster and no bus. So this mode dials a socket,
// reads the same presentation blocks the pod passes, and runs the same
// forwarding code the sidecar runs. The local screens then show the item's own
// words and its own art.

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// serve-blocks reads a socket path on top of the presentation blocks, because
// a local mpv serves its socket somewhere other than the pod's fixed path. It
// takes a default, so the mode runs with nothing set.
const mpvSocketVariable = "MEDIA_MPV_SOCKET"

// runBlocksServe dials the socket and forwards the blocks with a report sender
// that publishes nothing. The item tracking then runs as it does in the pod,
// with no bus.
func runBlocksServe() {
	socket := os.Getenv(mpvSocketVariable)
	if socket == "" {
		socket = mpvSocketPath
	}

	cmd := &commander{
		presentations: parsePresentations(os.Getenv(presentationsVariable)),
		next:          parseNext(os.Getenv(nextVariable)),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	conn, err := dialMPV(ctx, socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve-blocks: %v\n", err)
		return
	}
	defer conn.Close()
	defer context.AfterFunc(ctx, func() { conn.Close() })()

	cmd.drive(ctx, conn, func(playReport) error { return nil })
}
