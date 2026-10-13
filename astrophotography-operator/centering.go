package main

// Centering (plan 02). A mount's slew lands near the target, not on
// it, so after the slew the runner takes a short frame, solves it, and
// compares the solved center with the target, both J2000. Outside the
// target's tolerance, it syncs the mount to the solved center and slews
// to the target again, until the error is within the tolerance or the
// rounds run out.
//
// A sync corrects where the mount reports it points, so each later
// slew of the night lands closer too. An offset slew, which moves by
// the error and leaves the mount's model alone, would make every later
// slew miss by the same amount. Both sides of the sync convert J2000 to
// the equinox of the date with the same sky.ToDate, so an error of the
// conversion cancels.
//
// A target whose Solver is missing or not Ready is imaged where the
// slew left the mount, and the Imaging condition and a Warning on the
// reservation say so. Plan 02 gives no rule for that case. Frames a few
// arcminutes off the target are still frames of it, and a night with
// no frames at all is lost.

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

// plateCentering centers each target that states spec.centering with
// a plate solve.
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
		fmt.Sprintf("Reservation %s centers %s with Solver %s.", w.r.name, w.target.Spec.Object, spec.Solver)); err != nil {
		return "", err
	}
	return run.rounds(ctx)
}

// notCentered posts the Warning of a target that the runner images
// without centering, and answers the Imaging condition's words for it.
func (w *work) notCentered(solverName string, err error) string {
	w.r.logf("target %s: imaging without centering: %v", targetName(w.target), err)
	w.r.o.recorder.Warning(reservationRef(w.res), reasonNotCentered,
		fmt.Sprintf("Solver %s is not Ready, so astrophotography-operator images target %s without centering it: %v", solverName, w.target.Metadata.Name, err))
	return fmt.Sprintf("not centered because Solver %s is not Ready", solverName)
}

// centeringPass is one centering of a target: the frame each round takes,
// and where the target is.
type centeringPass struct {
	w        *work
	spec     astrophotography.Centering
	target   sky.Position
	settings sequenceSettings
	result   astrophotography.CenteringResult
}

// centeringRun reads the settings of a target's centering frames: the
// first sequence's, with the centering's exposure, and its filter when
// it names one.
func (w *work) centeringRun(spec astrophotography.Centering) (*centeringPass, error) {
	// The slew read the same coordinates before the centering starts.
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
	return &centeringPass{w: w, spec: spec, target: target, settings: settings,
		result: astrophotography.CenteringResult{Reservation: w.r.name}}, nil
}

// rounds solves, syncs, and slews again until the error is within the
// tolerance or spec.attempts rounds have passed. Whatever ends the
// centering after a round's solve answered, such as a sync that the
// mount refuses, it records the rounds in status.centering and in the
// series, so a centering that failed leaves the errors it measured.
func (c *centeringPass) rounds(ctx context.Context) (string, error) {
	how, failed := c.round(ctx)
	if c.result.Rounds == 0 {
		return how, failed
	}
	return how, c.end(failed)
}

// round runs the rounds of rounds, and answers how the target was
// centered or why the centering failed.
func (c *centeringPass) round(ctx context.Context) (string, error) {
	w := c.w
	for round := int32(1); ; round++ {
		answer, err := c.solve(ctx)
		if errors.Is(err, errSolverNotReady) {
			return w.notCentered(c.spec.Solver, err), nil
		}
		if err != nil {
			return "", err
		}
		c.result.Rounds, c.result.Time = round, time.Now().UTC().Truncate(time.Second)
		if !answer.Solved {
			return "", fmt.Errorf("no centering frame of round %d solved, at %s or at %s", round, c.settings.exposure, 2*c.settings.exposure)
		}
		// A solve places the center to a fraction of a pixel, a few
		// tenths of an arcsecond, so a tenth is the last digit that means
		// anything.
		offBy := math.Round(sky.Separation(*answer.Center, c.target)*36000) / 10
		c.result.Error = &offBy
		c.result.RoundErrors = append(c.result.RoundErrors, offBy)
		c.result.Solution = &astrophotography.Solution{
			Center:   astrophotography.Coordinates{RA: sky.FormatRA(answer.Center.RA), Dec: sky.FormatDec(answer.Center.Dec)},
			Rotation: answer.Rotation, Scale: answer.Scale,
		}
		w.r.o.metrics.centering.lastError.WithLabelValues(w.target.Metadata.Namespace, w.target.Metadata.Name).Set(offBy)
		if offBy <= c.spec.Tolerance {
			return fmt.Sprintf("centered within %.1f arcseconds in %s", offBy, roundsText(round)), nil
		}
		if round >= c.spec.Attempts {
			return "", fmt.Errorf("after %s the error is %.1f arcseconds, more than the tolerance of %g", roundsText(round), offBy, c.spec.Tolerance)
		}
		if err := w.syncTo(ctx, *answer.Center); err != nil {
			return "", err
		}
		if err := w.goTo(ctx, c.target); err != nil {
			return "", err
		}
	}
}

// solve takes one centering frame and solves it, and when it does not
// solve, takes one more at twice the exposure (plan 02): a frame with
// too few stars to solve can have enough at twice the light.
func (c *centeringPass) solve(ctx context.Context) (solver.SolveAnswer, error) {
	settings := c.settings
	var answer solver.SolveAnswer
	for range 2 {
		var err error
		if answer, err = c.w.solveFrame(ctx, c.spec.Solver, settings, c.target); err != nil || answer.Solved {
			return answer, err
		}
		settings.exposure *= 2
	}
	return answer, nil
}

// end records the centering in status.centering and in the series of
// rounds, and answers failed, the centering's outcome.
func (c *centeringPass) end(failed error) error {
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

// syncTo tells the mount that it points at a solved position: SYNC in
// ON_COORD_SET, the position at the equinox of the date, and TRACK
// again, so the next write of coordinates slews.
//
// The mount answers the position with where it now computes that it
// points, and that need not be the position sent: the telescope
// simulator feeds a sync to its alignment model and answers a position
// 3 arcseconds from the one sent. So the position's write ends on the
// mount's first answer, in any state but Busy or Alert, and does not
// wait for the values sent, which such a mount never reports.
func (w *work) syncTo(ctx context.Context, solved sky.Position) error {
	c, mount := w.r.client, w.mountDevice
	jnow := sky.ToDate(solved, time.Now())
	for _, write := range []struct {
		send   func() (indi.Sent, error)
		answer func(context.Context, indi.Sent) (indi.Property, error)
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
		if _, err := w.r.answer(ctx, settingTimeout, write.send, write.answer); err != nil {
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

// centeringFailure fails the target with CenteringFailed, unless the
// connection ended or the runner stops, which end the work without a
// verdict on the target, or the error already carries its reason, such
// as NoFilter for a centering filter that the wheel does not hold.
func centeringFailure(err error) error {
	var failure *imagingFailure
	if lostConnection(err) || errors.Is(err, context.Canceled) || errors.As(err, &failure) {
		return err
	}
	return fail(astrophotography.ReasonCenteringFailed, "%w", err)
}
