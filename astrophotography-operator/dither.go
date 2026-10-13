package main

// Dithering (plan 01, step 7.9). A sensor's fixed pattern, such as a
// hot pixel, falls on the same place of the sky in every frame when
// the guiding holds the star still, and stacking keeps it. After every
// dither.every frames, PHD2 moves its lock position by up to
// dither.pixels guide camera pixels, so the pattern falls elsewhere in
// the next frame, and the runner waits for SettleDone before that
// frame starts. A dither changes no condition, so it posts no Event.

import (
	"context"
	"fmt"
	"time"
)

// ditherTimeout bounds a dither from the request to SettleDone. PHD2
// ends the settle itself after guideSettle.Timeout, and the rest covers
// the guide frames around it.
const ditherTimeout = 2 * time.Minute

// afterFrame dithers after every dither.every frames. A PHD2 that does
// not guide at that moment, such as one that lost its star during the
// exposure, gets no dither: the next frame guides again first, on the
// star where it is, which moves the lock position too.
func (g *phd2Guiding) afterFrame(ctx context.Context, w *work, frames int) error {
	l, d := g.link(w.r), w.target.Spec.Dither
	if l == nil || d == nil || d.Every < 1 || frames%int(d.Every) != 0 {
		return nil
	}
	if s := l.client.State(); !s.Open || s.AppState != "Guiding" {
		w.r.logf("target %s: PHD2 of Guider %s does not guide, so no dither after frame %d", targetName(w.target), l.name, frames)
		return nil
	}
	started := time.Now()
	if err := l.dither(ctx, d.Pixels, d.RAOnly); err != nil {
		l.setUnsettled(true)
		return fmt.Errorf("PHD2 of Guider %s: the dither after frame %d: %w", l.name, frames, err)
	}
	t := w.target
	w.r.o.metrics.guiding.dithers.WithLabelValues(t.Metadata.Namespace, t.Metadata.Name).Inc()
	w.r.o.metrics.guiding.settle.WithLabelValues("dither").Observe(time.Since(started).Seconds())
	return nil
}

// dither moves the lock position and waits for SettleDone.
func (l *guiderLink) dither(ctx context.Context, pixels float64, raOnly bool) error {
	if err := l.client.Dither(ctx, pixels, raOnly, guideSettle); err != nil {
		return err
	}
	settling, cancel := context.WithTimeout(ctx, ditherTimeout)
	defer cancel()
	return l.client.WaitSettled(settling)
}
