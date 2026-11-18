package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// A screen with no Layout draws every surface over the whole screen,
// with the newest on top.
func TestTheDefaultLayoutDrawsEverySurfaceOverTheWholeScreen(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	idle := fixture.prepareClaim("media", "idle", idleClaimUID, "HDMI-A-1", "idle-0")
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "idle-0", map[string]string{"media.liken.sh/focus": "false"})
	fixture.pod("media", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	first := fixture.surface(idle, 1920, 1080)
	second := fixture.surface(film, 1920, 1080)

	fixture.run()

	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", first),
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", second),
		// The order lists the bottom of the stack first, so the newest
		// surface is last.
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
		t.Fatalf("the surfaces are %+v, want both", status.Surfaces)
	}
	idleSurface := status.Surfaces[0]
	if idleSurface.ID != "idle-cla-1" || idleSurface.Claim != "media/idle" {
		t.Errorf("the first surface is %+v, want the idle claim's", idleSurface)
	}
	if !slices.Equal(idleSurface.Pods, []string{"media/idle-0"}) {
		t.Errorf("the first surface is held by %v, want the idle pod", idleSurface.Pods)
	}
	if idleSurface.Labels["media.liken.sh/focus"] != "false" || idleSurface.Region != defaultLayoutName {
		t.Errorf("the first surface is %+v, want the idle pod's labels in the default region", idleSurface)
	}
	if *idleSurface.Size != (SurfaceSize{Width: 1920, Height: 1080}) {
		t.Errorf("the first surface draws at %+v, want 1920 by 1080", *idleSurface.Size)
	}
	assertCondition(t, status, conditionTrue, DefaultLayoutReason)

	// A pass that decides the same as the last pass sends nothing to
	// the module and writes nothing to the API server.
	version := fixture.displays[labDisplayName()].Metadata.ResourceVersion
	fixture.run()
	if sent := fixture.sent(); len(sent) != 0 {
		t.Errorf("an unchanged pass sent %q", sent)
	}
	if now := fixture.displays[labDisplayName()].Metadata.ResourceVersion; now != version {
		t.Errorf("an unchanged pass wrote the status again (%s became %s)", version, now)
	}
}

// A Layout with two regions can draw claims from two namespaces on
// one screen.
func TestATwoRegionLayoutDrawsTwoNamespacesOnOneScreen(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("front-desk",
		LayoutRegion{
			Name:     "notices",
			Rect:     LayoutRect{Width: 0.7, Height: 1},
			Selector: LabelSelector{MatchLabels: map[string]string{"panel": "notices"}},
		},
		LayoutRegion{
			Name:     "lot",
			Rect:     LayoutRect{Left: 0.7, Width: 0.3, Height: 0.6},
			Selector: LabelSelector{MatchLabels: map[string]string{"panel": "parking-lot"}},
			Transition: &LayoutTransition{
				Enter: LayoutTransitionHalf{Kind: transitionFade, Milliseconds: 300},
			},
		})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "front-desk"})
	notices := fixture.prepareClaim("notices", "wall", noticesClaimUID, "HDMI-A-1", "notices-7d9f")
	lot := fixture.prepareClaim("parking", "camera", lotClaimUID, "HDMI-A-1", "camera-0")
	fixture.pod("notices", "notices-7d9f", map[string]string{"panel": "notices"})
	fixture.pod("parking", "camera-0", map[string]string{"panel": "parking-lot"})
	wall := fixture.surface(notices, 1344, 1080)
	camera := fixture.surface(lot, 576, 648)

	fixture.run()

	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1344 1080 none 0", wall),
		// The fade comes from the region's transition, and it is sent
		// with the first placement of the surface.
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
		t.Errorf("the surfaces are %+v, want one claim from each namespace", status.Surfaces)
	}
	assertCondition(t, status, conditionTrue, LayoutFoundReason)
}

// A Display whose Layout name matches no Layout is drawn with the
// default layout rather than left blank, and the status reports the
// name.
func TestALayoutNameThatResolvesToNothingDrawsTheDefault(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{Layout: "front-desk"})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "film-0", map[string]string{"panel": "notices"})
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
		t.Errorf("the condition message is %q, want one that names front-desk", conditionOf(status).Message)
	}
}

