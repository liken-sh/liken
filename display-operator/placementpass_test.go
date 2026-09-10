package main

// These tests drive one pass over the screens of the bench: what the
// module was asked to place, in what order, and what the Display
// reports about it.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The living room under no Layout at all, which is what every screen
// showed under kiosk-shell: the idle client, then the film over it.
func TestTheDefaultLayoutDrawsEverySurfaceOverTheWholeScreen(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	idle := fixture.hold("living-room", "idle", idleClaimUID, "HDMI-A-1", "idle-0")
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "idle-0", map[string]string{"media.liken.sh/focus": "false"})
	fixture.pod("living-room", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	first := fixture.surface(idle, 1920, 1080)
	second := fixture.surface(film, 1920, 1080)

	fixture.run()

	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", first),
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", second),
		// The newest surface is last in the order, which is the top of
		// the stack.
		fmt.Sprintf("order HDMI-A-1 %d %d", first, second),
		"commit",
	})

	status := fixture.status(labMonitor())
	if status.Layout.Name != defaultLayoutName {
		t.Errorf("the screen shows %q, want the default", status.Layout.Name)
	}
	want := []DisplayRegion{{Name: defaultLayoutName, Surface: "film-cla-2"}}
	if !slices.Equal(status.Layout.Regions, want) {
		t.Errorf("the regions are %+v, want %+v", status.Layout.Regions, want)
	}
	if len(status.Surfaces) != 2 {
		t.Fatalf("status holds %+v, want both surfaces", status.Surfaces)
	}
	idleSurface := status.Surfaces[0]
	if idleSurface.ID != "idle-cla-1" || idleSurface.Claim != "living-room/idle" {
		t.Errorf("the first surface is %+v, want the idle claim's", idleSurface)
	}
	if !slices.Equal(idleSurface.Pods, []string{"living-room/idle-0"}) {
		t.Errorf("the first surface is held by %v, want the idle pod", idleSurface.Pods)
	}
	if idleSurface.Labels["media.liken.sh/focus"] != "false" || idleSurface.Region != defaultLayoutName {
		t.Errorf("the first surface is %+v, want the idle pod's labels in the default region", idleSurface)
	}
	if *idleSurface.Size != (SurfaceSize{Width: 1920, Height: 1080}) {
		t.Errorf("the first surface draws at %+v, want 1920 by 1080", *idleSurface.Size)
	}
	assertCondition(t, status, conditionTrue, DefaultLayoutReason)

	// A pass that decides what the last one decided sends nothing at
	// all, so a screen that holds still costs the compositor no
	// commit and the API server no write.
	version := fixture.displays[labDisplayName()].Metadata.ResourceVersion
	fixture.run()
	if sent := fixture.sent(); len(sent) != 0 {
		t.Errorf("an unchanged pass sent %q", sent)
	}
	if now := fixture.displays[labDisplayName()].Metadata.ResourceVersion; now != version {
		t.Errorf("an unchanged pass wrote the status again (%s became %s)", version, now)
	}
}

