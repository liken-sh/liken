package main

// When the reservations of several telescopes of one observatory end
// together, each one reaches the StopSite step while the others still
// hold their telescopes. If each step skipped while another telescope
// was held, every step could skip, and no step would disconnect the
// observatory's devices or switch off their outputs before the sweep
// stopped its server. So exactly one reservation stops the
// observatory: the one whose telescope's name sorts last among the
// telescopes that are deactivating. It waits until the others have
// released their telescopes, and each of the others skips its step.
// The choice comes from the claims and the telescopes' phases, so it
// holds no state of its own and gives the same answer after an
// operator restart.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type siteAction int

const (
	// siteRun stops the observatory now.
	siteRun siteAction = iota
	// siteInUse skips the step, because a telescope of the observatory
	// is Ready, Activating, or in Error.
	siteInUse
	// siteStoppedByOther skips the step, because another deactivating
	// reservation stops the observatory.
	siteStoppedByOther
	// siteWait waits until the other deactivating reservations release
	// their telescopes, and then asks again.
	siteWait
)

// siteStop is the answer of siteStopFor. reservations names the
// reservation that holds the observatory for siteInUse, the one that
// stops it for siteStoppedByOther, and the ones to wait for for
// siteWait, with their telescopes in telescopes.
type siteStop struct {
	action       siteAction
	site         string
	reservations []string
	telescopes   []string
}

// keepsObservatory reports whether a telescope in this phase uses its
// observatory, so the observatory stays up and the observatory's status
// is not Deactivating.
func keepsObservatory(phase observatory.Phase) bool {
	switch phase {
	case observatory.PhaseReady, observatory.PhaseActivating, observatory.PhaseError:
		return true
	}
	return false
}

// siteStopFor answers what the StopSite step of res does now. It reads
// every other held telescope of the observatory. A telescope that
// keeps the observatory makes the step skip as in use. A telescope
// that is deactivating joins the choice, unless its reservation's
// StopSite step is already Done or Skipped: that reservation has
// stopped the observatory, or skipped while another telescope kept it,
// and runs the step no more, so the choice must not leave the
// observatory to it. A Released reservation has finished every step,
// so it never joins. A telescope that reads Idle, because its
// reservation is gone from the store, takes no part: its runner
// releases it and runs no step.
func (o *operator) siteStopFor(t *tree, res *observatory.Reservation) siteStop {
	telescope, ok := t.telescopes[res.Spec.Telescope]
	if !ok {
		// stopSite skips the step for the missing telescope.
		return siteStop{action: siteRun}
	}
	own, site := telescope.Metadata.Name, telescope.Spec.Observatory
	var leaving, holders []string
	for _, name := range sortedNames(t.telescopes) {
		if name == own || t.telescopes[name].Spec.Observatory != site {
			continue
		}
		holder, held := o.claims.holderOf(name)
		if !held {
			continue
		}
		switch phase, r := o.telescopePhase(t, name); {
		case keepsObservatory(phase):
			return siteStop{action: siteInUse, site: site, reservations: []string{holder}}
		case phase == observatory.PhaseDeactivating && !stoppedSite(r):
			leaving, holders = append(leaving, name), append(holders, holder)
		}
	}
	switch {
	case len(leaving) == 0:
		return siteStop{action: siteRun, site: site}
	case leaving[len(leaving)-1] > own:
		return siteStop{action: siteStoppedByOther, site: site, reservations: holders[len(holders)-1:]}
	}
	return siteStop{action: siteWait, site: site, reservations: holders, telescopes: leaving}
}

// stoppedSite reports whether the reservation's StopSite step has
// finished, Done or Skipped.
func stoppedSite(r *observatory.Reservation) bool {
	s := findStep(r.Status.Steps, observatory.StepStopSite)
	return s != nil && (s.State == observatory.StepDone || s.State == observatory.StepSkipped)
}

// skipMessage is the summary of a StopSite step that skips.
func (s siteStop) skipMessage() string {
	if s.action == siteInUse {
		return "in use by Reservation " + s.reservations[0]
	}
	return fmt.Sprintf("Reservation %s stops Observatory %s", s.reservations[0], s.site)
}

// waitMessage is the summary of the StopSite step while it waits.
func (s siteStop) waitMessage() string {
	release := "Telescope " + s.telescopes[0]
	if len(s.telescopes) > 1 {
		release = "Telescopes " + strings.Join(s.telescopes, ", ")
	}
	verb := "releases"
	if len(s.reservations) > 1 {
		verb = "release"
	}
	return fmt.Sprintf("Stops Observatory %s when %s %s %s", s.site, reservationList(s.reservations), verb, release)
}

// errSiteChanged is the error of a StopSite step that found, under the
// site's lock, a deactivating reservation to wait for. runStep returns
// the step to Pending, so the runner waits again before the step
// starts, and the step's deadline counts only the work of stopping the
// observatory.
var errSiteChanged = errors.New("another reservation of the observatory began to deactivate")

// awaitSite asks siteStopFor until the answer is not siteWait, and
// waits for a change to the tree between the questions: another
// reservation releases its telescope in released, or its status
// changes in the store. While it waits, the StopSite step, still
// Pending, says what it waits for. It returns false when ctx ends or
// the reservation is gone.
func (r *runner) awaitSite(ctx context.Context) (siteStop, bool) {
	for ctx.Err() == nil {
		wake := r.o.structure.wait()
		if !r.refresh() {
			return siteStop{}, false
		}
		stop := r.o.siteStopFor(r.o.snapshot(), r.res)
		if stop.action != siteWait {
			return stop, true
		}
		s := r.step(observatory.StepStopSite)
		if message := stop.waitMessage(); s.Summary != message || r.status.Step != observatory.StepStopSite {
			s.Summary, r.status.Step = message, observatory.StepStopSite
			r.save(ctx)
			r.o.logf("Reservation %s: StopSite: %s", r.name, message)
		}
		select {
		case <-ctx.Done():
		case <-wake:
		}
	}
	return siteStop{}, false
}

// runStopSite runs the StopSite step after awaitSite, or records it as
// skipped with the answer's message. It returns false when the
// deactivation must stop, as runStep's error does.
func (r *runner) runStopSite(ctx context.Context) bool {
	for {
		stop, ok := r.awaitSite(ctx)
		if !ok {
			return false
		}
		step := r.stopSite
		if stop.action != siteRun {
			step = func(context.Context, *stepWork) (outcome, error) { return skipped("%s", stop.skipMessage()) }
		}
		err := r.runStep(ctx, observatory.StepStopSite, step)
		if !errors.Is(err, errSiteChanged) {
			return err == nil
		}
	}
}
