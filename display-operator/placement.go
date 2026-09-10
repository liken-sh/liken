package main

// The decision: from the surfaces on one screen and the Layout that
// screen names, to where each surface is drawn.
//
// The function reads no clock, opens no socket, and calls nothing,
// so its tests are tables and its result depends on its inputs
// alone. Its return type is the seam where a different layout engine
// would plug in: another engine would produce the same placement from
// the same surfaces and labels, and the compositor's module would
// commit it the same way. The module executes placements and decides
// nothing, and this function decides placements and executes nothing.
// plans/open-problems/an-external-layout-engine.md says what a second
// engine would need.

import (
	"cmp"
	"slices"
)

// The two words the status uses where there is nothing to name. A
// screen that names no Layout is drawn to the default, and a region
// that took no surface shows nothing.
const (
	defaultLayoutName = "default"
	emptyRegion       = "empty"
)

// The whole screen, which is the default layout's one region.
var wholeScreen = LayoutRect{Width: 1, Height: 1}

// One surface the compositor reported, and the labels of the pods
// that hold its claim.
type surface struct {
	// The id the compositor's controller assigned.
	id string
	// The claim whose socket the surface arrived on, as
	// namespace/name. A surface on the shared socket belongs to no
	// claim and carries no key.
	claimKey string
	// The labels every holder of the claim carries with the same
	// value. A claim-less surface carries none.
	labels map[string]string
	// The order the surface arrived in, lowest first.
	arrival int
}

// Where one surface is drawn. Stack is its position in the render
// order, and a higher one is nearer the viewer.
type placement struct {
	surface    string
	region     string
	rect       LayoutRect
	stack      int
	transition LayoutTransition
}

// What one region of the layout shows: a surface id, or the word
// empty.
type placedRegion struct {
	name    string
	surface string
}

// The decision for one screen.
type screenPlacement struct {
	placed   []placement
	unplaced []string
	regions  []placedRegion
}

// placeSurfaces decides where every surface on one screen is drawn. A
// nil layout is the screen that names none.
//
// A region shows the first surface to arrive whose labels match its
// selector, and it shows one. A second matching surface stays
// unplaced, because a region is one rectangle and the newer surface
// would either hide the older one or fight it for the space.
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
			// A surface that holds no claim matches no selector: its
			// socket names no claim, so there are no holders and no
			// labels to read, and an empty selector must not sweep it
			// into a region a workload was meant to fill.
			if taken[candidate.id] || candidate.claimKey == "" {
				continue
			}
			if !matchesSelector(region.Selector, candidate.labels) {
				continue
			}
			taken[candidate.id] = true
			shown.surface = candidate.id
			decision.placed = append(decision.placed, placement{
				surface: candidate.id,
				region:  region.Name,
				rect:    region.Rect,
				// The region's position in the list is the stacking
				// order, so a small region written after a large one
				// draws in the corner of it.
				stack:      stack,
				transition: transitionOf(region),
			})
			break
		}
		decision.regions = append(decision.regions, shown)
	}

	for _, candidate := range arrived {
		if !taken[candidate.id] {
			decision.unplaced = append(decision.unplaced, candidate.id)
		}
	}
	return decision
}

// The default: one region over the whole screen that shows every
// surface, with the newest on top. That is what kiosk-shell did, so a
// cluster that writes no Layout keeps every screen showing what it
// showed.
func defaultPlacement(arrived []surface) screenPlacement {
	region := placedRegion{name: defaultLayoutName, surface: emptyRegion}
	var placed []placement
	for stack, candidate := range arrived {
		placed = append(placed, placement{
			surface:    candidate.id,
			region:     defaultLayoutName,
			rect:       wholeScreen,
			stack:      stack,
			transition: LayoutTransition{Kind: transitionNone},
		})
		// The last surface to arrive is the top of the stack, so it is
		// the one the region shows.
		region.surface = candidate.id
	}
	return screenPlacement{placed: placed, regions: []placedRegion{region}}
}

// The surfaces in the order they arrived, oldest first. The caller's
// order is not read, because the arrival order decides which surface a
// region takes and which surface is on top.
func byArrival(surfaces []surface) []surface {
	arrived := slices.Clone(surfaces)
	slices.SortStableFunc(arrived, func(a, b surface) int {
		return cmp.Compare(a.arrival, b.arrival)
	})
	return arrived
}

// The transition a region states, or none.
func transitionOf(region LayoutRegion) LayoutTransition {
	if region.Transition == nil {
		return LayoutTransition{Kind: transitionNone}
	}
	return *region.Transition
}

// Whether one set of labels satisfies a selector, by the rules a
// Service's selector follows: every entry of matchLabels and every
// requirement of matchExpressions has to hold, and a selector that
// states neither matches any labels.
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

// One requirement of matchExpressions. NotIn holds for labels that do
// not carry the key at all, the same as DoesNotExist, which is the
// upstream rule.
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
	// An operator the schema does not state matches nothing. A
	// selector this program cannot read must not place a surface,
	// because the region it would fill was written for another one.
	return false
}