// The front desk of the plan: two regions, two claims, two
// namespaces, one screen.
func TestATwoRegionLayoutDrawsTwoNamespacesOnOneScreen(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("front-desk",
		LayoutRegion{
			Name:     "notices",
			Rect:     LayoutRect{Width: 0.7, Height: 1},
			Selector: LabelSelector{MatchLabels: map[string]string{"panel": "notices"}},
		},
		LayoutRegion{
			Name:       "lot",
			Rect:       LayoutRect{Left: 0.7, Width: 0.3, Height: 0.6},
			Selector:   LabelSelector{MatchLabels: map[string]string{"panel": "parking-lot"}},
			Transition: &LayoutTransition{Kind: transitionFade, Milliseconds: 300},
		})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "front-desk"})
	notices := fixture.hold("notices", "wall", noticesClaimUID, "HDMI-A-1", "notices-7d9f")
	lot := fixture.hold("parking", "camera", lotClaimUID, "HDMI-A-1", "camera-0")
	fixture.pod("notices", "notices-7d9f", map[string]string{"panel": "notices"})
	fixture.pod("parking", "camera-0", map[string]string{"panel": "parking-lot"})
	wall := fixture.surface(notices, 1344, 1080)
	camera := fixture.surface(lot, 576, 648)

	fixture.run()

	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1344 1080 none 0", wall),
		// The fade is the region's own, and it runs on the placement
		// that first shows the surface.
		fmt.Sprintf("place %d HDMI-A-1 1344 0 576 648 fade 300", camera),
		fmt.Sprintf("order HDMI-A-1 %d %d", wall, camera),
		"commit",
	})

	status := fixture.status(labMonitor())
	if status.Layout.Name != "front-desk" {
		t.Errorf("the screen shows %q, want front-desk", status.Layout.Name)
	}
	want := []DisplayRegion{
		{Name: "notices", Surface: "note-cla-1"},
		{Name: "lot", Surface: "lot0-cla-2"},
	}
	if !slices.Equal(status.Layout.Regions, want) {
		t.Errorf("the regions are %+v, want %+v", status.Layout.Regions, want)
	}
	if status.Surfaces[0].Claim != "notices/wall" || status.Surfaces[1].Claim != "parking/camera" {
		t.Errorf("status holds %+v, want one claim from each namespace", status.Surfaces)
	}
	assertCondition(t, status, conditionTrue, LayoutFoundReason)
}

// A Display that names a Layout the cluster does not hold shows the
// default and reports the name, because a screen that went dark
// would be a worse answer than a screen that shows what it showed.
func TestALayoutNameThatResolvesToNothingDrawsTheDefault(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{Layout: "front-desk"})
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "film-0", map[string]string{"panel": "notices"})
	surface := fixture.surface(film, 1920, 1080)

	fixture.run()

	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", surface),
		fmt.Sprintf("order HDMI-A-1 %d", surface),
		"commit",
	})
	status := fixture.status(labMonitor())
	if status.Layout.Name != defaultLayoutName {
		t.Errorf("the screen shows %q, want the default", status.Layout.Name)
	}
	assertCondition(t, status, conditionFalse, LayoutNotFoundReason)
	if !strings.Contains(conditionOf(status).Message, "front-desk") {
		t.Errorf("the condition says %q, and it does not name the layout", conditionOf(status).Message)
	}
}

// A region shows one surface. The second surface a region matches
// stays off the screen and is reported with no region, which is what
// a person reads when a program draws nothing they can see.
func TestASecondMatchStaysOffTheScreenAndIsReported(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("front-desk", LayoutRegion{
		Name:     "notices",
		Rect:     LayoutRect{Width: 1, Height: 1},
		Selector: LabelSelector{MatchLabels: map[string]string{"panel": "notices"}},
	})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "front-desk"})
	first := fixture.hold("notices", "wall", noticesClaimUID, "HDMI-A-1", "notices-7d9f")
	second := fixture.hold("notices", "second", lotClaimUID, "HDMI-A-1", "second-0")
	fixture.pod("notices", "notices-7d9f", map[string]string{"panel": "notices"})
	fixture.pod("notices", "second-0", map[string]string{"panel": "notices"})
	shown := fixture.surface(first, 1920, 1080)
	fixture.surface(second, 1920, 1080)

	fixture.run()

	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", shown),
		fmt.Sprintf("order HDMI-A-1 %d", shown),
		"commit",
	})
	status := fixture.status(labMonitor())
	if status.Surfaces[1].Region != "" {
		t.Errorf("the second surface is in %q, want no region", status.Surfaces[1].Region)
	}
	if status.Surfaces[1].Claim != "notices/second" {
		t.Errorf("the second surface is %+v, want the second claim", status.Surfaces[1])
	}
}