// A region shows one surface. A second surface that matches the same
// region is not placed and is reported with no region, so a person
// can see why that program is not visible.
func TestASecondMatchStaysOffTheScreenAndIsReported(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("front-desk", LayoutRegion{
		Name:     "notices",
		Rect:     LayoutRect{Width: 1, Height: 1},
		Selector: LabelSelector{MatchLabels: map[string]string{"panel": "notices"}},
	})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "front-desk"})
	first := fixture.prepareClaim("notices", "wall", noticesClaimUID, "HDMI-A-1", "notices-7d9f")
	second := fixture.prepareClaim("notices", "second", lotClaimUID, "HDMI-A-1", "second-0")
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

// A region selects surfaces by label, so moving a label moves the
// region. The surface that stops matching is hidden, and the surface
// that starts matching is placed in the region.
func TestALabelThatMovesRestacksTheScreen(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("media", LayoutRegion{
		Name:     "main",
		Rect:     LayoutRect{Width: 1, Height: 1},
		Selector: LabelSelector{MatchLabels: map[string]string{"media.liken.sh/focus": "true"}},
	})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "media"})
	idle := fixture.prepareClaim("media", "idle", idleClaimUID, "HDMI-A-1", "idle-0")
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "idle-0", map[string]string{"media.liken.sh/focus": "false"})
	fixture.pod("media", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	idleSurface := fixture.surface(idle, 1920, 1080)
	filmSurface := fixture.surface(film, 1920, 1080)

	fixture.run()
	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", filmSurface),
		fmt.Sprintf("order HDMI-A-1 %d", filmSurface),
		"commit",
	})

	// The film ended, so the label moves from the film pod to the idle
	// pod.
	fixture.pod("media", "film-0", map[string]string{"media.liken.sh/focus": "false"})
	fixture.pod("media", "idle-0", map[string]string{"media.liken.sh/focus": "true"})

	fixture.run()
	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", idleSurface),
		fmt.Sprintf("hide %d none 0", filmSurface),
		fmt.Sprintf("order HDMI-A-1 %d", idleSurface),
		"commit",
	})
	status := fixture.status(labMonitor())
	if status.Surfaces[0].Region != "main" || status.Surfaces[1].Region != "" {
		t.Errorf("the surfaces are %+v, want the idle surface in main and the film surface in no region",
			status.Surfaces)
	}
}

// A surface that stops matching a region fades out with the region's
// exit transition while its client keeps drawing. media-operator adds
// a label to the playback pod when the film ends, and its sidecar
// keeps the player running until the fade finishes.
func TestASurfaceThatStopsMatchingLeavesWithTheRegionsExit(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("theater",
		LayoutRegion{
			Name:     "room",
			Rect:     LayoutRect{Width: 1, Height: 1},
			Selector: LabelSelector{MatchLabels: map[string]string{"app": "media-browser"}},
		},
		LayoutRegion{
			Name: "film",
			Rect: LayoutRect{Width: 1, Height: 1},
			Selector: LabelSelector{MatchExpressions: []LabelSelectorRequirement{
				{Key: "media.liken.sh/component", Operator: selectorIn, Values: []string{"playback"}},
				{Key: "media.liken.sh/ending", Operator: selectorDoesNotExist},
			}},
			Transition: &LayoutTransition{
				Enter: LayoutTransitionHalf{Kind: transitionFade, Milliseconds: 600},
				Exit:  LayoutTransitionHalf{Kind: transitionFade, Milliseconds: 250},
			},
		})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "theater"})
	browser := fixture.prepareClaim("media", "browser", idleClaimUID, "HDMI-A-1", "browser-0")
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "browser-0", map[string]string{"app": "media-browser"})
	fixture.pod("media", "film-0", map[string]string{"media.liken.sh/component": "playback"})
	page := fixture.surface(browser, 1920, 1080)
	filmSurface := fixture.surface(film, 1920, 1080)

	fixture.run()
	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", page),
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 fade 600", filmSurface),
		fmt.Sprintf("order HDMI-A-1 %d %d", page, filmSurface),
		"commit",
	})

	// The film is ending. The new label stops the surface matching the
	// region, so the surface is hidden with the region's exit.
	fixture.pod("media", "film-0", map[string]string{
		"media.liken.sh/component": "playback",
		"media.liken.sh/ending":    "true",
	})

	fixture.run()
	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("hide %d fade 250", filmSurface),
		fmt.Sprintf("order HDMI-A-1 %d", page),
		"commit",
	})
	if region := fixture.status(labMonitor()).Surfaces[1].Region; region != "" {
		t.Errorf("the film surface is in %q, want no region", region)
	}

	// The player exits when the fade ends. The module reports the
	// surface as gone, and the pass sends no hide for it because it is
	// already off the screen.
	fixture.module.send(fmt.Sprintf("surface-gone %d", filmSurface))
	waitUntil(t, "the film surface goes", func() bool {
		_, held := fixture.link.state().Surfaces[filmSurface]
		return !held
	})

	fixture.run()
	if sent := fixture.sent(); len(sent) != 0 {
		t.Errorf("the pass sent %q for a surface the module reported gone", sent)
	}
}

