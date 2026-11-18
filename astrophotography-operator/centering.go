package main

// A slew ends near the target, not on it. So after the slew the runner
// takes a short frame, plate-solves it, and measures how far the solved
// center is from the target, both in J2000. When that is more than the
// target's tolerance, the runner syncs the mount to the solved center
// and slews to the target again, up to spec.centering.attempts rounds.
//
// The runner corrects with a sync and not with an offset slew, because
// a sync corrects the mount's pointing model and every later slew of the
// night ends closer too. The sync and the slew both convert J2000 to the
// equinox of the date with sky.ToDate, so an error in that conversion
// cancels out.
//
// When the target's Solver is missing or not Ready, the runner images
// the target where the slew left the mount, says so in the Imaging
// condition, and posts a Warning on the reservation. Frames a few
// arcminutes off the target are still usable, and failing the target
// would leave the night with no frames.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
	"github.com/liken-sh/liken/astrophotography-operator/solver"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// plateCentering centers each target that sets spec.centering.
type plateCentering struct{}

func (plateCentering) center(ctx context.Context, w *work) (string, error) {
	spec := w.target.Spec.Centering
	if spec == nil {
		return "not centered", nil
	}
	if _, err := w.r.o.readySolver(w.r.namespace, spec.Solver); err != nil {
		return w.notCentered(spec.Solver, err), nil
	}
	run, err := w.centeringRun(*spec)
	if err != nil {
		return "", err
	}
	if err := w.setImaging(true, astrophotography.ReasonCentering,
		fmt.Sprintf("Reservation %s centers %s with Solver %s", w.r.name, w.target.Spec.Object, spec.Solver)); err != nil {
		return "", err
	}
	return run.run(ctx)
}

// notCentered posts the Warning for a target that the runner images
// without centering, and returns the text for the Imaging condition.
func (w *work) notCentered(solverName string, err error) string {
	w.r.logf("target %s: imaging without centering: %v", targetName(w.target), err)
	w.r.o.recorder.Warning(reservationRef(w.res), reasonNotCentered,
		fmt.Sprintf("Solver %s is not Ready, so astrophotography-operator images target %s without centering it: %v", solverName, w.target.Metadata.Name, err))
	return fmt.Sprintf("not centered because Solver %s is not Ready", solverName)
}

type centeringAttempt struct {
	w        *work
	spec     astrophotography.Centering
	target   sky.Position
	settings sequenceSettings
	result   astrophotography.CenteringResult
}

// centeringRun takes the frame settings of the first sequence, and
// replaces the exposure, and the filter when one is set, with those of
// spec.centering.
func (w *work) centeringRun(spec astrophotography.Centering) (*centeringAttempt, error) {
	// The slew already parsed these coordinates, so this cannot fail.
	target, _ := targetPosition(w.target)
	settings, err := settingsOf(w.target.Spec.Sequences[0])
	if err != nil {
		return nil, err
	}
	if settings.exposure, err = time.ParseDuration(spec.Exposure); err != nil {
		return nil, fmt.Errorf("spec.centering.exposure %q: %w", spec.Exposure, err)
	}
	if spec.Filter != "" {
		settings.filter = spec.Filter
	}
	return &centeringAttempt{w: w, spec: spec, target: target, settings: settings,
		result: astrophotography.CenteringResult{Reservation: w.r.name}}, nil
}

// run records the result in status.centering and the metrics whenever at
// least one solve finished, so a centering that fails later, such as on
// a sync that the mount refuses, still shows the errors it measured.
func (c *centeringAttempt) run(ctx context.Context) (string, error) {
	how, failed := c.rounds(ctx)
	if c.result.Rounds == 0 {
		return how, failed
	}
	return how, c.record(failed)
}

