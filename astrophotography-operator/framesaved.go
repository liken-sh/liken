package main

// Checks that the INDI driver saved each frame at the outbox path that
// the runner wrote to UPLOAD_SETTINGS. When a person changes the upload
// settings in KStars before the exposure, the driver saves the frame
// outside the target's directory, where promotion never looks. The
// runner does not count such a frame.

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/outbox"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// savedPrefix starts the driver's message that gives the path of a
// saved file (INDI::CCD::uploadFile in libs/indibase/indiccd.cpp).
const savedPrefix = "Image saved to "

// savedWait is how long checkSaved waits for the message after
// CCD_EXPOSURE turns Ok. The driver sends the message first, so it is
// normally already queued. The limit matters only for a driver that
// sends none, and then checkSaved lists the outbox instead.
const savedWait = 5 * time.Second

// savedFrames returns a channel of the paths that a device reports it
// saved, from now until ctx ends. A path is dropped when 16 are
// already waiting.
func savedFrames(ctx context.Context, client *indi.Client, device string) <-chan string {
	events := client.Subscribe(ctx)
	saved := make(chan string, 16)
	go func() {
		for e := range events {
			if e.Kind != indi.Message || e.Device != device {
				continue
			}
			if _, file, ok := strings.Cut(e.Message, savedPrefix); ok {
				select {
				case saved <- strings.TrimSpace(file):
				default:
				}
			}
		}
	}()
	return saved
}

// checkSaved returns nil when the driver reports saving the frame at its
// outbox path within savedWait, and an error when the driver reports
// only other paths. When the driver reports no path at all, it asks the
// worker for the outbox's frame list and looks for the frame there.
func (w *work) checkSaved(ctx context.Context, saved <-chan string, file outbox.Frame) error {
	want := path.Join(outbox.Root, file.Path())
	var elsewhere []string
	timer := time.NewTimer(savedWait)
	defer timer.Stop()
	for {
		select {
		case got := <-saved:
			if got == want {
				return nil
			}
			elsewhere = append(elsewhere, got)
			continue
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
		break
	}
	if len(elsewhere) > 0 {
		return fmt.Errorf("the driver saved the frame as %s and not %s, so a client changed the upload settings", strings.Join(elsewhere, ", "), want)
	}
	box, err := w.r.o.outboxOf(ctx, w.r.namespace, w.r.name)
	if err != nil {
		return err
	}
	if !slices.Contains(box.Frames, file.Path()) {
		return fmt.Errorf("the driver reported no saved frame, and the outbox holds no %s", file.Path())
	}
	return nil
}
