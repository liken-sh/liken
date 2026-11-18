package main

import (
	"reflect"
	"testing"
)

func claimedSurface(id, claim string, arrival int, labels map[string]string) surface {
	return surface{id: id, claimKey: claim, labels: labels, arrival: arrival}
}

func labelRegion(name string, rect LayoutRect, labels map[string]string) LayoutRegion {
	return LayoutRegion{Name: name, Rect: rect, Selector: LabelSelector{MatchLabels: labels}}
}

var (
	noticesRect = LayoutRect{Left: 0, Top: 0, Width: 0.7, Height: 1}
	cornerRect  = LayoutRect{Left: 0.7, Top: 0, Width: 0.3, Height: 0.6}
)

func TestPlaceSurfaces(t *testing.T) {
	for _, test := range []struct {
		name     string
		surfaces []surface
		layout   *LayoutSpec
		want     screenPlacement
	}{
		{
			name: "a screen that names no Layout and has no surface",
			want: screenPlacement{
				regions: []placedRegion{{name: "default", surface: "empty"}},
			},
		},
		{
			name:     "a screen that names no Layout draws its one surface over the whole screen",
			surfaces: []surface{claimedSurface("a-1", "media/idle", 0, nil)},
			want: screenPlacement{
				placed: []placement{
					{surface: "a-1", region: "default", rect: LayoutRect{Width: 1, Height: 1}},
				},
				regions: []placedRegion{{name: "default", surface: "a-1"}},
			},
		},
		{
			name: "a screen that names no Layout draws every surface with the newest on top",
			surfaces: []surface{
				claimedSurface("c-3", "media/film", 2, nil),
				claimedSurface("a-1", "media/idle", 0, nil),
				claimedSurface("b-2", "lobby/camera", 1, nil),
			},
			want: screenPlacement{
				placed: []placement{
					{surface: "a-1", region: "default", rect: LayoutRect{Width: 1, Height: 1}, stack: 0},
					{surface: "b-2", region: "default", rect: LayoutRect{Width: 1, Height: 1}, stack: 1},
					{surface: "c-3", region: "default", rect: LayoutRect{Width: 1, Height: 1}, stack: 2},
				},
				regions: []placedRegion{{name: "default", surface: "c-3"}},
			},
		},
		{
			name: "two regions take one surface each from two namespaces",
			surfaces: []surface{
				claimedSurface("a-1", "notices/board", 0, map[string]string{"panel": "notices"}),
				claimedSurface("b-2", "parking/camera", 1, map[string]string{"panel": "parking-lot"}),
			},
			layout: &LayoutSpec{Regions: []LayoutRegion{
				labelRegion("notices", noticesRect, map[string]string{"panel": "notices"}),
				{
					Name:     "corner",
					Rect:     cornerRect,
					Selector: LabelSelector{MatchLabels: map[string]string{"panel": "parking-lot"}},
					Transition: &LayoutTransition{
						Enter: LayoutTransitionHalf{Kind: "fade", Milliseconds: 300},
						Exit:  LayoutTransitionHalf{Kind: "fade", Milliseconds: 250},
					},
				},
			}},
			want: screenPlacement{
				placed: []placement{
					{surface: "a-1", region: "notices", rect: noticesRect, stack: 0},
					{surface: "b-2", region: "corner", rect: cornerRect, stack: 1,
						transition: LayoutTransition{
							Enter: LayoutTransitionHalf{Kind: "fade", Milliseconds: 300},
							Exit:  LayoutTransitionHalf{Kind: "fade", Milliseconds: 250},
						}},
				},
				regions: []placedRegion{{name: "notices", surface: "a-1"}, {name: "corner", surface: "b-2"}},
			},
		},
		{
			name: "the region written last draws over the region it overlaps",
			surfaces: []surface{
				claimedSurface("b-2", "parking/camera", 1, map[string]string{"panel": "parking-lot"}),
				claimedSurface("a-1", "notices/board", 0, map[string]string{"panel": "notices"}),
			},
			layout: &LayoutSpec{Regions: []LayoutRegion{
				labelRegion("notices", LayoutRect{Width: 1, Height: 1}, map[string]string{"panel": "notices"}),
				labelRegion("corner", cornerRect, map[string]string{"panel": "parking-lot"}),
			}},
			want: screenPlacement{
				placed: []placement{
					{surface: "a-1", region: "notices", rect: LayoutRect{Width: 1, Height: 1}, stack: 0},
					{surface: "b-2", region: "corner", rect: cornerRect, stack: 1},
				},
				regions: []placedRegion{{name: "notices", surface: "a-1"}, {name: "corner", surface: "b-2"}},
			},
		},
		{
			name: "a region shows the first claim to arrive and leaves a second claim unplaced",
			surfaces: []surface{
				claimedSurface("a-1", "notices/board", 0, map[string]string{"panel": "notices"}),
				claimedSurface("b-2", "notices/second", 1, map[string]string{"panel": "notices"}),
			},
			layout: &LayoutSpec{Regions: []LayoutRegion{
				labelRegion("notices", noticesRect, map[string]string{"panel": "notices"}),
			}},
			want: screenPlacement{
				placed: []placement{
					{surface: "a-1", region: "notices", rect: noticesRect, stack: 0},
				},
				unplaced: []string{"b-2"},
				regions:  []placedRegion{{name: "notices", surface: "a-1"}},
			},
		},
		{
			name: "a region shows every surface of its claim with the newest on top",
			surfaces: []surface{
				claimedSurface("b-3", "notices/second", 2, map[string]string{"panel": "notices"}),
				claimedSurface("a-2", "notices/board", 1, map[string]string{"panel": "notices"}),
				claimedSurface("a-1", "notices/board", 0, map[string]string{"panel": "notices"}),
			},
			layout: &LayoutSpec{Regions: []LayoutRegion{
				{
					Name:     "notices",
					Rect:     noticesRect,
					Selector: LabelSelector{MatchLabels: map[string]string{"panel": "notices"}},
					Transition: &LayoutTransition{
						Enter: LayoutTransitionHalf{Kind: "fade", Milliseconds: 600},
						Exit:  LayoutTransitionHalf{Kind: "fade", Milliseconds: 250},
					},
				},
			}},
			want: screenPlacement{
				placed: []placement{
					{surface: "a-1", region: "notices", rect: noticesRect, stack: 0,
						transition: LayoutTransition{
							Enter: LayoutTransitionHalf{Kind: "fade", Milliseconds: 600},
							Exit:  LayoutTransitionHalf{Kind: "fade", Milliseconds: 250},
						}},
					{surface: "a-2", region: "notices", rect: noticesRect, stack: 0,
						transition: LayoutTransition{
							Enter: LayoutTransitionHalf{Kind: "fade", Milliseconds: 600},
							Exit:  LayoutTransitionHalf{Kind: "fade", Milliseconds: 250},
						}},
				},
				unplaced: []string{"b-3"},
				regions:  []placedRegion{{name: "notices", surface: "a-2"}},
			},
		},
		{
			name:     "a region no surface matches shows empty",
			surfaces: []surface{claimedSurface("a-1", "notices/board", 0, map[string]string{"panel": "notices"})},
			layout: &LayoutSpec{Regions: []LayoutRegion{
				labelRegion("notices", noticesRect, map[string]string{"panel": "notices"}),
				labelRegion("corner", cornerRect, map[string]string{"panel": "parking-lot"}),
			}},
			want: screenPlacement{
				placed: []placement{
					{surface: "a-1", region: "notices", rect: noticesRect, stack: 0},
				},
				regions: []placedRegion{{name: "notices", surface: "a-1"}, {name: "corner", surface: "empty"}},
			},
		},
		{
			name: "matchExpressions decide which region a surface goes in",
			surfaces: []surface{
				claimedSurface("a-1", "media/film", 0, map[string]string{"media.liken.sh/focus": "true"}),
				claimedSurface("b-2", "media/idle", 1, map[string]string{"role": "idle"}),
			},
			layout: &LayoutSpec{Regions: []LayoutRegion{
				{Name: "focus", Rect: LayoutRect{Width: 1, Height: 1}, Selector: LabelSelector{
					MatchExpressions: []LabelSelectorRequirement{
						{Key: "media.liken.sh/focus", Operator: selectorIn, Values: []string{"true"}},
					},
				}},
				{Name: "corner", Rect: cornerRect, Selector: LabelSelector{
					MatchExpressions: []LabelSelectorRequirement{
						{Key: "media.liken.sh/focus", Operator: selectorDoesNotExist},
					},
				}},
			}},
			want: screenPlacement{
				placed: []placement{
					{surface: "a-1", region: "focus", rect: LayoutRect{Width: 1, Height: 1}, stack: 0},
					{surface: "b-2", region: "corner", rect: cornerRect, stack: 1},
				},
				regions: []placedRegion{{name: "focus", surface: "a-1"}, {name: "corner", surface: "b-2"}},
			},
		},
		{
			name:     "a surface with no claim matches no selector",
			surfaces: []surface{{id: "a-1", arrival: 0}},
			layout: &LayoutSpec{Regions: []LayoutRegion{
				{Name: "anything", Rect: LayoutRect{Width: 1, Height: 1}},
			}},
			want: screenPlacement{
				unplaced: []string{"a-1"},
				regions:  []placedRegion{{name: "anything", surface: "empty"}},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := placeSurfaces(test.surfaces, test.layout)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("placed\n%+v\nwant\n%+v", got, test.want)
			}
		})
	}
}

