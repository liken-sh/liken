package main

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// The module keeps its last committed layout while it is disconnected,
// and reports every surface again when it connects, so a pass does
// nothing while no module is connected.
func TestAPassWithNoModuleConnectedPlacesNothing(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	pass := newPlacementPass(fixture.client, "liken-1", servingSocket(t, t.TempDir()),
		newLayoutLink(t.TempDir()+"/layout.sock"), newClaimIndex(fixture.client), fixture.outputs)

	if err := pass.pass(); err != nil {
		t.Fatalf("running a pass with no module connected: %v", err)
	}
	if status := fixture.status(labMonitor()); status.Conditions != nil {
		t.Errorf("a pass with no module connected wrote the conditions %+v, want none", status.Conditions)
	}
}

// A failed read of a screen's Layout leaves the screen as it is. If
// the pass fell back to the default layout, one failed request would
// rearrange every surface.
func TestAFailedLayoutReadChangesNothing(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("front-desk", LayoutRegion{
		Name:     "notices",
		Rect:     LayoutRect{Width: 1, Height: 1},
		Selector: LabelSelector{},
	})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "front-desk"})
	board := fixture.prepareClaim("notices", "board", noticesClaimUID, "HDMI-A-1", "board-0")
	fixture.pod("notices", "board-0", map[string]string{"panel": "notices"})
	fixture.surface(board, 1920, 1080)
	fixture.refuse[LayoutsPath+"/front-desk"] = 1

	if err := fixture.pass.pass(); err == nil {
		t.Fatal("a pass whose Layout read was refused returned no error, want one")
	}
	if sent := fixture.sent(); len(sent) != 0 {
		t.Errorf("the pass sent %q, want nothing because it read no layout", sent)
	}
	if fixture.status(labMonitor()).Layout != nil {
		t.Errorf("the pass reported the layout %+v, want none because it read no layout", fixture.status(labMonitor()).Layout)
	}
}

// A region with no matching surface is reported as empty. A screen
// where no surface is placed gets no order, because the protocol has
// no order message with an empty list.
func TestARegionThatMatchesNothingIsReportedEmpty(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("front-desk", LayoutRegion{
		Name:     "notices",
		Rect:     LayoutRect{Width: 1, Height: 1},
		Selector: LabelSelector{MatchLabels: map[string]string{"panel": "notices"}},
	})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "front-desk"})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	fixture.surface(film, 1920, 1080)

	fixture.run()

	if sent := fixture.sent(); len(sent) != 0 {
		t.Errorf("the pass sent %q, want nothing because no region placed a surface", sent)
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

// No hide is sent for a surface that the module reports gone, because
// it is already off the screen.
func TestAGoneSurfaceLeavesTheOrderAndTheStatus(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	idle := fixture.prepareClaim("media", "idle", idleClaimUID, "HDMI-A-1", "idle-0")
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "idle-0", nil)
	fixture.pod("media", "film-0", nil)
	below := fixture.surface(idle, 1920, 1080)
	above := fixture.surface(film, 1920, 1080)
	fixture.run()
	fixture.sent()

	// The pod ended and its client removed its surface.
	fixture.module.send(fmt.Sprintf("surface-gone %d", above))
	waitUntil(t, "the surface is gone", func() bool {
		_, reported := fixture.link.state().Surfaces[above]
		return !reported
	})

	fixture.run()

	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("order HDMI-A-1 %d", below),
		"commit",
	})
	if surfaces := fixture.status(labMonitor()).Surfaces; len(surfaces) != 1 {
		t.Errorf("status lists %+v, want the surface that is left", surfaces)
	}
}

// A surface on a socket that no prepared claim holds comes from a
// client that kept drawing after its claim was unprepared. The pass
// returns an error that names the socket, and places the surface like
// one on the compositor's own socket, because the client is still on
// the screen.
func TestASurfaceOnASocketNoClaimHoldsIsReported(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	surface := fixture.surface(waylandSocketPrefix+"claim-that-ended", 1920, 1080)

	err := fixture.pass.pass()
	if err == nil {
		t.Fatal("a surface on an unknown socket returned no error, want one")
	}
	if !strings.Contains(err.Error(), "claim-that-ended") {
		t.Errorf("the error is %q, want it to name the socket", err)
	}
	assertSent(t, fixture.sent(), []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", surface),
		fmt.Sprintf("order HDMI-A-1 %d", surface),
		"commit",
	})
	if claim := fixture.status(labMonitor()).Surfaces[0].Claim; claim != "" {
		t.Errorf("the surface has the claim %q, want none", claim)
	}
}

