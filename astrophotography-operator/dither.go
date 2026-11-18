package main

// When guiding holds the star still, a fixed pattern of the sensor,
// such as a hot pixel, falls on the same part of the sky in every
// frame, and stacking keeps it. So after every dither.every frames,
// PHD2 moves its lock position by up to dither.pixels guide camera
// pixels, and the runner waits for SettleDone before the next frame. A
// dither changes no condition, so it posts no Event.

import (
	"context"
	"fmt"
	"time"
)

// ditherTimeout limits the time from the dither request to SettleDone.
// PHD2 gives up on the settle itself after guideSettle.Timeout, and the
// remaining time covers the guide frames around the settle.
const ditherTimeout = 2 * time.Minute

// afterFrame dithers after every dither.every frames. It skips the
// dither when PHD2 is not guiding, for example after it lost its star
// during the exposure. The next frame starts guiding again on the star
// where it is now, which moves the lock position as well.
func (g *phd2Guiding) afterFrame(ctx context.Context, w *work, frames int) error {
	l, d := g.link(w.r), w.target.Spec.Dither
	if l == nil || d == nil || d.Every < 1 || frames%int(d.Every) != 0 {
		return nil
	}
	if s := l.client.State(); !s.Open || s.AppState != "Guiding" {
		w.r.logf("target %s: skipped the dither after frame %d because PHD2 of Guider %s is not guiding", targetName(w.target), frames, l.name)
		return nil
	}
	started := time.Now()
	if err := l.dither(ctx, d.Pixels, d.RAOnly); err != nil {
		l.setUnsettled(true)
		return fmt.Errorf("dithering after frame %d through PHD2 of Guider %s: %w", frames, l.name, err)
	}
	t := w.target
	w.r.o.metrics.guiding.dithers.WithLabelValues(t.Metadata.Namespace, t.Metadata.Name).Inc()
	w.r.o.metrics.guiding.settle.WithLabelValues("dither").Observe(time.Since(started).Seconds())
	return nil
}

func (l *guiderLink) dither(ctx context.Context, pixels float64, raOnly bool) error {
	if err := l.client.Dither(ctx, pixels, raOnly, guideSettle); err != nil {
		return err
	}
	settling, cancel := context.WithTimeout(ctx, ditherTimeout)
	defer cancel()
	return l.client.WaitSettled(settling)
}
