package main

// Guiding with PHD2 (plan 01, steps 2 and 6). PHD2 must not guide while
// the mount slews, because it would chase the stars the mount leaves
// behind, so the runner sends stop_capture before each slew. Before
// each frame, the runner checks that PHD2 guides a settled star. When
// it does not, as after a slew or a star lost during the last exposure,
// the runner sends loop, find_star, and guide, the order that
// observatory-operator's plan 09 drilled, and waits for SettleDone,
// because a frame taken while the star moves is blurred.
//
// A failure to guide, such as a cloud that hides every star, keeps the
// target. Imaging turns False with GuidingFailed, and the runner tries
// again before the next frame a minute later. The frames wait, because
// a long frame with no guiding trails, and a target whose telescope has
// a Guider asks for guided frames.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/phd2"
)

// guideSettle is when PHD2 reports that a guide or a dither settled: the
// star stays within 1.5 pixels of the lock position for 8 seconds, the
// values that plan 09's drill settled with in 10 guide frames. PHD2
// ends a settle that takes longer than a minute with an error.
var guideSettle = phd2.Settle{Pixels: 1.5, Time: 8 * time.Second, Timeout: time.Minute}

const (
	// stopTimeout bounds stop_capture and the events that report the
	// stop. PHD2 answers at once and reports the stop after its current
	// guide exposure.
	stopTimeout = 30 * time.Second
	// loopTimeout bounds the wait for PHD2's first guide frame after
	// loop.
	loopTimeout = time.Minute
	// guideTimeout bounds a guide from the request to SettleDone. PHD2
	// calibrates first when it holds no calibration, which took 73
	// seconds in plan 09's drill, and then settles.
	guideTimeout = 10 * time.Minute
	// failedGuidingWait is the wait after guiding failed, before the
	// runner guides again. A cloud passes in minutes, and the wait
	// keeps a PHD2 that fails at once from cycling all night.
	failedGuidingWait = time.Minute
)

// busy reports whether PHD2 takes guide exposures in a state. Every
// state but Stopped loops, calibrates, or guides, and moves the mount
// or reads the star.
func busy(state string) bool { return state != "" && state != "Stopped" }

// stop sends stop_capture when PHD2 takes exposures, and waits until
// PHD2 reports the stop. A PHD2 that does not answer cannot stop, and
// the slew goes ahead: its connection to the mount goes through the
// same INDI server, and the next start reports the guiding as failed.
func (g *phd2Guiding) stop(ctx context.Context, w *work) error {
	l := g.link(w.r)
	if l == nil {
		return nil
	}
	if err := l.waitOpen(ctx); err != nil {
		w.r.logf("target %s: %v, so the mount slews without stop_capture", targetName(w.target), err)
		return nil
	}
	if !busy(l.client.State().AppState) {
		return nil
	}
	stopping, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	if err := l.client.StopCapture(stopping); err != nil {
		return fmt.Errorf("PHD2 of Guider %s: stop_capture: %w", l.name, err)
	}
	if err := l.client.WaitFor(stopping, func(s phd2.State) bool { return !s.Open || !busy(s.AppState) }); err != nil {
		return fmt.Errorf("PHD2 of Guider %s did not stop within %s", l.name, stopTimeout)
	}
	return nil
}

// start guides before a frame, unless PHD2 already guides a settled
// star.
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
	// A failure that repeats keeps its condition, so the retries post
	// no Events, and the next frame's Exposing posts the recovery.
	if !imagingReason(w.target, astrophotography.ReasonGuidingFailed) {
		if err := w.setImaging(true, astrophotography.ReasonGuiding,
			fmt.Sprintf("Reservation %s guides with Guider %s.", w.r.name, l.name)); err != nil {
			return "", err
		}
	}
	started := time.Now()
	if err := l.guide(ctx); err != nil {
		l.setUnsettled(true)
		return "", fmt.Errorf("PHD2 of Guider %s: %w", l.name, err)
	}
	l.setUnsettled(false)
	w.r.o.metrics.guiding.settle.WithLabelValues("guide").Observe(time.Since(started).Seconds())
	return guidedBy(l, w), nil
}

// guide starts PHD2's exposures, selects the best star in the first
// frame, guides on it, and waits for SettleDone.
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
		return fmt.Errorf("no guide frame within %s of loop", loopTimeout)
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

// guidedBy says how the frames are guided, for the Imaging condition's
// message.
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

// guidingFailed sets Imaging False with GuidingFailed and waits before
// the next frame, which guides again. The runner keeps the target.
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
			fmt.Sprintf("Reservation %s: %v. The runner guides again in %s.", w.r.name, cause, failedGuidingWait))
		return true
	})
	if err != nil {
		return err
	}
	sleep(ctx, failedGuidingWait, nil)
	return nil
}