// A mode change restarts the compositor, and the restarted compositor
// has none of the placements. After a new connection the module
// reports every surface again, and the pass sends every placement
// again.
func TestAReconnectSendsEveryPlacementAgain(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "film-0", map[string]string{"media.liken.sh/focus": "true"})
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
	// On a new connection the module reports every surface again.
	fixture.report(surface, film, 1920, 1080)

	fixture.run()
	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", surface),
		fmt.Sprintf("order HDMI-A-1 %d", surface),
		"commit",
	})
}

// When a monitor switches to another input, it disconnects and the
// compositor destroys its output. The module hides every surface on
// that output and clears its placement, while the clients stay
// connected. So when the output returns, the pass must send every
// placement again.
func TestAnOutputThatLeavesAndReturnsSendsEveryPlacementAgain(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	surface := fixture.surface(film, 1920, 1080)

	fixture.run()
	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", surface),
		fmt.Sprintf("order HDMI-A-1 %d", surface),
		"commit",
	})

	fixture.disconnect("HDMI-A-1")
	fixture.run()

	fixture.reconnect(primaryScreen())
	fixture.run()

	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", surface),
		fmt.Sprintf("order HDMI-A-1 %d", surface),
		"commit",
	})
}

// A surface on the compositor's own socket has no claim, so nothing
// says which screen it is for. It goes on the first connected output,
// where the default layout draws it, and no region selector matches
// it.
func TestASurfaceWithNoClaimGoesOnTheFirstConnectedOutput(t *testing.T) {
	fixture := newPlacementFixture(t, primaryScreen(), secondaryScreen())
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
		t.Errorf("the first surface is %+v, want shared-1 with no claim", status.Surfaces[0])
	}
}

// Each rectangle is computed from the logical size of its own output,
// which is the size the compositor lays out in, not the kernel mode.
func TestEachScreenIsDrawnInItsOwnLogicalPixels(t *testing.T) {
	fixture := newPlacementFixture(t, primaryScreen(), secondaryScreen())
	fixture.layout("half", LayoutRegion{
		Name:     "left",
		Rect:     LayoutRect{Width: 0.5, Height: 1},
		Selector: LabelSelector{MatchLabels: map[string]string{"panel": "notices"}},
	})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "half"})
	fixture.screen(portableMonitor(), DisplaySpec{Layout: "half"})
	wall := fixture.prepareClaim("notices", "wall", noticesClaimUID, "HDMI-A-1", "notices-7d9f")
	board := fixture.prepareClaim("notices", "board", lotClaimUID, "HDMI-A-2", "board-0")
	fixture.pod("notices", "notices-7d9f", map[string]string{"panel": "notices"})
	fixture.pod("notices", "board-0", map[string]string{"panel": "notices"})
	first := fixture.surface(wall, 960, 1080)
	second := fixture.surface(board, 640, 800)

	fixture.run()

	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 960 1080 none 0", first),
		fmt.Sprintf("order HDMI-A-1 %d", first),
		fmt.Sprintf("place %d HDMI-A-2 0 0 640 800 none 0", second),
		fmt.Sprintf("order HDMI-A-2 %d", second),
		// One commit covers both screens, so both change in the same
		// frame.
		"commit",
	})
}

