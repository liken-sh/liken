package main

// Guiding with PHD2. PHD2 must not guide while the mount slews, because
// it would send guide corrections against the slew, so the runner sends
// stop_capture before each slew. Before each frame, the runner
// checks that PHD2 is guiding on a settled star. When it is not, for
// example after a slew or when the star was lost during the last
// exposure, the runner sends loop, find_star, and guide, and waits for
// SettleDone, because a frame taken while the star still moves is
// blurred.
//
// When a guide fails, for example because cloud hides every star, the
// runner keeps the Target, sets Imaging to False with the reason
// GuidingFailed, and guides again a minute later. It takes no unguided
// frame in the meantime, because a long unguided frame shows trailed
// stars.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/phd2"
)

// guideSettle sets when PHD2 reports SettleDone for a guide or a
// dither: the star must stay within 1.5 pixels of the lock position for
// 8 seconds.
// PHD2 ends a settle that takes longer than Timeout with an error.
var guideSettle = phd2.Settle{Pixels: 1.5, Time: 8 * time.Second, Timeout: time.Minute}

const (
	// stopTimeout limits stop_capture and the wait for the stop. PHD2
	// responds to the request at once and reports the stop after its
	// current guide exposure ends.
	stopTimeout = 30 * time.Second
	// loopTimeout limits the wait for PHD2's first guide frame after
	// loop.
	loopTimeout = time.Minute
	// guideTimeout limits the wait for SettleDone after guide. When PHD2
	// has no calibration, it calibrates before it settles, so the limit
	// covers both.
	guideTimeout = 10 * time.Minute
	// failedGuidingWait is the wait after a failed guide. A cloud passes
	// in minutes, and the wait keeps a PHD2 that fails at once from
	// being retried in a tight loop all night.
	failedGuidingWait = time.Minute
)

// phd2Active reports whether PHD2 in this AppState takes guide
// exposures. PHD2 takes them in every state except Stopped. While it
// calibrates or guides, it also moves the mount.
func phd2Active(state string) bool { return state != "" && state != "Stopped" }

// stop sends stop_capture when PHD2 is taking exposures, and waits
// until PHD2 reports the stop. When the connection to PHD2 does not
// open, stop cannot send the request, so it logs that and lets the slew
// go ahead. The guide before the next frame then fails with
// GuidingFailed if PHD2 still does not respond.
func (g *phd2Guiding) stop(ctx context.Context, w *work) error {
	l := g.link(w.r)
	if l == nil {
		return nil
	}
	if err := l.waitOpen(ctx); err != nil {
		w.r.logf("Target %s: %v, so the mount slews without sending stop_capture", targetName(w.target), err)
		return nil
	}
	if !phd2Active(l.client.State().AppState) {
		return nil
	}
	stopping, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	if err := l.client.StopCapture(stopping); err != nil {
		return fmt.Errorf("sending stop_capture to PHD2 of Guider %s: %w", l.name, err)
	}
	if err := l.client.WaitFor(stopping, func(s phd2.State) bool { return !s.Open || !phd2Active(s.AppState) }); err != nil {
		// The predicate counts a closed connection as stopped, so the
		// wait fails only when a context ends: ctx, as when the
		// reservation ends, or the stop's time limit.
		if ctx.Err() != nil {
			return fmt.Errorf("waiting for PHD2 of Guider %s to stop: %w", l.name, err)
		}
		return fmt.Errorf("PHD2 of Guider %s did not stop within %s", l.name, stopTimeout)
	}
	return nil
}

// start guides before a frame, unless PHD2 is already guiding on a
// settled star. It returns the part of the Imaging message that
// describes how the frames are guided.
func (g *phd2Guiding) start(ctx context.Context, w *work) (string, error) {
	l := g.link(w.r)
	if l == nil {
		if w.target.Spec.Dither != nil {
			return "unguided and not dithered because the telescope has no Guider", nil
		}
		return "unguided because the telescope has no Guider", nil
	}
	if err := l.waitOpen(ctx); err != nil {
		return "", err
	}
	if l.client.State().AppState == "Guiding" && !l.isUnsettled() {
		return guidedBy(l, w), nil
	}
	// After a failed guide, Imaging stays False with GuidingFailed
	// during the retries, so each retry posts no Guiding Event. The
	// Exposing Event of the next frame reports the recovery.
	if !imagingReason(w.target, astrophotography.ReasonGuidingFailed) {
		if err := w.setImaging(true, astrophotography.ReasonGuiding,
			fmt.Sprintf("Reservation %s guides with Guider %s", w.r.name, l.name)); err != nil {
			return "", err
		}
	}
	started := time.Now()
	if err := l.guide(ctx); err != nil {
		l.setUnsettled(true)
		return "", fmt.Errorf("guiding with PHD2 of Guider %s: %w", l.name, err)
	}
	l.setUnsettled(false)
	w.r.o.metrics.guiding.settle.WithLabelValues("guide").Observe(time.Since(started).Seconds())
	return guidedBy(l, w), nil
}

// guide sends loop, find_star, and guide to PHD2, and waits for
// SettleDone.
func (l *guiderLink) guide(ctx context.Context) error {
	c := l.client
	if err := c.Loop(ctx); err != nil {
		return err
	}
	looping, cancel := context.WithTimeout(ctx, loopTimeout)
	defer cancel()
	var last phd2.State
	if err := c.WaitFor(looping, func(s phd2.State) bool {
		last = s
		return !s.Open || s.AppState == "Looping" || s.AppState == "Selected"
	}); err != nil {
		return fmt.Errorf("PHD2 reported neither Looping nor Selected within %s of the loop request", loopTimeout)
	}
	if !last.Open {
		return phd2.ErrDisconnected
	}
	if _, _, err := c.FindStar(ctx); err != nil {
		return err
	}
	if err := c.Guide(ctx, guideSettle, false); err != nil {
		return err
	}
	settling, cancelSettle := context.WithTimeout(ctx, guideTimeout)
	defer cancelSettle()
	return c.WaitSettled(settling)
}

func guidedBy(l *guiderLink, w *work) string {
	how := "guided by Guider " + l.name
	switch d := w.target.Spec.Dither; {
	case d == nil:
		return how
	case d.Every == 1:
		return how + ", dithered after each frame"
	default:
		return fmt.Sprintf("%s, dithered every %d frames", how, d.Every)
	}
}

// guidingFailed sets Imaging to False with the reason GuidingFailed and
// waits failedGuidingWait. It does nothing when ctx has ended.
func (w *work) guidingFailed(ctx context.Context, cause error) error {
	if ctx.Err() != nil || errors.Is(cause, context.Canceled) {
		return nil
	}
	t := w.target
	w.r.logf("target %s: %v", targetName(t), cause)
	w.r.o.metrics.guiding.failures.WithLabelValues(t.Metadata.Namespace, t.Metadata.Name).Inc()
	_, err := w.r.o.writeTarget(t, func(fresh *astrophotography.Target, change *statusChange) bool {
		if fresh.Status.Reservation != w.r.name {
			return false
		}
		change.setImaging(fresh, false, astrophotography.ReasonGuidingFailed,
			fmt.Sprintf("Reservation %s starts guiding again in %s: %v", w.r.name, failedGuidingWait, cause))
		return true
	})
	if err != nil {
		return err
	}
	sleep(ctx, failedGuidingWait, nil)
	return nil
}
