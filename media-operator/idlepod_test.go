package main

// These tests cover the standing idle pod a Player becomes: one claim
// that holds the shared draw device and the render node, one pod that
// runs the idle client, and the reconcile
// that creates each once and builds nothing when the idle screen is off.

import (
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// One Player that drives a screen, with a display selector the idle claim
// copies and a render node the idle client draws through.
func standingIdlePlayer() *Player {
	return &Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house", UID: "player-uid"},
		Spec: PlayerSpec{
			Display: &PlayerDevice{
				Class:    "display-output",
				Selector: `device.attributes["monitor.liken.sh"].id == "DP-1"`,
			},
			Render: &PlayerDevice{Class: "gpu-render"},
		},
	}
}

// containerEnv is one container's environment as a map, for a test that
// reads the whole of it rather than one variable.
func containerEnv(container Container) map[string]string {
	env := map[string]string{}
	for _, entry := range container.Env {
		env[entry.Name] = entry.Value
	}
	return env
}

// testIdleImage is the client IDLE_IMAGE names, which every idle
// container runs where no tier of the idle policy names an image.
const testIdleImage = "ghcr.io/liken-sh/media-operator-idle:2026.09.01-001"

// plainIdlePod builds the idle client pod of a Player that states no
// idle policy and owns no remotes, for the tests that are about neither.
func plainIdlePod(player *Player, claim *ResourceClaim, busAddress, topicBase, timeZone string) *Pod {
	return buildIdlePod(player, claim, busAddress, topicBase, timeZone,
		resolveIdle(nil, nil, testIdleImage), nil)
}

// The claim holds two requests, the draw device on the Player's own
// screen and the render node, and is owned by the Player. The draw
// request carries the class from config and the Player's display
// selector, so the idle client draws to the same screen a Play does.
func TestBuildIdleClaimHoldsTheDrawDeviceAndRenderNode(t *testing.T) {
	claim := buildIdleClaim(standingIdlePlayer(), "display-draw")

	if claim.APIVersion != claimAPIVersion || claim.Kind != "ResourceClaim" {
		t.Errorf("apiVersion = %q, kind = %q", claim.APIVersion, claim.Kind)
	}
	if claim.Metadata.Name != "theater-idle-devices" {
		t.Errorf("name = %q, want theater-idle-devices", claim.Metadata.Name)
	}
	if claim.Metadata.Namespace != "house" {
		t.Errorf("namespace = %q, want house", claim.Metadata.Namespace)
	}

	requests := claim.Spec.Devices.Requests
	if len(requests) != 2 {
		t.Fatalf("requests = %+v, want two", requests)
	}

	draw := requests[0]
	if draw.Name != "draw" {
		t.Errorf("draw request name = %q, want draw", draw.Name)
	}
	if draw.Exactly == nil || draw.Exactly.DeviceClassName != "display-draw" {
		t.Fatalf("draw request = %+v, want the display-draw class", draw)
	}
	selectors := []DeviceSelector{{CEL: &CELDeviceSelector{
		Expression: `device.attributes["monitor.liken.sh"].id == "DP-1"`,
	}}}
	if !reflect.DeepEqual(draw.Exactly.Selectors, selectors) {
		t.Errorf("draw selectors = %+v, want %+v", draw.Exactly.Selectors, selectors)
	}

	render := requests[1]
	if render.Name != "render" {
		t.Errorf("render request name = %q, want render", render.Name)
	}
	if render.Exactly == nil || render.Exactly.DeviceClassName != "gpu-render" {
		t.Fatalf("render request = %+v, want the gpu-render class", render)
	}
	if len(render.Exactly.Selectors) != 0 {
		t.Errorf("render selectors = %+v, want none", render.Exactly.Selectors)
	}

	owners := []OwnerReference{{
		APIVersion: mediaAPIVersion,
		Kind:       "Player",
		Name:       "theater",
		UID:        "player-uid",
		Controller: true,
	}}
	if !reflect.DeepEqual(claim.Metadata.OwnerReferences, owners) {
		t.Errorf("ownerReferences = %+v, want %+v", claim.Metadata.OwnerReferences, owners)
	}
}

