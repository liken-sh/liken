package main

// A fake camera behind the fake INDI server. The CCD simulator's driver
// of inditest answers CCD_EXPOSURE with Busy and keeps it Busy, and the
// fake camera plays the rest of an exposure the way the pinned
// simulator does: after the exposure's length it sends the message
// "[INFO] Image saved to <path>" and then sets CCD_EXPOSURE Ok. It also
// puts the file in the fake worker's outbox.

import (
	"context"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/outbox"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// cameraPlay is how the fake camera answers each exposure.
type cameraPlay int

const (
	// saves writes the frame where the upload settings name it.
	saves cameraPlay = iota
	// hangs never finishes the exposure, as a driver whose hardware
	// stopped answering does.
	hangs
	// savesElsewhere writes the frame under another name, as a driver
	// does after a person in KStars changed its upload settings.
	savesElsewhere
	// savesSilently writes the frame and sends no message.
	savesSilently
	// loses sends no message and writes no file.
	loses
)

// playCamera answers the CCD's exposures until the test ends.
func (w *night) playCamera(t *testing.T, play cameraPlay) {
	w.indi.Move(ccdINDI, "CCD_EXPOSURE")
	ctx := t.Context()
	client := indi.NewClient("indiserver", indi.WithDialer(w.indi))
	go keepConnected(ctx, client)
	go func() {
		for ctx.Err() == nil {
			if err := client.WaitFor(ctx, exposureState(indi.Busy, true)); err != nil {
				return
			}
			w.finishExposure(ctx, play)
			if err := client.WaitFor(ctx, exposureState(indi.Busy, false)); err != nil {
				return
			}
		}
	}()
}

// keepConnected opens the client's connection again each time it ends,
// as a driver's client does after a network cut.
func keepConnected(ctx context.Context, client *indi.Client) {
	for ctx.Err() == nil {
		_ = client.Run(ctx)
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
		}
	}
}

// exposureState answers a condition that holds when CCD_EXPOSURE's
// state is, or is not, the state given.
func exposureState(state indi.State, is bool) func(*indi.Store) bool {
	return func(s *indi.Store) bool {
		p, ok := s.Property(ccdINDI, "CCD_EXPOSURE")
		return ok && (p.State == state) == is
	}
}

// finishExposure plays the end of one exposure.
func (w *night) finishExposure(ctx context.Context, play cameraPlay) {
	exposure := w.indi.Property(ccdINDI, "CCD_EXPOSURE")
	seconds, _ := exposure.Value("CCD_EXPOSURE_VALUE")
	length, _ := strconv.ParseFloat(seconds, 64)
	upload := w.indi.Property(ccdINDI, "UPLOAD_SETTINGS")
	dir, _ := upload.Value("UPLOAD_DIR")
	prefix, _ := upload.Value("UPLOAD_PREFIX")
	select {
	case <-time.After(time.Duration(length * float64(time.Second))):
	case <-ctx.Done():
		return
	}
	if play == hangs {
		<-ctx.Done()
		return
	}
	if play == savesElsewhere {
		prefix = "IMAGE_001"
	}
	file := path.Join(dir, prefix) + outbox.Extension
	if play != savesSilently && play != loses {
		w.indi.Message(ccdINDI, "[INFO] Image saved to "+file)
	}
	if play != loses {
		w.worker.save(strings.TrimPrefix(file, outbox.Root+"/"))
	}
	w.indi.SetState(ccdINDI, "CCD_EXPOSURE", "Ok")
}
