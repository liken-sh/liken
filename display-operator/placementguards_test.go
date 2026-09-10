package main

// These tests cover what one pass refuses to do: a screen it could
// not read whole, a record or a claim the pass could not read, and a
// commit the module refused. Each of them leaves the screen as it is.

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// A pass with no module serving does nothing. The module keeps the
// last layout it committed while nothing is connected to it, and the
// connection that follows reports every surface again.
func TestAPassWithNoModuleServingPlacesNothing(t *testing.T) {
	fixture := newPlacementFixture(t)
	pass := newPlacementPass(fixture.client, "liken-1",
		newLayoutLink(t.TempDir()+"/layout.sock"), newClaimIndex(fixture.client), fixture.outputs)

	if err := pass.pass(); err != nil {
		t.Fatalf("a pass with no module serving failed: %v", err)
	}
}

// A screen keeps the arrangement it holds while the API server cannot
// answer for its Layout. A pass that read the default there would
// rearrange every surface because one request failed.
func TestALayoutTheAPIServerCannotAnswerForChangesNothing(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("front-desk", LayoutRegion{
		Name:     "notices",
		Rect:     LayoutRect{Width: 1, Height: 1},
		Selector: LabelSelector{},
	})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "front-desk"})
	wall := fixture.hold("notices", "wall", noticesClaimUID, "HDMI-A-1", "wall-0")
	fixture.pod("notices", "wall-0", map[string]string{"panel": "notices"})
	fixture.surface(wall, 1920, 1080)
	fixture.refuse[LayoutsPath+"/front-desk"] = 1

	if err := fixture.pass.pass(); err == nil {
		t.Fatal("a pass whose Layout read failed reported no error")
	}
	if sent := fixture.sent(); len(sent) != 0 {
		t.Errorf("the pass sent %q, and it read no layout", sent)
	}
	if fixture.status(labMonitor()).Layout != nil {
		t.Errorf("the pass reported %+v, and it read no layout", fixture.status(labMonitor()).Layout)
	}
}

// A region with no matching surface is empty and is reported, and a
// screen with nothing to show sends no order: the protocol has no
// line for an order that names no surface.
func TestARegionThatMatchesNothingIsReportedEmpty(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("front-desk", LayoutRegion{
		Name:     "notices",
		Rect:     LayoutRect{Width: 1, Height: 1},
		Selector: LabelSelector{MatchLabels: map[string]string{"panel": "notices"}},
	})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "front-desk"})
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	fixture.surface(film, 1920, 1080)

	fixture.run()

	if sent := fixture.sent(); len(sent) != 0 {
		t.Errorf("the pass sent %q, and no region took a surface", sent)
	}
	status := fixture.status(labMonitor())
	want := []DisplayRegion{{Name: "notices", Surface: emptyRegion}}
	if !slices.Equal(status.Layout.Regions, want) {
		t.Errorf("the regions are %+v, want %+v", status.Layout.Regions, want)
	}
	if status.Surfaces[0].Region != "" {
		t.Errorf("the surface is in %q, want no region", status.Surfaces[0].Region)
	}
}

// A surface that goes takes its place in the order with it. Nothing
// is hidden, because the surface the module reported gone is already
// off the screen.
func TestASurfaceThatGoesLeavesTheOrderAndTheStatus(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	idle := fixture.hold("living-room", "idle", idleClaimUID, "HDMI-A-1", "idle-0")
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "idle-0", nil)
	fixture.pod("living-room", "film-0", nil)
	below := fixture.surface(idle, 1920, 1080)
	above := fixture.surface(film, 1920, 1080)
	fixture.run()
	fixture.sent()

	// The film ended and its pod went, so the client took its surface
	// with it.
	fixture.module.send(fmt.Sprintf("surface-gone %d", above))
	waitUntil(t, "the surface goes", func() bool {
		_, held := fixture.link.state().Surfaces[above]
		return !held
	})

	fixture.run()

	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("order HDMI-A-1 %d", below),
		"commit",
	})
	if surfaces := fixture.status(labMonitor()).Surfaces; len(surfaces) != 1 {
		t.Errorf("status holds %+v, want the surface that is left", surfaces)
	}
}

// A surface on a socket no prepared claim holds belongs to no claim.
// The claim was given back while its client kept drawing, and the
// pass reports the socket rather than dropping the surface: a client
// that is still connected is still on the screen.
func TestASurfaceOnASocketNoClaimHoldsIsReported(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	surface := fixture.surface(waylandSocketPrefix+"claim-that-ended", 1920, 1080)

	err := fixture.pass.pass()
	if err == nil {
		t.Fatal("a surface on an unknown socket reported no error")
	}
	if !strings.Contains(err.Error(), "claim-that-ended") {
		t.Errorf("the error is %q, and it does not name the socket", err)
	}
	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", surface),
		fmt.Sprintf("order HDMI-A-1 %d", surface),
		"commit",
	})
	if claim := fixture.status(labMonitor()).Surfaces[0].Claim; claim != "" {
		t.Errorf("the surface holds %q, want no claim", claim)
	}
}