// A Player with no render node yields the draw request alone, the way an
// idle screen driven by a machine with no separate GPU still draws.
func TestBuildIdleClaimOmitsTheRenderRequestWithoutARenderNode(t *testing.T) {
	player := standingIdlePlayer()
	player.Spec.Render = nil

	requests := buildIdleClaim(player, "display-draw").Spec.Devices.Requests
	if len(requests) != 1 {
		t.Fatalf("requests = %+v, want one", requests)
	}
	if requests[0].Name != "draw" {
		t.Errorf("request name = %q, want draw", requests[0].Name)
	}
}

// The pod runs the image IDLE_IMAGE names, with that image's own
// entrypoint. It carries the household timezone, the unit's identity, and
// the bus in its environment, holds the draw and render requests,
// restarts on a crash, and is owned by the Player.
func TestBuildIdlePodRunsTheIdleImage(t *testing.T) {
	player := standingIdlePlayer()
	claim := buildIdleClaim(player, "display-draw")
	pod := plainIdlePod(player, claim, testBusAddress, testTopicBase, "America/New_York")

	if pod.Metadata.Name != "theater-idle" {
		t.Errorf("name = %q, want theater-idle", pod.Metadata.Name)
	}
	if pod.Spec.RestartPolicy != "Always" {
		t.Errorf("restartPolicy = %q, want Always", pod.Spec.RestartPolicy)
	}
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("containers = %+v, want one", pod.Spec.Containers)
	}

	container := pod.Spec.Containers[0]
	mustMatch(t, container.Image, testIdleImage)
	if container.Command != nil {
		t.Errorf("command = %v, want none, so the image starts with its own entrypoint", container.Command)
	}
	wantEnv := map[string]string{
		timeZoneVariable:             "America/New_York",
		idleWindowGraceVariable:      strconv.Itoa(idleWindowGraceSeconds),
		idlePlayerNameVariable:       "theater",
		idlePlayerComponentsVariable: "display-output",
		busAddressVariable:           testBusAddress,
		playerStatusTopicVariable:    playerStatusTopic(testTopicBase, "house", "theater"),
		playerNameVariable:           "theater",
		playerCommandsTopicVariable:  playerCommandsTopic(testTopicBase, "house", "theater"),
		playerPanelTopicVariable:     playerPanelTopic(testTopicBase, "house", "theater"),
		idleFadeAfterSecondsVariable: "600",
		idleOffAfterSecondsVariable:  "0",
	}
	if env := containerEnv(container); !reflect.DeepEqual(env, wantEnv) {
		t.Errorf("env = %+v, want %+v", env, wantEnv)
	}

	claims := container.Resources.Claims
	wantClaims := []ContainerClaim{
		{Name: podClaimName, Request: "draw"},
		{Name: podClaimName, Request: "render"},
	}
	if !reflect.DeepEqual(claims, wantClaims) {
		t.Errorf("resources.claims = %+v, want %+v", claims, wantClaims)
	}

	owners := []OwnerReference{{
		APIVersion: mediaAPIVersion,
		Kind:       "Player",
		Name:       "theater",
		UID:        "player-uid",
		Controller: true,
	}}
	if !reflect.DeepEqual(pod.Metadata.OwnerReferences, owners) {
		t.Errorf("ownerReferences = %+v, want %+v", pod.Metadata.OwnerReferences, owners)
	}
}

// The idle client pod runs the client alone. The client holds the
// timers, the focus gate, the shade, the volume step, and the panel
// desire in its own process, so no unit's client pod carries a second
// container.
func TestBuildIdlePodCarriesNoSecondContainer(t *testing.T) {
	player := standingIdlePlayer()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "America/New_York")

	if len(pod.Spec.InitContainers) != 0 {
		t.Errorf("initContainers = %+v, want none", pod.Spec.InitContainers)
	}
	if len(pod.Spec.Containers) != 1 {
		t.Errorf("containers = %+v, want one", pod.Spec.Containers)
	}
}

