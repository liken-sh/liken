package main

// serve-blocks runs the command sidecar's code that sends each item's
// presentation block, the titles and art that the
// display draws. It reads a
// local mpv's IPC socket, so you can work on the display without a
// cluster or a bus.

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// A local mpv can put its socket anywhere, so this variable overrides
// the pod's fixed path.
const mpvSocketVariable = "MEDIA_MPV_SOCKET"

// runBlocksServe runs the sidecar's loop with a report sender that
// discards each report, because there is no bus.
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