// A failed read of a claim leaves its screen and the screen's status
// as they are. The surface would have no labels, match no region, and
// be hidden, so one failed request would remove a program from the
// screen.
func TestAFailedClaimReadChangesNothing(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.layout("media", LayoutRegion{
		Name:     "main",
		Rect:     LayoutRect{Width: 1, Height: 1},
		Selector: LabelSelector{MatchLabels: map[string]string{"media.liken.sh/focus": "true"}},
	})
	fixture.screen(labMonitor(), DisplaySpec{Layout: "media"})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "film-0", map[string]string{"media.liken.sh/focus": "true"})
	fixture.surface(film, 1920, 1080)
	fixture.run()
	fixture.sent()
	version := fixture.displays[labDisplayName()].Metadata.ResourceVersion

	fixture.refuse["/apis/resource.k8s.io/v1/namespaces/media/resourceclaims/film"] = 1
	if err := fixture.pass.pass(); err == nil {
		t.Fatal("a pass whose claim read was refused returned no error, want one")
	}

	if sent := fixture.sent(); len(sent) != 0 {
		t.Errorf("the pass sent %q, want nothing because the claim read was refused", sent)
	}
	if now := fixture.displays[labDisplayName()].Metadata.ResourceVersion; now != version {
		t.Errorf("the pass wrote the status (%s became %s), want it unchanged because the claim read was refused", version, now)
	}
}

// The pass finds each surface's claim from the prepared claims' CDI
// spec files. When one cannot be read, the pass ends before it sends
// anything, because every surface would match no region and be hidden.
func TestACDISpecThatCannotBeReadEndsThePass(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.surface(film, 1920, 1080)
	if err := os.WriteFile(cdiSpecPath("half-written"), []byte("{"), 0o644); err != nil {
		t.Fatalf("writing the CDI spec: %v", err)
	}

	if err := fixture.pass.pass(); err == nil {
		t.Fatal("a CDI spec that does not parse returned no error, want one")
	}
	if sent := fixture.sent(); len(sent) != 0 {
		t.Errorf("the pass sent %q, want nothing because it read no CDI spec", sent)
	}
}

// A commit that the module refuses changes nothing on the screen, so
// the next pass sends everything again.
func TestACommitTheModuleRefusesIsSentAgain(t *testing.T) {
	fixture := newPlacementBench(t,
		map[string]string{"commit": "the compositor is not running its event loop"})
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "film-0", nil)
	surface := fixture.surface(film, 1920, 1080)

	batch := []string{
		fmt.Sprintf("place %d HDMI-A-1 0 0 1920 1080 none 0", surface),
		fmt.Sprintf("order HDMI-A-1 %d", surface),
		"commit",
	}
	if err := fixture.pass.pass(); err == nil {
		t.Fatal("a pass whose commit the module refused returned no error, want one")
	}
	assertSent(t, fixture.sent(), batch)

	if err := fixture.pass.pass(); err == nil {
		t.Fatal("a pass whose commit the module refused returned no error, want one")
	}
	assertSent(t, fixture.sent(), batch)
}

func TestTheRectangleIsTheFractionOfTheOutputsLogicalSize(t *testing.T) {
	output := layoutOutput{Connector: "HDMI-A-1", Width: 1920, Height: 1080, Scale: 2}
	for _, test := range []struct {
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
		// 1.0/3 is not exact in floating point, so the pixels must
		// round to 640 and 360.
		{
			"a third of each side",
			LayoutRect{Left: 1.0 / 3, Top: 1.0 / 3, Width: 1.0 / 3, Height: 1.0 / 3},
			rect{X: 640, Y: 360, W: 640, H: 360},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := logicalRect(test.fraction, output); got != test.want {
				t.Errorf("logicalRect(%+v) = %+v, want %+v", test.fraction, got, test.want)
			}
		})
	}
}

func TestEnterTransition(t *testing.T) {
	for _, test := range []struct {
		name         string
		enter        LayoutTransitionHalf
		placed       bool
		want         string
		milliseconds int
	}{
		{"a region with no transition", LayoutTransitionHalf{Kind: transitionNone}, false, transitionNone, 0},
		{
			"a fade on a new surface",
			LayoutTransitionHalf{Kind: transitionFade, Milliseconds: 300}, false, transitionFade, 300,
		},
		{
			"a fade on a surface that moved",
			LayoutTransitionHalf{Kind: transitionFade, Milliseconds: 300}, true, transitionMove, 300,
		},
		// The module refuses a fade or a move of zero milliseconds, so a
		// region with a kind and no duration enters with no animation.
		{"a fade with no duration", LayoutTransitionHalf{Kind: transitionFade}, false, transitionNone, 0},
		{"a move with no duration", LayoutTransitionHalf{Kind: transitionFade}, true, transitionNone, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, milliseconds := enterTransition(test.enter, test.placed)
			if got != test.want || milliseconds != test.milliseconds {
				t.Errorf("enterTransition(%+v, %v) = %q, %d; want %q, %d",
					test.enter, test.placed, got, milliseconds, test.want, test.milliseconds)
			}
		})
	}
}

