package main

// placeSurfaces decides where each surface on one screen is drawn. It
// reads no clock and opens no socket, so its tests are tables. The
// placement pass in placementpass.go sends the result to the
// compositor module, which draws what it is told.

import (
	"cmp"
	"slices"
)

// The region names that the Display status reports for a screen with
// no Layout (defaultLayoutName) and for a region with no surface
// (emptyRegion).
const (
	defaultLayoutName = "default"
	emptyRegion       = "empty"
)

var wholeScreen = LayoutRect{Width: 1, Height: 1}

// A surface is one window that the compositor reported.
type surface struct {
	id string
	// The claim, as namespace/name, whose Wayland socket the program
	// connected on. It is empty for a surface on the compositor's own
	// socket, wayland-0, which belongs to no claim.
	claimKey string
	// The labels that every pod holding the claim has, with the same
	// value on each pod.
	labels map[string]string
	// A lower value arrived earlier.
	arrival int
}

// A placement says where one surface is drawn. A higher stack is drawn
// over a lower one.
type placement struct {
	surface string
	region  string
	rect    LayoutRect
	stack   int
	// The enter half runs when this placement is drawn. The exit half
	// runs on a later pass, when the surface leaves the region, so the
	// compositor module keeps it until then.
	transition LayoutTransition
}

// A placedRegion gives the surface id that one region shows, or
// emptyRegion.
type placedRegion struct {
	name    string
	surface string
}

type screenPlacement struct {
	placed []placement
	// The ids of the surfaces that no region shows.
	unplaced []string
	regions  []placedRegion
}

// placeSurfaces takes a nil layout when the screen's Display names no
// Layout.
//
// A region shows the surfaces of one claim: the earliest claim whose
// labels match the region's selector and that no earlier region took.
// Every surface of that claim is drawn in the region's rectangle,
// oldest at the bottom, so a player that opens an overlay window over
// its video keeps both on the screen. A later claim that matches is not
// drawn in the same region, because its windows would cover the first
// claim's windows.
func placeSurfaces(surfaces []surface, layout *LayoutSpec) screenPlacement {
	arrived := byArrival(surfaces)
	if layout == nil {
		return defaultPlacement(arrived)
	}

	decision := screenPlacement{}
	taken := make(map[string]bool, len(arrived))
	for stack, region := range layout.Regions {
		shown := placedRegion{name: region.Name, surface: emptyRegion}
		for _, candidate := range arrived {
			// A surface with no claim matches no selector. Without this
			// check, an empty selector, which matches any labels, would
			// put it in a region that a workload is meant to fill.
			if taken[candidate.claimKey] || candidate.claimKey == "" {
				continue
			}
			if !matchesSelector(region.Selector, candidate.labels) {
				continue
			}
			taken[candidate.claimKey] = true
			for _, member := range arrived {
				if member.claimKey != candidate.claimKey {
					continue
				}
				// The stack value is the region's index in
				// layout.Regions, so a small region that the Layout lists
				// after a large one is drawn over it.
				decision.placed = append(decision.placed, placement{
					surface:    member.id,
					region:     region.Name,
					rect:       region.Rect,
					stack:      stack,
					transition: transitionOf(region),
				})
				// The status reports the newest surface, which is on top.
				shown.surface = member.id
			}
			break
		}
		decision.regions = append(decision.regions, shown)
	}

	for _, candidate := range arrived {
		if !taken[candidate.claimKey] {
			decision.unplaced = append(decision.unplaced, candidate.id)
		}
	}
	return decision
}

// defaultPlacement draws every surface over the whole screen with the
// newest on top.
func defaultPlacement(arrived []surface) screenPlacement {
	region := placedRegion{name: defaultLayoutName, surface: emptyRegion}
	var placed []placement
	for stack, candidate := range arrived {
		placed = append(placed, placement{
			surface: candidate.id,
			region:  defaultLayoutName,
			rect:    wholeScreen,
			stack:   stack,
		})
		region.surface = candidate.id
	}
	return screenPlacement{placed: placed, regions: []placedRegion{region}}
}

// byArrival returns a copy of surfaces sorted oldest first, because
// arrival order decides which claim a region shows and which surface is
// on top.
func byArrival(surfaces []surface) []surface {
	arrived := slices.Clone(surfaces)
	slices.SortStableFunc(arrived, func(a, b surface) int {
		return cmp.Compare(a.arrival, b.arrival)
	})
	return arrived
}

// transitionOf returns an empty transition for a region that sets
// none, so its surfaces appear and disappear with no animation.
func transitionOf(region LayoutRegion) LayoutTransition {
	if region.Transition == nil {
		return LayoutTransition{}
	}
	return *region.Transition
}

// matchesSelector follows the rules of a Kubernetes label selector:
// every entry of matchLabels and every requirement of matchExpressions
// must match, and a selector with neither matches any labels.
func matchesSelector(selector LabelSelector, labels map[string]string) bool {
	for key, want := range selector.MatchLabels {
		held, ok := labels[key]
		if !ok || held != want {
			return false
		}
	}
	for _, requirement := range selector.MatchExpressions {
		if !matchesRequirement(requirement, labels) {
			return false
		}
	}
	return true
}

// As in Kubernetes, NotIn matches labels that do not have the key.
func matchesRequirement(requirement LabelSelectorRequirement, labels map[string]string) bool {
	held, ok := labels[requirement.Key]
	switch requirement.Operator {
	case selectorIn:
		return ok && slices.Contains(requirement.Values, held)
	case selectorNotIn:
		return !ok || !slices.Contains(requirement.Values, held)
	case selectorExists:
		return ok
	case selectorDoesNotExist:
		return !ok
	}
	// An unknown operator matches nothing, so a selector that this
	// program cannot read places no surface.
	return false
}