// The idle client holds a window for its whole life, so its container
// carries the grace it waits for one. The client exits non-zero when the
// window never arrives, and the kubelet restarts the container until the
// compositor is back.
func TestBuildIdlePodArmsTheWindowWatchdog(t *testing.T) {
	player := standingIdlePlayer()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "")

	mustMatch(t, envValue(pod.Spec.Containers[0], idleWindowGraceVariable),
		strconv.Itoa(idleWindowGraceSeconds))
}

// An unset household zone carries no TZ, so the clock stays on UTC. The
// identity variables still travel, because they name the unit and do not
// depend on the zone.
func TestBuildIdlePodOmitsTheTimeZoneWhenUnset(t *testing.T) {
	player := standingIdlePlayer()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"), testBusAddress, testTopicBase, "")

	env := containerEnv(pod.Spec.Containers[0])
	if _, held := env[timeZoneVariable]; held {
		t.Errorf("env carries %s, want it omitted: %+v", timeZoneVariable, env)
	}
	mustMatch(t, env[idlePlayerNameVariable], "theater")
}

// The idle pod carries the unit's friendly name and its parts, resolved from
// spec.displayName and each selection's displayName. The parts join with
// newlines in display, sink, remote order, so the client splits them into
// the bottom-left list.
func TestBuildIdlePodCarriesTheIdentity(t *testing.T) {
	player := &Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house", UID: "player-uid"},
		Spec: PlayerSpec{
			DisplayName: "Studio Lab",
			Display:     &PlayerDevice{Class: "display-output", DisplayName: "Portable Screen"},
			Sinks:       []PlayerDevice{{Class: "audio-sink", DisplayName: "Built-in Speakers"}},
			Remotes:     []PlayerRemote{{Name: "pad", DisplayName: "Studio Dualsense Controller"}},
		},
	}
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"), testBusAddress, testTopicBase, "")

	env := containerEnv(pod.Spec.Containers[0])
	if env[idlePlayerNameVariable] != "Studio Lab" {
		t.Errorf("%s = %q, want Studio Lab", idlePlayerNameVariable, env[idlePlayerNameVariable])
	}
	want := "Portable Screen\nBuilt-in Speakers\nStudio Dualsense Controller"
	if env[idlePlayerComponentsVariable] != want {
		t.Errorf("%s = %q, want %q", idlePlayerComponentsVariable, env[idlePlayerComponentsVariable], want)
	}
}

// The friendly name falls back to the object name when spec.displayName is
// unset, so an unnamed Player still shows a name on the idle screen.
func TestIdlePlayerNameFallsBackToTheObjectName(t *testing.T) {
	player := standingIdlePlayer()
	if got := idlePlayerName(player); got != "theater" {
		t.Errorf("idlePlayerName = %q, want theater", got)
	}

	player.Spec.DisplayName = "Studio Lab"
	if got := idlePlayerName(player); got != "Studio Lab" {
		t.Errorf("idlePlayerName = %q, want Studio Lab", got)
	}
}

// idleComponents lists the display, then the sinks, then the remotes, in spec
// order. Each name resolves to its displayName when set, and to a plain
// fallback when unset: the DeviceClass for a device, the referenced Remote for
// a controller.
func TestIdleComponentsOrderAndFallback(t *testing.T) {
	player := &Player{
		Spec: PlayerSpec{
			Display: &PlayerDevice{Class: "display-output", DisplayName: "Portable Screen"},
			Sinks: []PlayerDevice{
				{Class: "audio-sink", DisplayName: "Built-in Speakers"},
				{Class: "hdmi-audio"},
			},
			Remotes: []PlayerRemote{
				{Name: "pad", DisplayName: "Studio Dualsense Controller"},
				{Name: "wand"},
			},
		},
	}
	got := idleComponents(player)
	want := []string{
		"Portable Screen",
		"Built-in Speakers",
		"hdmi-audio",
		"Studio Dualsense Controller",
		"wand",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("idleComponents = %+v, want %+v", got, want)
	}
}

