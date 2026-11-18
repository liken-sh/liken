package main

// A resource's spec.requires lists conditions on other resources that
// must be True before a reservation starts the resource's equipment,
// such as a person's SetUp on a portable telescope, or a
// WeatherStation's Safe so that nothing powers on in the rain.
//
// The reservation checks requirements before it starts each INDI
// server. The Wait step checks the requirements of the Observatory and
// of the devices on its server, before the reservation takes the
// telescope. The Requires step checks those of the Telescope and of the
// devices on its server, after StartSite and before PowerOn. So a
// Telescope can require the Safe of a WeatherStation on the
// Observatory's server, because the station reports Safe only after
// StartSite connects it. The same requirement on the Observatory would
// never be met.
//
// Requirements are checked only before the session starts. When a
// condition turns False while the equipment runs, a trigger in
// spec.triggers must handle it.
//
// When a step still waits at dawn, the reservation ends without
// powering on the telescope's equipment, because a session that starts
// after dawn cannot take images. The step becomes Skipped with the
// conditions it waited for in its summary. After the Requires step,
// deactivation stops what StartSite started. After the Wait step, the
// reservation is Released without taking the telescope.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// gateOf returns the resources whose requirements are checked before a
// server starts: the server's owner and each device on that server.
func (t *tree) gateOf(owner resource, ref serverRef) []resource {
	resources := []resource{owner}
	for _, d := range t.devicesOn(ref) {
		resources = append(resources, t.ofDevice(d))
	}
	return resources
}

// unmet returns the requirements that are not met as waiting and those
// that are met as held, each as text such as "Telescope east
// SetUp=True". A requirement whose target does not exist is not met,
// and its text names the spec.requires entry so a person can find it.
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

// dawnError ends the Wait or Requires step at dawn. runStep records
// the step as Skipped with the error's text.
type dawnError struct{ summary string }

func (g dawnError) Error() string { return g.summary }

// dawnPrefix starts the summary of a step that dawn ended. A reservation
// that ends for another reason, such as spec.end, also leaves Wait or
// Requires Skipped, so the prefix tells the two apart.
const dawnPrefix = "Dawn came at "

func endedAtDawn(steps []observatory.Step) bool {
	return dawnSummary(steps) != ""
}

// dawnSummary returns the summary of the step that dawn ended, or ""
// when dawn ended none.
func dawnSummary(steps []observatory.Step) string {
	for _, s := range steps {
		if (s.Name == observatory.StepWait || s.Name == observatory.StepRequires) &&
			s.State == observatory.StepSkipped && strings.HasPrefix(s.Summary, dawnPrefix) {
			return s.Summary
		}
	}
	return ""
}

// gate is the state of the requirements checked before one server
// starts.
type gate struct {
	// waiting is the step's message, or "" when every requirement is met.
	waiting string
	held    []string
	// dawn is when the wait ends. It is zero when nothing waits, and
	// when the telescope's Observatory is missing, because dawn needs
	// the Observatory's location.
	dawn time.Time
}

// gateOn checks the requirements of the resources. It returns
// dawnError when a requirement is not met and the first astronomical
// dawn after began, at the telescope's Observatory, has passed.
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
		return gate{}, dawnError{fmt.Sprintf(dawnPrefix+"%s while waiting for %s, so the session did not start",
			g.dawn.UTC().Format(time.RFC3339), list)}
	}
	return g, nil
}

// requires runs the Requires step. A change to the tree wakes it, and
// its only timer is the dawn that ends the wait.
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
			return outcome{}, fmt.Errorf("spec.telescope names Telescope %s, which does not exist", r.res.Spec.Telescope)
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

func asDawnError(err error) (dawnError, bool) {
	var g dawnError
	return g, errors.As(err, &g)
}
