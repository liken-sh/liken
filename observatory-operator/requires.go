package main

// The spec.requires of the resources that a session starts. Each
// resource can list conditions that must hold before the session
// starts it, as a pod's readinessGates hold a pod: a person's SetUp on
// a portable telescope, or a WeatherStation's Safe, so nothing powers on
// in the rain. Plan 21 gives the design.
//
// Two steps hold the gates, because each server needs its own. The
// Observatory's requirements, and those of the devices on its server,
// hold the Wait step, so the observatory's server does not start
// before they hold. The Telescope's requirements, and those of its
// devices, hold the Requires step, after StartSite and before PowerOn.
// That order lets a telescope require the Safe of a WeatherStation
// that the observatory's server runs: the station reports Safe only
// once StartSite has connected it. The same requirement on the
// Observatory would wait for good, and its step's message says what it
// waits for.
//
// A requirement gates only the start of a session. A condition that
// turns False once the equipment runs changes nothing here: a trigger
// in spec.triggers acts on it, as plan 13 gives triggers that job.
//
// A reservation that still waits in either step at dawn ends with the
// telescope's equipment off, because the night is over: a session that
// started after dawn could not image, and a reservation that waited
// all day would wake the equipment at an hour nobody chose. The step is
// Skipped with the conditions it waited for, and deactivation stops
// whatever StartSite started.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// gateOf answers the resources whose requirements hold one server's
// start: the server's owner, and each device on the server.
func (t *tree) gateOf(owner resource, ref serverRef) []resource {
	resources := []resource{owner}
	for _, d := range t.devicesOn(ref) {
		resources = append(resources, t.ofDevice(d))
	}
	return resources
}

// unmet answers each requirement of the resources that does not hold
// now, such as "Telescope east SetUp=True", in the order of the
// resources and their lists. A requirement that names no resource does
// not hold, and its text names the field, so a person can find it.
// held answers the ones that hold, in the same form.
func (t *tree) unmet(resources []resource) (waiting, held []string) {
	for _, r := range resources {
		for i, req := range r.procedures.Requires {
			targets, err := t.resolve(r, fmt.Sprintf("spec.requires[%d] of %s", i, r), req.Kind, req.Name, false)
			if err != nil {
				waiting = append(waiting, err.Error())
				continue
			}
			want := conditionStatus(req.Status)
			text := fmt.Sprintf("%s %s=%s", targets[0], req.Type, want)
			conditions, _ := t.conditionsOf(targets[0].kind, targets[0].name)
			if conditionOf(conditions, req.Type).Status != want {
				waiting = append(waiting, text)
			} else {
				held = append(held, text)
			}
		}
	}
	return waiting, held
}

// gaveUpError ends a gate's step at dawn. runStep records the step as
// Skipped with the error's text, and the reservation then deactivates
// (gaveUp).
type gaveUpError struct{ summary string }

func (g gaveUpError) Error() string { return g.summary }

// dawnCame starts the summary of a gate's step that dawn ended. A
// reservation that ends for another reason, such as spec.end, also
// leaves Wait or Requires Skipped, with "Reservation ended", so the
// summary tells the two apart.
const dawnCame = "Dawn came at "

// gaveUp reports whether a gate's step ended the reservation at dawn.
func gaveUp(steps []observatory.Step) bool {
	return gaveUpSummary(steps) != ""
}

// gaveUpSummary answers the summary of the gate's step that ended the
// reservation at dawn, for the reservation's Events, or "".
func gaveUpSummary(steps []observatory.Step) string {
	for _, s := range steps {
		if (s.Name == observatory.StepWait || s.Name == observatory.StepRequires) &&
			s.State == observatory.StepSkipped && strings.HasPrefix(s.Summary, dawnCame) {
			return s.Summary
		}
	}
	return ""
}

// gate is one gate's wait as one reading of the tree finds it.
type gate struct {
	// waiting is the step's message, "waiting for" each condition, or
	// "" when every requirement holds.
	waiting string
	// held names each requirement that holds.
	held []string
	// dawn ends the wait. It is zero when nothing waits, or when the
	// telescope's Observatory is missing and gives no place.
	dawn time.Time
}

// gateOn reads one gate: the requirements of the resources, and the
// first astronomical dawn after the wait began at the telescope's
// Observatory. It fails with gaveUpError once that dawn has come and a
// requirement still does not hold.
func gateOn(t *tree, resources []resource, telescope *observatory.Telescope, began time.Time) (gate, error) {
	waiting, held := t.unmet(resources)
	g := gate{held: held}
	if len(waiting) == 0 {
		return g, nil
	}
	list := strings.Join(waiting, ", ")
	g.waiting = "waiting for " + list
	if site, ok := t.observatories[telescope.Spec.Observatory]; ok {
		g.dawn = dawnAfter(began, site.Spec.Location)
	}
	if !g.dawn.IsZero() && !time.Now().Before(g.dawn) {
		return gate{}, gaveUpError{fmt.Sprintf(dawnCame+"%s while waiting for %s, so the session did not start",
			g.dawn.UTC().Format(time.RFC3339), list)}
	}
	return g, nil
}

// requires holds the Requires step until every requirement of the
// Telescope and of the devices on its server holds, or ends the
// reservation at the first dawn after the step began. The timer is a
// clock: dawn.
func (r *runner) requires(ctx context.Context, w *stepWork) (outcome, error) {
	began := *r.step(observatory.StepRequires).StartTime
	for {
		if ctx.Err() != nil {
			return outcome{}, context.Cause(ctx)
		}
		wake := r.o.structure.wait()
		t := r.o.snapshot()
		telescope, ok := t.telescopes[r.res.Spec.Telescope]
		if !ok {
			return outcome{}, fmt.Errorf("missing Telescope %s", r.res.Spec.Telescope)
		}
		g, err := gateOn(t, t.gateOf(ofTelescope(telescope), serverRef{observatory.TelescopeKind, telescope.Metadata.Name}), telescope, began)
		switch {
		case err != nil:
			return outcome{}, err
		case g.waiting == "" && len(g.held) > 0:
			return done("found %s", strings.Join(g.held, ", "))
		case g.waiting == "":
			return skipped("no requirements on Telescope %s or its devices", telescope.Metadata.Name)
		}
		w.report(g.waiting)
		awaitChangeOr(ctx, wake, g.dawn)
	}
}

// isGaveUp reports whether a step's error is a gate's end at dawn.
func isGaveUp(err error) (gaveUpError, bool) {
	var g gaveUpError
	return g, errors.As(err, &g)
}
