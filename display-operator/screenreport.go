package main

// This file writes the placement pass's result to a Display's status.
// placementpass.go decides what goes where, and report turns that
// decision into status fields.

import (
	"errors"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// report writes the surfaces, layout, arrangers, and the LayoutResolved
// and CompositorServing conditions of one screen to the Display's
// status. It writes nothing when the status it computed equals the
// published one.
//
// The Display controller also writes this status and owns the panel
// fields. Each writer sets only its own fields on the status it last
// read. On a conflict, settleStatus reads the Display again and
// composes again. before and after come from the last compose, so the
// arranger Events describe the write that succeeded.
func (p *placementPass) report(display *Display, held *screenSurfaces, decision screenPlacement,
	name string, resolved, serving DisplayCondition) error {
	on, arrangers := surfacesOn(held), arrangersOn(held)
	var before, after []DisplayArranger
	compose := func(published DisplayStatus) (DisplayStatus, bool) {
		status := published
		status.Surfaces = surfaceStatus(on, decision)
		status.Layout = &DisplayLayout{Name: name, Regions: regionStatus(decision)}
		status.Arrangers = arrangerStatus(arrangers, status.Surfaces)
		status.Conditions = setCondition(status.Conditions, resolved)
		status.Conditions = setCondition(status.Conditions, serving)
		before, after = published.Arrangers, status.Arrangers
		return status, true
	}
	err := p.displays.settleStatus(display, compose)
	if errors.Is(err, apiclient.ErrNotFound) {
		// The Display controller creates a deleted Display again, and a
		// later pass reports to it.
		return nil
	}
	if err == nil {
		p.announceArrangers(display, before, after, departuresOn(held))
	}
	return err
}

func (p *placementPass) condition(met bool, reason, message string) DisplayCondition {
	return p.newCondition(LayoutResolvedCondition, met, reason, message)
}

// serving builds the CompositorServing condition. Its message is the
// detail from the probe of the compositor's socket, so the cause of a
// failure shows on the resource.
func (p *placementPass) serving(live compositorLiveness) DisplayCondition {
	return p.newCondition(CompositorServingCondition, live.serving, live.reason, servingMessage(live))
}

// servingMessage states what the probe found, and puts the socket
// error after it.
func servingMessage(live compositorLiveness) string {
	switch {
	case live.serving:
		return "The compositor replies on its Wayland socket"
	case live.reason == CompositorHungReason:
		return "The compositor accepts connections on its Wayland socket and does not reply: " + live.detail
	default:
		return "The compositor's Wayland socket refuses or closes the connection: " + live.detail
	}
}

func (p *placementPass) newCondition(kind string, met bool, reason, message string) DisplayCondition {
	status := conditionFalse
	if met {
		status = conditionTrue
	}
	return DisplayCondition{
		Type:               kind,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: p.now().UTC().Truncate(time.Second),
	}
}

// surfaceStatus lists every surface on the screen, including those that
// no region took. An empty Region shows a person why a running program
// is not visible.
func surfaceStatus(on []screenSurface, decision screenPlacement) []DisplaySurface {
	regions := map[string]string{}
	for _, place := range decision.placed {
		regions[place.surface] = place.region
	}
	var surfaces []DisplaySurface
	for _, surface := range on {
		surfaces = append(surfaces, DisplaySurface{
			ID:     surface.id,
			Claim:  surface.claim,
			Pods:   surface.pods,
			Labels: surface.labels,
			Size:   &SurfaceSize{Width: surface.Width, Height: surface.Height},
			Region: regions[surface.id],
			AppID:  surface.AppID,
		})
	}
	return surfaces
}

// regionStatus lists each region in stacking order with the surface it
// shows. Surface is emptyRegion when the region shows none.
func regionStatus(decision screenPlacement) []DisplayRegion {
	var regions []DisplayRegion
	for _, region := range decision.regions {
		regions = append(regions, DisplayRegion{Name: region.name, Surface: region.surface})
	}
	return regions
}

// layoutName returns the name that status.layout reports: the named
// Layout when one resolved, and defaultLayoutName when the screen names
// no Layout or the name matched none.
func layoutName(named string, layout *LayoutSpec) string {
	if layout == nil {
		return defaultLayoutName
	}
	return named
}