// rounds returns the text for the Imaging condition, or the reason the
// centering failed.
func (c *centeringAttempt) rounds(ctx context.Context) (string, error) {
	w := c.w
	for round := int32(1); ; round++ {
		solved, err := c.solve(ctx)
		if errors.Is(err, errSolverNotReady) {
			return w.notCentered(c.spec.Solver, err), nil
		}
		if err != nil {
			return "", err
		}
		c.result.Rounds, c.result.Time = round, time.Now().UTC().Truncate(time.Second)
		if !solved.Solved {
			return "", fmt.Errorf("no centering frame of round %d solved, at %s or at %s", round, c.settings.exposure, 2*c.settings.exposure)
		}
		// A solve places the center to a fraction of a pixel, a few
		// tenths of an arcsecond, so more than one decimal is noise.
		offBy := math.Round(sky.Separation(*solved.Center, c.target)*36000) / 10
		c.result.Error = &offBy
		c.result.RoundErrors = append(c.result.RoundErrors, offBy)
		c.result.Solution = &astrophotography.Solution{
			Center:   astrophotography.Coordinates{RA: sky.FormatRA(solved.Center.RA), Dec: sky.FormatDec(solved.Center.Dec)},
			Rotation: solved.Rotation, Scale: solved.Scale,
		}
		w.r.o.metrics.centering.lastError.WithLabelValues(w.target.Metadata.Namespace, w.target.Metadata.Name).Set(offBy)
		if offBy <= c.spec.Tolerance {
			return fmt.Sprintf("centered within %.1f arcseconds in %s", offBy, roundsText(round)), nil
		}
		if round >= c.spec.Attempts {
			return "", fmt.Errorf("after %s the error is %.1f arcseconds, more than the tolerance of %g", roundsText(round), offBy, c.spec.Tolerance)
		}
		if err := w.syncTo(ctx, *solved.Center); err != nil {
			return "", err
		}
		if err := w.goTo(ctx, c.target); err != nil {
			return "", err
		}
	}
}

// solve takes a centering frame and solves it. When the frame does not
// solve, it takes one more at twice the exposure, because a frame with
// too few stars for the solver can show enough at a longer exposure.
func (c *centeringAttempt) solve(ctx context.Context) (solver.SolveResponse, error) {
	settings := c.settings
	var response solver.SolveResponse
	for range 2 {
		var err error
		if response, err = c.w.solveFrame(ctx, c.spec.Solver, settings, c.target); err != nil || response.Solved {
			return response, err
		}
		settings.exposure *= 2
	}
	return response, nil
}

func (c *centeringAttempt) record(failed error) error {
	w := c.w
	w.r.o.metrics.centering.rounds.Observe(float64(c.result.Rounds))
	result := c.result
	_, err := w.r.o.writeTarget(w.target, func(t *astrophotography.Target, _ *statusChange) bool {
		if t.Status.Reservation != w.r.name {
			return false
		}
		t.Status.Centering = &result
		return true
	})
	return errors.Join(failed, err)
}

// syncTo tells the mount that it points at a solved position. It sets
// ON_COORD_SET back to TRACK at the end, so the next write of
// coordinates slews instead of syncing.
//
// After a sync, a mount with an alignment model reports the position
// that its model computes, which can differ from the position sent. The
// telescope simulator reports one a few arcseconds away. So the sync
// waits with Answered, which accepts the first report that is not Busy,
// whatever its values.
func (w *work) syncTo(ctx context.Context, solved sky.Position) error {
	c, mount := w.r.client, w.mountDevice
	jnow := sky.ToDate(solved, time.Now())
	for _, write := range []struct {
		send func() (indi.Sent, error)
		wait func(context.Context, indi.Sent) (indi.Property, error)
	}{
		{func() (indi.Sent, error) {
			return c.SetSwitches(mount, "ON_COORD_SET", map[string]bool{"SYNC": true})
		}, c.Settle},
		{func() (indi.Sent, error) {
			return c.SetNumbers(mount, "EQUATORIAL_EOD_COORD", map[string]float64{"RA": jnow.RAHours(), "DEC": jnow.Dec})
		}, c.Answered},
		{func() (indi.Sent, error) {
			return c.SetSwitches(mount, "ON_COORD_SET", map[string]bool{"TRACK": true})
		}, c.Settle},
	} {
		if _, err := w.r.sendAndWait(ctx, settingTimeout, write.send, write.wait); err != nil {
			return fmt.Errorf("syncing the mount: %w", err)
		}
	}
	return nil
}

func roundsText(n int32) string {
	if n == 1 {
		return "1 round"
	}
	return fmt.Sprintf("%d rounds", n)
}

// centeringFailure fails the target with the reason CenteringFailed. It
// returns err unchanged when the connection was lost or the runner is
// stopping, because neither is a fault of the target, and when err
// already has a reason, such as NoFilter for a centering filter that
// the filter wheel does not have.
func centeringFailure(err error) error {
	var failure *imagingFailure
	if lostConnection(err) || errors.Is(err, context.Canceled) || errors.As(err, &failure) {
		return err
	}
	return fail(astrophotography.ReasonCenteringFailed, "%w", err)
}
