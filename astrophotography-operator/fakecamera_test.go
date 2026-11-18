package main

// A fake camera on the fake INDI server. playCamera makes the server
// keep CCD_EXPOSURE Busy until the test sets its state. The fake camera
// then ends each exposure the way the pinned CCD simulator does: after
// the exposure's length, it sends the message "[INFO] Image saved to
// <path>" and sets CCD_EXPOSURE to Ok. It also puts the file in the
// fake worker's outbox.

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

type cameraPlay int

const (
	// saves writes the frame to the path in the upload settings.
	saves cameraPlay = iota
	// hangs never finishes the exposure, like a driver whose hardware
	// stopped responding.
	hangs
	// savesElsewhere writes the frame under a different name, like a
	// driver after someone changed its upload settings in KStars.
	savesElsewhere
	// savesSilently writes the frame and sends no message.
	savesSilently
	// loses sends no message and writes no file.
	loses
)

// playCamera ends every exposure of the CCD as play describes, until
// the test ends.
func (w *runnerWorld) playCamera(t *testing.T, play cameraPlay) {
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

// keepConnected reconnects the client each time its connection ends,
// like a driver's client after a network interruption.
func keepConnected(ctx context.Context, client *indi.Client) {
	for ctx.Err() == nil {
		_ = client.Run(ctx)
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
		}
	}
}

// exposureState returns a condition that is true when CCD_EXPOSURE's
// state equals state, or, when is is false, when it differs.
func exposureState(state indi.State, is bool) func(*indi.Store) bool {
	return func(s *indi.Store) bool {
		p, ok := s.Property(ccdINDI, "CCD_EXPOSURE")
		return ok && (p.State == state) == is
	}
}

func (w *runnerWorld) finishExposure(ctx context.Context, play cameraPlay) {
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