// The label is the binding, so a label that moves moves the screen.
// The surface that no longer matches is hidden, and the one that now
// matches takes the region.
func TestALabelThatMovesRestacksTheScreen(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("living-room", LayoutRegion{
		Name:     "main",
		Rect:     LayoutRect{Width: 1, Height: 1},
		Selector: LabelSelector{MatchLabels: map[string]string{"media.liken.sh/focus": "true"}},
	})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "living-room"})
	idle := fixture.hold("living-room", "idle", idleClaimUID, "HDMI-A-1", "idle-0")
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "idle-0", map[string]string{"media.liken.sh/focus": "false"})
	fixture.pod("living-room", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	idleSurface := fixture.surface(idle, 1920, 1080)
	filmSurface := fixture.surface(film, 1920, 1080)

	fixture.run()
	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", filmSurface),
		fmt.Sprintf("order HDMI-A-1 %d", filmSurface),
		"commit",
	})

	// The Play ended, so the film pod drops the label and the idle pod
	// carries it.
	fixture.pod("living-room", "film-0", map[string]string{"media.liken.sh/focus": "false"})
	fixture.pod("living-room", "idle-0", map[string]string{"media.liken.sh/focus": "true"})

	fixture.run()
	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", idleSurface),
		fmt.Sprintf("hide %d", filmSurface),
		fmt.Sprintf("order HDMI-A-1 %d", idleSurface),
		"commit",
	})
	status := fixture.status(labMonitor())
	if status.Surfaces[0].Region != "main" || status.Surfaces[1].Region != "" {
		t.Errorf("status holds %+v, want the idle surface in main and the film surface nowhere",
			status.Surfaces)
	}
}

// The compositor restarts on every mode change, and the module holds
// no layout of its own. A new connection reports every surface again,
// and the pass states every placement again, because what it
// remembered sending went with the compositor.
func TestAReconnectStatesEveryPlacementAgain(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	surface := fixture.surface(film, 1920, 1080)

	fixture.run()
	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", surface),
		fmt.Sprintf("order HDMI-A-1 %d", surface),
		"commit",
	})

	fixture.module.drop()
	fixture.module.accepted()
	waitUntil(t, "the link serves the second connection", fixture.link.moduleServing)
	// The module re-sends one surface line for every surface it holds
	// on a new connection, and the ids it reports are the ids of the
	// compositor it runs in.
	fixture.report(surface, film, 1920, 1080)

	fixture.run()
	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", surface),
		fmt.Sprintf("order HDMI-A-1 %d", surface),
		"commit",
	})
}

// A surface on the compositor's own socket belongs to no claim, so
// nothing says which screen it was meant for and it goes on the first
// connected output. The default layout shows it, and no selector
// matches it.
func TestASurfaceWithNoClaimGoesOnTheFirstConnectedOutput(t *testing.T) {
	fixture := newPlacementFixture(t, livingRoomScreen(), deskScreen())
	fixture.screen(labMonitor(), DisplaySpec{})
	surface := fixture.surface(socketName, 800, 600)

	fixture.run()

	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", surface),
		fmt.Sprintf("order HDMI-A-1 %d", surface),
		"commit",
	})
	status := fixture.status(labMonitor())
	if status.Surfaces[0].ID != "shared-1" || status.Surfaces[0].Claim != "" {
		t.Errorf("status holds %+v, want a shared surface with no claim", status.Surfaces[0])
	}
}

// Each rectangle is computed against its own output's logical size,
// which is the size the compositor lays out in and never the kernel
// mode.
func TestEachScreenIsDrawnInItsOwnLogicalPixels(t *testing.T) {
	fixture := newPlacementFixture(t, livingRoomScreen(), deskScreen())
	fixture.layout("half", LayoutRegion{
		Name:     "left",
		Rect:     LayoutRect{Width: 0.5, Height: 1},
		Selector: LabelSelector{MatchLabels: map[string]string{"panel": "notices"}},
	})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "half"})
	fixture.screen(portableMonitor(), DisplaySpec{Layout: "half"})
	wall := fixture.hold("notices", "wall", noticesClaimUID, "HDMI-A-1", "notices-7d9f")
	desk := fixture.hold("notices", "desk", lotClaimUID, "HDMI-A-2", "desk-0")
	fixture.pod("notices", "notices-7d9f", map[string]string{"panel": "notices"})
	fixture.pod("notices", "desk-0", map[string]string{"panel": "notices"})
	first := fixture.surface(wall, 960, 1080)
	second := fixture.surface(desk, 640, 800)

	fixture.run()

	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 960 1080 none 0", first),
		fmt.Sprintf("order HDMI-A-1 %d", first),
		fmt.Sprintf("place %d HDMI-A-2 0 0 640 800 none 0", second),
		fmt.Sprintf("order HDMI-A-2 %d", second),
		// One commit covers both screens, so the two land in one frame.
		"commit",
	})
}