func TestExitTransition(t *testing.T) {
	for _, test := range []struct {
		name         string
		exit         LayoutTransitionHalf
		want         string
		milliseconds int
	}{
		{"a region with no exit", LayoutTransitionHalf{}, transitionNone, 0},
		{"a region that leaves at once", LayoutTransitionHalf{Kind: transitionNone}, transitionNone, 0},
		{
			"a region that fades out",
			LayoutTransitionHalf{Kind: transitionFade, Milliseconds: 250}, transitionFade, 250,
		},
		{"a fade with no duration", LayoutTransitionHalf{Kind: transitionFade}, transitionNone, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, milliseconds := exitTransition(test.exit)
			if got != test.want || milliseconds != test.milliseconds {
				t.Errorf("exitTransition(%+v) = %q, %d; want %q, %d",
					test.exit, got, milliseconds, test.want, test.milliseconds)
			}
		})
	}
}

func TestTheSurfaceIDHasTheClaimPrefixAndTheModulesID(t *testing.T) {
	for _, test := range []struct {
		claim string
		id    int
		want  string
	}{
		{"3f2a91be-1d4c-4a1e-9c77-2b8f0a5d6e31", 7, "3f2a91be-7"},
		{"short", 7, "short-7"},
		{"", 7, "shared-7"},
	} {
		if got := surfaceID(test.claim, test.id); got != test.want {
			t.Errorf("surfaceID(%q, %d) = %q, want %q", test.claim, test.id, got, test.want)
		}
	}
}

// While the compositor is down, a Display lists no surfaces, because
// nobody can see those programs.
func TestAScreenReportsNoSurfacesWhileTheCompositorIsDown(t *testing.T) {
	cases := []struct {
		name   string
		live   compositorLiveness
		reason string
	}{
		{
			name:   "a socket that refuses connections",
			live:   compositorLiveness{reason: CompositorDownReason, detail: "connect: connection refused"},
			reason: CompositorDownReason,
		},
		{
			name:   "a compositor that accepts and never responds",
			live:   compositorLiveness{reason: CompositorHungReason, detail: "i/o timeout"},
			reason: CompositorHungReason,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlacementFixture(t)
			fixture.screen(labMonitor(), DisplaySpec{})
			film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
			fixture.pod("media", "film-0", nil)
			fixture.surface(film, 1920, 1080)
			fixture.run()
			fixture.sent()
			if len(fixture.status(labMonitor()).Surfaces) != 1 {
				t.Fatal("the screen reports no surface while the compositor is up, want one")
			}

			fixture.probes(test.live)
			fixture.run()

			status := fixture.status(labMonitor())
			if status.Surfaces != nil {
				t.Errorf("the screen reports %+v while the compositor is down", status.Surfaces)
			}
			if status.Layout != nil {
				t.Errorf("the screen reports the layout %+v while the compositor is down", status.Layout)
			}
			condition := conditionByType(status, CompositorServingCondition)
			if condition.Status != conditionFalse || condition.Reason != test.reason {
				t.Errorf("%s is %s/%s, want %s/%s", CompositorServingCondition,
					condition.Status, condition.Reason, conditionFalse, test.reason)
			}
			if !strings.Contains(condition.Message, test.live.detail) {
				t.Errorf("the condition message %q does not contain the probe's error %q",
					condition.Message, test.live.detail)
			}
			if sent := fixture.sent(); len(sent) != 0 {
				t.Errorf("the pass sent %q while the compositor is down", sent)
			}
		})
	}
}

func TestTheCompositorConditionTimeChangesOnlyOnATransition(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "film-0", nil)
	fixture.surface(film, 1920, 1080)
	clock := time.Unix(0, 0).UTC()
	fixture.pass.now = func() time.Time { return clock }

	fixture.run()

	serving := conditionByType(fixture.status(labMonitor()), CompositorServingCondition)
	if serving.Status != conditionTrue || serving.Reason != CompositorServingReason {
		t.Fatalf("%s is %s/%s while the compositor is up",
			CompositorServingCondition, serving.Status, serving.Reason)
	}

	clock = clock.Add(time.Minute)
	fixture.run()
	if again := conditionByType(fixture.status(labMonitor()), CompositorServingCondition); again != serving {
		t.Errorf("a pass that changed nothing wrote %+v, want %+v", again, serving)
	}

	clock = clock.Add(time.Minute)
	fixture.probes(compositorLiveness{reason: CompositorHungReason, detail: "i/o timeout"})
	fixture.run()

	down := conditionByType(fixture.status(labMonitor()), CompositorServingCondition)
	if !down.LastTransitionTime.Equal(clock.UTC().Truncate(time.Second)) {
		t.Errorf("the condition changed at %s, want %s", down.LastTransitionTime, clock.UTC().Truncate(time.Second))
	}
}