var claimLabels = map[string]string{"panel": "notices", "tier": "front"}

func TestMatchesSelector(t *testing.T) {
	for _, test := range []struct {
		name     string
		selector LabelSelector
		want     bool
	}{
		{
			name: "an empty selector matches any labels",
			want: true,
		},
		{
			name:     "matchLabels matches when every value is equal",
			selector: LabelSelector{MatchLabels: map[string]string{"panel": "notices", "tier": "front"}},
			want:     true,
		},
		{
			name:     "matchLabels fails on another value",
			selector: LabelSelector{MatchLabels: map[string]string{"panel": "parking-lot"}},
		},
		{
			name:     "matchLabels fails on a missing key",
			selector: LabelSelector{MatchLabels: map[string]string{"room": "lobby"}},
		},
		{
			name: "In matches a value in the list",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "panel", Operator: selectorIn, Values: []string{"notices", "menu"}},
			}},
			want: true,
		},
		{
			name: "In fails on a value the list leaves out",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "panel", Operator: selectorIn, Values: []string{"menu"}},
			}},
		},
		{
			name: "In fails on a missing key",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "room", Operator: selectorIn, Values: []string{"lobby"}},
			}},
		},
		{
			name: "NotIn matches a value the list leaves out",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "panel", Operator: selectorNotIn, Values: []string{"menu"}},
			}},
			want: true,
		},
		{
			name: "NotIn matches a missing key",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "room", Operator: selectorNotIn, Values: []string{"lobby"}},
			}},
			want: true,
		},
		{
			name: "NotIn fails on a value in the list",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "panel", Operator: selectorNotIn, Values: []string{"notices"}},
			}},
		},
		{
			name: "Exists matches a present key",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "tier", Operator: selectorExists},
			}},
			want: true,
		},
		{
			name: "Exists fails on a missing key",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "room", Operator: selectorExists},
			}},
		},
		{
			name: "DoesNotExist matches a missing key",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "room", Operator: selectorDoesNotExist},
			}},
			want: true,
		},
		{
			name: "DoesNotExist fails on a present key",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "tier", Operator: selectorDoesNotExist},
			}},
		},
		{
			name: "every requirement has to match",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "tier", Operator: selectorExists},
				{Key: "panel", Operator: selectorIn, Values: []string{"menu"}},
			}},
		},
		{
			name: "an unknown operator matches nothing",
			selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "tier", Operator: "Gt"},
			}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := matchesSelector(test.selector, claimLabels); got != test.want {
				t.Errorf("the selector matched %v, want %v", got, test.want)
			}
		})
	}
}