// A Player with no display and no parts yields no components, so buildIdlePod
// sends no parts variable and the idle screen draws the name alone.
func TestBuildIdlePodOmitsTheComponentsWhenThereAreNone(t *testing.T) {
	player := &Player{Metadata: ObjectMeta{Name: "solo", Namespace: "house"}}
	if got := idleComponents(player); len(got) != 0 {
		t.Fatalf("idleComponents = %+v, want none", got)
	}
	pod := plainIdlePod(player, buildIdleClaim(&Player{Spec: PlayerSpec{Display: &PlayerDevice{Class: "display-output"}}}, "display-draw"),
		testBusAddress, testTopicBase, "")

	env := map[string]string{}
	for _, entry := range pod.Spec.Containers[0].Env {
		env[entry.Name] = entry.Value
	}
	if _, held := env[idlePlayerComponentsVariable]; held {
		t.Errorf("env carries %s, want it omitted: %+v", idlePlayerComponentsVariable, env)
	}
	if env[idlePlayerNameVariable] != "solo" {
		t.Errorf("%s = %q, want solo", idlePlayerNameVariable, env[idlePlayerNameVariable])
	}
}

// The first reconcile creates the idle claim and the standing idle pod. A
// second reconcile creates nothing, because both are already there.
func TestReconcileIdleCreatesTheClaimAndThePodOnce(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := standingIdlePlayer()

	if err := media.reconcileIdle(player, "America/New_York", nil); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	claim, held := cluster.claims["theater-idle-devices"]
	if !held {
		t.Fatalf("no claim was created: %v", cluster.requests)
	}
	if got := claim.Spec.Devices.Requests[0].Name; got != "draw" {
		t.Errorf("request = %q, want draw", got)
	}
	if _, held := cluster.pods["theater-idle"]; !held {
		t.Fatalf("no standing idle pod was created: %v", cluster.requests)
	}

	created := len(cluster.requests)
	if err := media.reconcileIdle(player, "America/New_York", nil); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	posts := 0
	for _, request := range cluster.requests[created:] {
		if strings.HasPrefix(request, http.MethodPost) {
			posts++
		}
	}
	if posts != 0 {
		t.Errorf("the second reconcile created objects: %v", cluster.requests[created:])
	}
}

// A Player that drives no screen gets no idle pod, so an audio-only unit
// creates nothing.
func TestReconcileIdleBuildsNothingWithoutADisplay(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := standingIdlePlayer()
	player.Spec.Display = nil

	if err := media.reconcileIdle(player, "America/New_York", nil); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(cluster.claims) != 0 || len(cluster.pods) != 0 {
		t.Errorf("built claims %v and pods %v, want none", cluster.claims, cluster.pods)
	}
}

// An unset display class turns the idle screen off, so every Player
// creates nothing however it is shaped.
func TestReconcileIdleBuildsNothingWhenTheClassIsUnset(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	player := standingIdlePlayer()

	if err := media.reconcileIdle(player, "America/New_York", nil); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(cluster.claims) != 0 || len(cluster.pods) != 0 {
		t.Errorf("built claims %v and pods %v, want none", cluster.claims, cluster.pods)
	}
}

