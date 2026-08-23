package main

// serve-art runs the bridge's decode side on its own, against a plain mpv IPC
// socket. The pod runs the whole command sidecar, which also reports to the
// bus. A person iterating on the display locally wants the decode half with no
// cluster and no bus. So this mode dials a socket, reads the same presentation
// blocks the pod passes, and runs the same decode and serve code the sidecar
// runs. The local screenshots then show art decoded the way the pod decodes it.

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// serve-art reads two variables on top of the presentation blocks. It reads a
// socket path, because a local mpv serves its socket somewhere other than the
// pod's fixed path. It reads an art directory, because a local run has no
// mounted volume. Both take a default, so the mode runs with neither set.
const (
	mpvSocketVariable = "MEDIA_MPV_SOCKET"
	artDirVariable    = "MEDIA_ART_DIR"
)

// runArtServe dials the socket and runs the decode side with a report sender
// that publishes nothing. The item tracking and the logo decode then run as
// they do in the pod, with no bus.
func runArtServe() {
	socket := os.Getenv(mpvSocketVariable)
	if socket == "" {
		socket = mpvSocketPath
	}
	artDir := os.Getenv(artDirVariable)
	if artDir == "" {
		artDir = artMountPath
	}

	cmd := &commander{
		presentations: parsePresentations(os.Getenv(presentationsVariable)),
		artDir:        artDir,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	conn, err := dialMPV(ctx, socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve-art: %v\n", err)
		return
	}
	defer conn.Close()
	defer context.AfterFunc(ctx, func() { conn.Close() })()

	cmd.drive(ctx, conn, func(playReport) error { return nil })
}