// The labels are the ones every holder carries with the same value. A
// claim two pods hold draws one surface, and a label only one of them
// carries must not place it.
func TestTheLabelsAreTheOnesEveryHolderShares(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	wall := fixture.hold("notices", "wall", noticesClaimUID, "HDMI-A-1", "wall-0", "wall-1")
	fixture.pod("notices", "wall-0", map[string]string{"panel": "notices", "pod": "first"})
	fixture.pod("notices", "wall-1", map[string]string{"panel": "notices", "pod": "second"})
	fixture.surface(wall, 1920, 1080)

	fixture.run()

	surface := fixture.status(labMonitor()).Surfaces[0]
	if !slices.Equal(surface.Pods, []string{"notices/wall-0", "notices/wall-1"}) {
		t.Errorf("the surface is held by %v, want both pods", surface.Pods)
	}
	if len(surface.Labels) != 1 || surface.Labels["panel"] != "notices" {
		t.Errorf("the surface carries %v, want the one label both pods share", surface.Labels)
	}
}

// The pass reads the API server once for each claim and once for the
// pods of this node, however many surfaces the compositor holds.
func TestOnePassReadsEachClaimAndTheNodesPodsOnce(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	// One claim, two surfaces: a player that mapped a second window.
	fixture.surface(film, 1920, 1080)
	fixture.surface(film, 320, 240)

	fixture.run()

	if fixture.podLists != 1 {
		t.Errorf("the pass listed the pods %d times, want once", fixture.podLists)
	}
	if fixture.claimLists != 0 {
		t.Errorf("the pass listed the claims %d times, and every claim was prepared in this process",
			fixture.claimLists)
	}
}

// The index is filled at prepare time, and an operator container that
// restarted under a running compositor has none. The specs on disk
// name the claims this node prepared, and one listing carries their
// namespaces and names.
func TestAnOperatorThatRestartedReadsTheClaimsFromOneListing(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	// The index of the process that prepared the claim went with it.
	fixture.pass.claims = newClaimIndex(fixture.client)
	fixture.surface(film, 1920, 1080)

	fixture.run()

	if claim := fixture.status(labMonitor()).Surfaces[0].Claim; claim != "living-room/film" {
		t.Errorf("the surface holds %q, want living-room/film", claim)
	}
	if fixture.claimLists != 1 {
		t.Errorf("the pass listed the claims %d times, want once", fixture.claimLists)
	}
	// The answer is remembered, so a second surface on the same claim
	// costs no listing.
	fixture.surface(film, 320, 240)
	fixture.run()
	if fixture.claimLists != 1 {
		t.Errorf("the pass listed the claims %d times, want the one listing", fixture.claimLists)
	}
}

// A holder the node listing leaves out is read by name, which is a
// pod that arrived after the listing was taken.
func TestAHolderTheListingLeavesOutIsReadByName(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	wall := fixture.hold("notices", "wall", noticesClaimUID, "HDMI-A-1", "wall-0")
	fixture.pod("notices", "wall-0", map[string]string{"panel": "notices"})
	fixture.elsewhere["notices/wall-0"] = true
	fixture.surface(wall, 1920, 1080)

	fixture.run()

	if fixture.podGets != 1 {
		t.Errorf("the pass read %d pods by name, want the one the listing left out", fixture.podGets)
	}
	if labels := fixture.status(labMonitor()).Surfaces[0].Labels; labels["panel"] != "notices" {
		t.Errorf("the surface carries %v, want the label of the pod read by name", labels)
	}
}

// A holder that is gone carries no labels, and the claim it held is
// still what status reports.
func TestAHolderThatIsGoneCarriesNoLabels(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	wall := fixture.hold("notices", "wall", noticesClaimUID, "HDMI-A-1", "wall-0")
	fixture.surface(wall, 1920, 1080)

	fixture.run()

	surface := fixture.status(labMonitor()).Surfaces[0]
	if surface.Claim != "notices/wall" || len(surface.Pods) != 0 || len(surface.Labels) != 0 {
		t.Errorf("the surface is %+v, want the claim and no holder", surface)
	}
}