// The idle client pod carries the resolved quiet window and the two
// remote lists, one per line and aligned by position, so the client
// pairs each controller's presses with the mark that gates them.
func TestBuildIdlePodCarriesTheFadePolicyAndTheRemotes(t *testing.T) {
	player := standingIdlePlayer()
	player.Spec.Remotes = []PlayerRemote{{Name: "sofa"}, {Name: "armchair"}}
	pod := buildIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "", resolveIdle(fadeAfter(60), nil, testIdleImage),
		gatherIdleRemotes(player, testTopicBase))

	env := containerEnv(pod.Spec.Containers[0])
	mustMatch(t, env[idleFadeAfterSecondsVariable], "60")
	mustMatch(t, env[remoteEventsTopicsVariable],
		"liken/media/remotes/house/sofa/events\nliken/media/remotes/house/armchair/events")
	mustMatch(t, env[remoteFocusTopicsVariable],
		"liken/media/remotes/house/sofa/focus\nliken/media/remotes/house/armchair/focus")
}

// The idle client gates every press on the mark, so its container
// carries the Player's own object name beside the friendly name the
// screen draws. A mark never carries the friendly name, so the two
// variables differ on a Player that states a displayName.
func TestTheIdlePodCarriesBothNames(t *testing.T) {
	player := standingIdlePlayer()
	player.Spec.DisplayName = "The Theater"
	client := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "")

	mustMatch(t, envValue(client.Spec.Containers[0], playerNameVariable), "theater")
	mustMatch(t, envValue(client.Spec.Containers[0], idlePlayerNameVariable), "The Theater")
}

// The commands topic and the panel topic reach every idle client,
// because the client answers the re-present and states the panel desire
// whatever the unit is made of.
func TestBuildIdlePodCarriesTheCommandsAndPanelTopics(t *testing.T) {
	player := standingIdlePlayer()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "")

	container := pod.Spec.Containers[0]
	mustMatch(t, envValue(container, playerCommandsTopicVariable),
		playerCommandsTopic(testTopicBase, "house", "theater"))
	mustMatch(t, envValue(container, playerPanelTopicVariable),
		playerPanelTopic(testTopicBase, "house", "theater"))
}

// A Player that owns no controller sends neither remote list, so its idle
// screen fades on the window alone and wakes on a Play.
func TestBuildIdlePodOmitsTheRemoteListsWithoutRemotes(t *testing.T) {
	player := standingIdlePlayer()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "")

	env := containerEnv(pod.Spec.Containers[0])
	if _, carried := env[remoteEventsTopicsVariable]; carried {
		t.Errorf("%s = %q, want none", remoteEventsTopicsVariable, env[remoteEventsTopicsVariable])
	}
	if _, carried := env[remoteFocusTopicsVariable]; carried {
		t.Errorf("%s = %q, want none", remoteFocusTopicsVariable, env[remoteFocusTopicsVariable])
	}
}

// The gather reads spec.remotes in spec order and builds each
// controller's two topics, so the pod pairs them by position.
func TestGatherIdleRemotesBuildsEachControllersTopics(t *testing.T) {
	player := standingIdlePlayer()
	player.Spec.Remotes = []PlayerRemote{{Name: "sofa"}, {Name: "armchair"}}

	remotes := gatherIdleRemotes(player, testTopicBase)

	want := []idleRemoteTopics{
		{
			Events: remoteEventsTopic(testTopicBase, "house", "sofa"),
			Focus:  remoteFocusTopic(testTopicBase, "house", "sofa"),
		},
		{
			Events: remoteEventsTopic(testTopicBase, "house", "armchair"),
			Focus:  remoteFocusTopic(testTopicBase, "house", "armchair"),
		},
	}
	if !reflect.DeepEqual(remotes, want) {
		t.Errorf("remotes = %+v, want %+v", remotes, want)
	}
}

// A Remote a Player names that does not exist still gets its topics, so
// a missing object breaks nothing: the controller that is not there
// publishes nothing on them.
func TestGatherIdleRemotesBuildsTopicsForAMissingRemote(t *testing.T) {
	player := standingIdlePlayer()
	player.Spec.Remotes = []PlayerRemote{{Name: "ghost"}}

	remotes := gatherIdleRemotes(player, testTopicBase)

	want := []idleRemoteTopics{{
		Events: remoteEventsTopic(testTopicBase, "house", "ghost"),
		Focus:  remoteFocusTopic(testTopicBase, "house", "ghost"),
	}}
	if !reflect.DeepEqual(remotes, want) {
		t.Errorf("remotes = %+v, want %+v", remotes, want)
	}
}