// A screen is rearranged from a whole reading and no other. A claim
// the API server cannot answer for would leave its surface with no
// labels, and one request that failed must not take a program off
// the screen.
func TestAClaimTheAPIServerCannotAnswerForChangesNothing(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("living-room", LayoutRegion{
		Name:     "main",
		Rect:     LayoutRect{Width: 1, Height: 1},
		Selector: LabelSelector{MatchLabels: map[string]string{"media.liken.sh/focus": "true"}},
	})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "living-room"})
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	fixture.surface(film, 1920, 1080)
	fixture.run()
	fixture.sent()
	version := fixture.displays[labDisplayName()].Metadata.ResourceVersion

	fixture.refuse["/apis/resource.k8s.io/v1/namespaces/living-room/resourceclaims/film"] = 1
	if err := fixture.pass.pass(); err == nil {
		t.Fatal("a claim read that failed reported no error")
	}

	if sent := fixture.sent(); len(sent) != 0 {
		t.Errorf("the pass sent %q, and it could not read the claim", sent)
	}
	if now := fixture.displays[labDisplayName()].Metadata.ResourceVersion; now != version {
		t.Errorf("the pass wrote the status (%s became %s), and it could not read the claim", version, now)
	}
}

// The record of which claim holds which socket is what turns a
// surface into a claim. A read of it that failed ends the pass, for
// the reason a claim that cannot be read does.
func TestARecordThatCannotBeReadEndsThePass(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.surface(film, 1920, 1080)
	if err := os.WriteFile(cdiSpecPath("half-written"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := fixture.pass.pass(); err == nil {
		t.Fatal("a record that will not parse reported no error")
	}
	if sent := fixture.sent(); len(sent) != 0 {
		t.Errorf("the pass sent %q, and it read no record", sent)
	}
}

// A commit the module refuses leaves nothing on the screen, so the
// pass forgets what it sent and the next pass states all of it again.
func TestACommitTheModuleRefusesIsStatedAgain(t *testing.T) {
	fixture := newPlacementBench(t,
		map[string]string{"commit": "the compositor is not running its event loop"})
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "film-0", nil)
	surface := fixture.surface(film, 1920, 1080)

	batch := []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", surface),
		fmt.Sprintf("order HDMI-A-1 %d", surface),
		"commit",
	}
	if err := fixture.pass.pass(); err == nil {
		t.Fatal("a refused commit reported no error")
	}
	assertSent(t, fixture.sent(), batch)

	if err := fixture.pass.pass(); err == nil {
		t.Fatal("a refused commit reported no error")
	}
	assertSent(t, fixture.sent(), batch)
}

func TestTheRectangleIsTheFractionOfTheOutputsLogicalSize(t *testing.T) {
	output := layoutOutput{Connector: "HDMI-A-1", Width: 1920, Height: 1080, Scale: 2}
	for _, drill := range []struct {
		name     string
		fraction LayoutRect
		want     rect
	}{
		{"the whole screen", LayoutRect{Width: 1, Height: 1}, rect{W: 1920, H: 1080}},
		{"the left seven tenths", LayoutRect{Width: 0.7, Height: 1}, rect{W: 1344, H: 1080}},
		{
			"the corner of it",
			LayoutRect{Left: 0.7, Width: 0.3, Height: 0.6},
			rect{X: 1344, W: 576, H: 648},
		},
		// A third of 1920 is 640, and a third of 1080 is 360: both
		// round from a fraction no decimal states exactly.
		{
			"a third of each side",
			LayoutRect{Left: 1.0 / 3, Top: 1.0 / 3, Width: 1.0 / 3, Height: 1.0 / 3},
			rect{X: 640, Y: 360, W: 640, H: 360},
		},
	} {
		t.Run(drill.name, func(t *testing.T) {
			if got := logicalRect(drill.fraction, output); got != drill.want {
				t.Errorf("logicalRect(%+v) = %+v, want %+v", drill.fraction, got, drill.want)
			}
		})
	}
}

func TestTheTransitionOnePlacementStates(t *testing.T) {
	for _, drill := range []struct {
		name         string
		transition   LayoutTransition
		placed       bool
		want         string
		milliseconds int
	}{
		{"a region with no transition", LayoutTransition{Kind: transitionNone}, false, transitionNone, 0},
		{
			"a fade on a new surface",
			LayoutTransition{Kind: transitionFade, Milliseconds: 300}, false, transitionFade, 300,
		},
		{
			"a fade on a surface that moved",
			LayoutTransition{Kind: transitionFade, Milliseconds: 300}, true, transitionMove, 300,
		},
		// The module refuses a fade or a move over zero milliseconds,
		// and a region that states a kind and no duration means an
		// entrance with no animation.
		{"a fade with no duration", LayoutTransition{Kind: transitionFade}, false, transitionNone, 0},
		{"a move with no duration", LayoutTransition{Kind: transitionFade}, true, transitionNone, 0},
	} {
		t.Run(drill.name, func(t *testing.T) {
			got, milliseconds := wireTransition(drill.transition, drill.placed)
			if got != drill.want || milliseconds != drill.milliseconds {
				t.Errorf("wireTransition(%+v, %v) = %q, %d; want %q, %d",
					drill.transition, drill.placed, got, milliseconds, drill.want, drill.milliseconds)
			}
		})
	}
}

func TestTheSurfaceIDCarriesTheClaimAndTheModulesID(t *testing.T) {
	for _, drill := range []struct {
		claim string
		id    int
		want  string
	}{
		{"3f2a91be-1d4c-4a1e-9c77-2b8f0a5d6e31", 7, "3f2a91be-7"},
		{"short", 7, "short-7"},
		{"", 7, "shared-7"},
	} {
		if got := surfaceID(drill.claim, drill.id); got != drill.want {
			t.Errorf("surfaceID(%q, %d) = %q, want %q", drill.claim, drill.id, got, drill.want)
		}
	}
}
