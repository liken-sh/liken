package main

// The check that the driver saved the frame where the runner named it.
// The driver answers every exposure with the message "[INFO] Image
// saved to <path>", and sends it before CCD_EXPOSURE turns Ok. A frame
// saved anywhere else is not in the target's directory of the outbox,
// so promotion would never find it, and the runner does not count it:
// that happens when a person in KStars changes the upload settings
// between the runner's write and the exposure.

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

// savedPrefix is the start of the driver's message that names a saved
// file (INDI::CCD::uploadFile in libs/indibase/indiccd.cpp).
const savedPrefix = "Image saved to "

// savedWait bounds the wait for the message after CCD_EXPOSURE turned
// Ok. The message comes first on the wire, so it is already queued and
// the wait ends at once, and the limit matters only for a driver that
// sends no message, which the outbox's own list then answers.
const savedWait = 5 * time.Second

// savedFrames answers the paths of the files a device reports it saved,
// from now until ctx ends.
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

// checkSaved answers nil when the driver saved the frame in its file.
// When no message names a file, the worker's list of the outbox
// answers instead.
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
		return fmt.Errorf("the driver saved the frame as %s, not %s; a client changed the upload settings", strings.Join(elsewhere, ", "), want)
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