// The off window is set on every idle client pod, because the resolver
// settles it for every Player and zero is a policy the client reads.
// The off mode stays with the operator, so the pod carries none.
func TestBuildIdlePodCarriesTheOffWindow(t *testing.T) {
	player := standingIdlePlayer()
	idle := resolveIdle(&IdlePolicy{
		OffAfterSeconds: ptr(int64(1800)),
		OffMode:         offModePower,
	}, nil, testIdleImage)

	pod := buildIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "", idle, nil)

	mustMatch(t, envValue(pod.Spec.Containers[0], idleOffAfterSecondsVariable), "1800")
}

// namedIdleImage is the image a tier names for the idle client in these
// tests. It is not the client IDLE_IMAGE gives every screen, so a pod
// that runs it tells the override and the fallback apart.
const namedIdleImage = "ghcr.io/liken-sh/media-browser:2026.09.01-001"

// namedIdlePod builds the idle pod of a Player whose idle policy names
// an image, with no household default under it.
func namedIdlePod(player *Player, image string) *Pod {
	return buildIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "America/New_York",
		resolveIdle(&IdlePolicy{Image: image}, nil, testIdleImage), nil)
}

// A Player that names an image runs that image in place of the one
// IDLE_IMAGE gives it, and the image alone changes. A client that draws a
// screen needs the same things whoever wrote it, so the container keeps
// every claim and every variable, and it starts with its own entrypoint
// either way.
func TestBuildIdlePodRunsTheNamedImageAndChangesNothingElse(t *testing.T) {
	player := standingIdlePlayer()
	player.Spec.Sinks = []PlayerDevice{{Class: "audio-sink"}}
	fallback := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "America/New_York")
	named := namedIdlePod(player, namedIdleImage)

	container := named.Spec.Containers[0]
	mustMatch(t, container.Name, idleContainer)
	mustMatch(t, container.Image, namedIdleImage)
	mustMatch(t, fallback.Spec.Containers[0].Image, testIdleImage)
	if container.Command != nil {
		t.Errorf("command = %v, want none, so the image starts with its own entrypoint", container.Command)
	}
	if !reflect.DeepEqual(containerEnv(container), containerEnv(fallback.Spec.Containers[0])) {
		t.Errorf("env = %+v, want %+v", containerEnv(container), containerEnv(fallback.Spec.Containers[0]))
	}
	if !reflect.DeepEqual(container.Resources.Claims, fallback.Spec.Containers[0].Resources.Claims) {
		t.Errorf("resources.claims = %+v, want %+v",
			container.Resources.Claims, fallback.Spec.Containers[0].Resources.Claims)
	}
}

// The volume topic is the speaker gate: a unit with speakers has a
// level to draw and step, and a unit without has none.
func TestBuildIdlePodCarriesTheVolumeTopicToTheClientOnlyWithSinks(t *testing.T) {
	speakerless := standingIdlePlayer()
	pod := plainIdlePod(speakerless, buildIdleClaim(speakerless, "display-draw"),
		testBusAddress, testTopicBase, "")
	mustMatch(t, envValue(pod.Spec.Containers[0], playerVolumeTopicVariable), "")

	speakered := standingIdlePlayer()
	speakered.Spec.Sinks = []PlayerDevice{{Class: "audio-sink"}}
	pod = plainIdlePod(speakered, buildIdleClaim(speakered, "display-draw"),
		testBusAddress, testTopicBase, "")
	mustMatch(t, envValue(pod.Spec.Containers[0], playerVolumeTopicVariable),
		playerVolumeTopic(testTopicBase, "house", "theater"))
}

