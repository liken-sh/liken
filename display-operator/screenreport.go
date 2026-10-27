package main

// What the placement pass writes to a Display about one screen: the
// windows on it, the layout it is drawn to, the arrangers inside its
// regions, and the two conditions the pass owns. The pass decides and
// places in placementpass.go, and this file turns the decision into
// status.

import (
	"errors"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// report writes what one screen shows. The write happens only where
// the status this pass computed differs from the published one, so a
// steady screen writes nothing.
//
// The Display controller writes the same status from its own loop,
// and the two write different fields of it: this pass writes the
// surfaces, the layout, and the LayoutResolved condition, and the
// controller writes the panel's own facts. Each starts from the
// status it read, so neither drops what the other wrote. A write the
// two raced is refused with a conflict, and this pass reads the
// Display again and composes its fields onto the fresh status.
//
// The arrangers' Events compare the arrangers of the copy the last
// compose read with the arrangers it wrote, so a write that a conflict
// made this pass compose again posts what the landed write changed.
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
		// A Display somebody deleted is the Display controller's to
		// create again, and this pass reports the screen after that.
		return nil
	}
	if err == nil {
		p.announceArrangers(display, before, after, departuresOn(held))
	}
	return err
}

func (p *placementPass) condition(met bool, reason, message string) DisplayCondition {
	return p.stated(LayoutResolvedCondition, met, reason, message)
}

// serving is the condition that reports the compositor. Its message is
// the socket's own words for the failure, so a person reads the cause
// from the resource.
func (p *placementPass) serving(live compositorLiveness) DisplayCondition {
	return p.stated(CompositorServingCondition, live.serving, live.reason, live.detail)
}

func (p *placementPass) stated(kind string, met bool, reason, message string) DisplayCondition {
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

// Every surface on the screen, whether or not a region took it. A
// surface with no region is a program that is running and not on the
// screen, which is the first thing to read when a program draws
// nothing a person can see.
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

// Each region in stacking order, with the surface it shows or the
// word empty.
func regionStatus(decision screenPlacement) []DisplayRegion {
	var regions []DisplayRegion
	for _, region := range decision.regions {
		regions = append(regions, DisplayRegion{Name: region.name, Surface: region.surface})
	}
	return regions
}

// The name status reports: the Layout in force, or the word default
// for a screen that names none and for a name that resolved to
// nothing.
func layoutName(named string, layout *LayoutSpec) string {
	if layout == nil {
		return defaultLayoutName
	}
	return named
}