// A surface's labels are the labels that every pod holding the claim
// has with the same value. Two pods can hold one claim and draw one
// surface, so a label that only one pod has must not place it.
func TestTheLabelsAreTheOnesEveryHolderShares(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	wall := fixture.prepareClaim("notices", "wall", noticesClaimUID, "HDMI-A-1", "wall-0", "wall-1")
	fixture.pod("notices", "wall-0", map[string]string{"panel": "notices", "pod": "first"})
	fixture.pod("notices", "wall-1", map[string]string{"panel": "notices", "pod": "second"})
	fixture.surface(wall, 1920, 1080)

	fixture.run()

	surface := fixture.status(labMonitor()).Surfaces[0]
	if !slices.Equal(surface.Pods, []string{"notices/wall-0", "notices/wall-1"}) {
		t.Errorf("the surface pods are %v, want both", surface.Pods)
	}
	if len(surface.Labels) != 1 || surface.Labels["panel"] != "notices" {
		t.Errorf("the surface labels are %v, want only the label that both pods have", surface.Labels)
	}
}

// The pass lists the pods of this node once, and reads each claim at
// most once, however many surfaces the compositor has.
func TestOnePassReadsEachClaimAndTheNodesPodsOnce(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	// One claim with two surfaces, such as a player that opens a
	// second window.
	fixture.surface(film, 1920, 1080)
	fixture.surface(film, 320, 240)

	fixture.run()

	if fixture.podLists != 1 {
		t.Errorf("the pass listed the pods %d times, want once", fixture.podLists)
	}
	if fixture.claimLists != 0 {
		t.Errorf("the pass listed the claims %d times, want none because every claim was prepared in this process",
			fixture.claimLists)
	}
}

// The claim index is filled when a claim is prepared, so an operator
// container that restarted while the compositor kept running has an
// empty index. The CDI specs on disk name the claims this node
// prepared, and one listing of claims returns their namespaces and
// names.
func TestAnOperatorThatRestartedReadsTheClaimsFromOneListing(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	// The index of the process that prepared the claim is gone.
	fixture.pass.claims = newClaimIndex(fixture.client)
	fixture.surface(film, 1920, 1080)

	fixture.run()

	if claim := fixture.status(labMonitor()).Surfaces[0].Claim; claim != "media/film" {
		t.Errorf("the surface claim is %q, want media/film", claim)
	}
	if fixture.claimLists != 1 {
		t.Errorf("the pass listed the claims %d times, want once", fixture.claimLists)
	}
	// The index keeps the result, so a second surface on the same
	// claim needs no listing.
	fixture.surface(film, 320, 240)
	fixture.run()
	if fixture.claimLists != 1 {
		t.Errorf("the pass listed the claims %d times, want still once after the second surface", fixture.claimLists)
	}
}

// A pod that is missing from the node's pod listing is read by name.
// This happens when the pod was created after the listing.
func TestAHolderTheListingLeavesOutIsReadByName(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	wall := fixture.prepareClaim("notices", "wall", noticesClaimUID, "HDMI-A-1", "wall-0")
	fixture.pod("notices", "wall-0", map[string]string{"panel": "notices"})
	fixture.elsewhere["notices/wall-0"] = true
	fixture.surface(wall, 1920, 1080)

	fixture.run()

	if fixture.podGets != 1 {
		t.Errorf("the pass read %d pods by name, want 1 for the pod the listing left out", fixture.podGets)
	}
	if labels := fixture.status(labMonitor()).Surfaces[0].Labels; labels["panel"] != "notices" {
		t.Errorf("the surface labels are %v, want the label of the pod read by name", labels)
	}
}

// When the pod that held a claim no longer exists, the surface has no
// labels, and status still reports the claim.
func TestAHolderThatIsGoneCarriesNoLabels(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	wall := fixture.prepareClaim("notices", "wall", noticesClaimUID, "HDMI-A-1", "wall-0")
	fixture.surface(wall, 1920, 1080)

	fixture.run()

	surface := fixture.status(labMonitor()).Surfaces[0]
	if surface.Claim != "notices/wall" || len(surface.Pods) != 0 || len(surface.Labels) != 0 {
		t.Errorf("the surface is %+v, want the claim and no holder", surface)
	}
}