// idleObjects names the claims and pods the cluster holds, sorted, so a
// case states the whole of what one controller stands up.
func idleObjects(cluster *fakeCluster) []string {
	var names []string
	for name := range cluster.claims {
		names = append(names, "claim/"+name)
	}
	for name := range cluster.pods {
		names = append(names, "pod/"+name)
	}
	sort.Strings(names)
	return names
}

// The resolved controller decides which objects stand. This operator's
// own name stands the claim and the idle client pod. A delegate stands
// the claim alone, and the delegate's own operator builds the pod that
// draws. Under media.liken.sh/none nothing stands.
func TestReconcileIdleStandsWhatTheControllerCallsFor(t *testing.T) {
	cases := []struct {
		name       string
		controller string
		want       []string
	}{
		{
			name: "the built-in default",
			want: []string{"claim/theater-idle-devices", "pod/theater-idle"},
		},
		{
			name:       "this operator by name",
			controller: idleControllerOwn,
			want:       []string{"claim/theater-idle-devices", "pod/theater-idle"},
		},
		{
			name:       "a delegate",
			controller: "library.liken.sh/media-browser",
			want:       []string{"claim/theater-idle-devices"},
		},
		{name: "none", controller: idleControllerNone},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			media := testOperator(t, cluster, make(chan struct{}, 1))
			media.idleDisplayClass = "display-draw"
			player := standingIdlePlayer()
			if one.controller != "" {
				player.Spec.Idle = &IdlePolicy{Controller: one.controller}
			}

			mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

			mustMatchAll(t, idleObjects(cluster), one.want)
		})
	}
}

// a household that names a delegate on the MediaPreferences and
// nothing on the Player takes the delegate, because the controller
// resolves through the same tiers the image does.
func TestReconcileIdleReadsTheHouseholdController(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"

	mustSucceed(t, media.reconcileIdle(standingIdlePlayer(), "America/New_York",
		&IdlePolicy{Controller: "library.liken.sh/media-browser"}))

	mustMatchAll(t, idleObjects(cluster), []string{"claim/theater-idle-devices"})
}

// A unit switched to media.liken.sh/none loses the claim, its holders,
// and the idle client pod, so nothing draws and the screen shows the
// compositor's background.
func TestReconcileIdleClearsWhatStandsUnderNone(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := standingIdlePlayer()
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	player.Spec.Idle = &IdlePolicy{Controller: idleControllerNone}
	// One pass clears both. The claim takes its holders with it, and
	// the idle client pod is one of them.
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	if got := idleObjects(cluster); len(got) != 0 {
		t.Errorf("objects stand under none: %v", got)
	}
}

// A cluster on an older release carries a <player>-idle-command pod
// that nothing builds any more. The pass deletes it, so it stops
// stepping the volume beside the client that holds those rules.
func TestReconcileIdleDeletesThePodAnOlderReleaseStood(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := standingIdlePlayer()
	retired := idlePodName(player.Metadata.Name) + retiredIdleCommandPodSuffix
	cluster.pods[retired] = &Pod{
		Metadata: ObjectMeta{Name: retired, Namespace: player.Metadata.Namespace},
	}

	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	mustMatchAll(t, idleObjects(cluster), []string{
		"claim/theater-idle-devices", "pod/theater-idle"})
}

// A cluster that never ran the older release holds no such pod, and the
// pass reads that once and writes nothing.
func TestReconcileIdleReadsTheRetiredPodOnceWhenNoneStands(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := standingIdlePlayer()
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))
	retired := idlePodName(player.Metadata.Name) + retiredIdleCommandPodSuffix
	cluster.requests = nil

	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	reads := 0
	for _, request := range cluster.requests {
		if strings.HasSuffix(request, "/"+retired) {
			mustMatch(t, request, "GET "+podsPath(player.Metadata.Namespace)+"/"+retired)
			reads++
		}
	}
	mustMatch(t, reads, 1)
}
