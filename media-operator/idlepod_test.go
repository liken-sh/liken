package main

// These tests cover the standing idle pod a Player becomes: one claim
// that holds the shared draw device and the render node, one pod that
// runs the idle client beside its command sidecar, and the reconcile
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
// idle policy, for the tests that are not about the policy.
func plainIdlePod(player *Player, claim *ResourceClaim, busAddress, topicBase, timeZone string) *Pod {
	return buildIdlePod(player, claim, busAddress, topicBase, timeZone, resolveIdle(nil, nil, testIdleImage))
}

// plainIdleCommandPod builds the idle command pod of a Player that
// states no idle policy and owns no remotes, for the tests that are
// about neither.
func plainIdleCommandPod(player *Player) *Pod {
	return buildIdleCommandPod(player, testSidecarImage, testBusAddress, testTopicBase,
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
		playerScreenTopicVariable:    playerScreenTopic(testTopicBase, "house", "theater"),
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

// the idle client pod runs the client alone. The timers stand in
// a pod of their own, so no unit's client pod carries a second container.
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

// the idle command pod stands on its own, owned by the Player,
// with one container in the idle-command mode. It runs the sidecar
// image, reads the bus address and the Player's topics, and restarts on
// a crash. It holds no claim and no volume, because it reads the bus and
// writes the bus.
func TestBuildIdleCommandPodStandsOnItsOwn(t *testing.T) {
	player := standingIdlePlayer()
	pod := plainIdleCommandPod(player)

	mustMatch(t, pod.Metadata.Name, "theater-idle-command")
	mustMatch(t, pod.Metadata.Namespace, "house")
	mustMatch(t, pod.Spec.RestartPolicy, "Always")
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
	if pod.Spec.Volumes != nil {
		t.Errorf("volumes = %+v, want none", pod.Spec.Volumes)
	}
	if pod.Spec.ResourceClaims != nil {
		t.Errorf("resourceClaims = %+v, want none", pod.Spec.ResourceClaims)
	}
	if len(pod.Spec.InitContainers) != 0 {
		t.Errorf("initContainers = %+v, want none", pod.Spec.InitContainers)
	}
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("containers = %+v, want one", pod.Spec.Containers)
	}

	held := pod.Spec.Containers[0]
	if held.Name != idleCommandContainer {
		t.Errorf("container name = %q, want %q", held.Name, idleCommandContainer)
	}
	mustMatch(t, held.Image, testSidecarImage)
	command := []string{"/media-operator", idleCommandMode}
	if !reflect.DeepEqual(held.Command, command) {
		t.Errorf("command = %v, want %v", held.Command, command)
	}
	if held.RestartPolicy != "" {
		t.Errorf("container restartPolicy = %q, want none, so the pod's own policy rules", held.RestartPolicy)
	}
	if len(held.Resources.Claims) != 0 {
		t.Errorf("claims = %+v, want none", held.Resources.Claims)
	}
	if held.VolumeMounts != nil {
		t.Errorf("volumeMounts = %+v, want none", held.VolumeMounts)
	}
	wantEnv := map[string]string{
		busAddressVariable:           testBusAddress,
		playerNameVariable:           "theater",
		playerCommandsTopicVariable:  playerCommandsTopic(testTopicBase, "house", "theater"),
		playerStatusTopicVariable:    playerStatusTopic(testTopicBase, "house", "theater"),
		playerScreenTopicVariable:    playerScreenTopic(testTopicBase, "house", "theater"),
		idleFadeAfterSecondsVariable: "600",
		idleOffAfterSecondsVariable:  "0",
		idlePanelTopicVariable:       playerPanelTopic(testTopicBase, "house", "theater"),
	}
	if env := containerEnv(held); !reflect.DeepEqual(env, wantEnv) {
		t.Errorf("env = %+v, want %+v", env, wantEnv)
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

// the idle command pod carries the resolved quiet window and the
// two remote lists, one per line and aligned by position, so it pairs
// each controller's presses with the keymap that names them.
func TestBuildIdleCommandPodCarriesTheFadePolicyAndTheRemotes(t *testing.T) {
	player := standingIdlePlayer()
	player.Spec.Remotes = []PlayerRemote{{Name: "sofa"}, {Name: "armchair"}}
	remotes := []idleRemoteTopics{
		{
			Events: remoteEventsTopic(testTopicBase, "house", "sofa"),
			Keymap: keymapTopic(testTopicBase, "gamepad"),
			Focus:  remoteFocusTopic(testTopicBase, "house", "sofa"),
		},
		{
			Events: remoteEventsTopic(testTopicBase, "house", "armchair"),
			Focus:  remoteFocusTopic(testTopicBase, "house", "armchair"),
		},
	}
	pod := buildIdleCommandPod(player, testSidecarImage, testBusAddress, testTopicBase,
		resolveIdle(fadeAfter(60), nil, testIdleImage), remotes)

	env := containerEnv(pod.Spec.Containers[0])
	mustMatch(t, env[idleFadeAfterSecondsVariable], "60")
	mustMatch(t, env[idleRemoteEventsTopicsVariable],
		"liken/media/remotes/house/sofa/events\nliken/media/remotes/house/armchair/events")
	mustMatch(t, env[idleRemoteKeymapTopicsVariable], "liken/media/keymaps/gamepad\n")
	mustMatch(t, env[idleRemoteFocusTopicsVariable],
		"liken/media/remotes/house/sofa/focus\nliken/media/remotes/house/armchair/focus")
}

// the idle command pod gates every press on the mark, so it
// carries the Player's own object name. The friendly name is the one the
// screen draws, and a mark never carries it, so the two pods differ on a
// Player that states a displayName.
func TestTheIdlePodsCarryThePlayerName(t *testing.T) {
	player := standingIdlePlayer()
	player.Spec.DisplayName = "The Theater"
	client := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "")

	mustMatch(t, envValue(plainIdleCommandPod(player).Spec.Containers[0], playerNameVariable), "theater")
	mustMatch(t, envValue(client.Spec.Containers[0], idlePlayerNameVariable), "The Theater")
}

// the volume topic reaches the idle command pod only for a unit
// that has speakers. A Player with no sinks has no level to mean
// anything, so its idle command pod reads no topic at all.
func TestBuildIdleCommandPodCarriesTheVolumeTopicOnlyWithSinks(t *testing.T) {
	speakerless := standingIdlePlayer()
	pod := plainIdleCommandPod(speakerless)
	mustMatch(t, envValue(pod.Spec.Containers[0], playerVolumeTopicVariable), "")

	speakered := standingIdlePlayer()
	speakered.Spec.Sinks = []PlayerDevice{{Class: "audio-sink"}}
	pod = plainIdleCommandPod(speakered)
	mustMatch(t, envValue(pod.Spec.Containers[0], playerVolumeTopicVariable),
		playerVolumeTopic(testTopicBase, "house", "theater"))
}

// A Player that owns no controller sends neither remote list, so its idle
// screen fades on the window alone and wakes on a Play.
func TestBuildIdleCommandPodOmitsTheRemoteListsWithoutRemotes(t *testing.T) {
	player := standingIdlePlayer()
	pod := plainIdleCommandPod(player)

	env := map[string]string{}
	for _, entry := range pod.Spec.Containers[0].Env {
		env[entry.Name] = entry.Value
	}
	if _, carried := env[idleRemoteEventsTopicsVariable]; carried {
		t.Errorf("%s = %q, want none", idleRemoteEventsTopicsVariable, env[idleRemoteEventsTopicsVariable])
	}
	if _, carried := env[idleRemoteKeymapTopicsVariable]; carried {
		t.Errorf("%s = %q, want none", idleRemoteKeymapTopicsVariable, env[idleRemoteKeymapTopicsVariable])
	}
	if _, carried := env[idleRemoteFocusTopicsVariable]; carried {
		t.Errorf("%s = %q, want none", idleRemoteFocusTopicsVariable, env[idleRemoteFocusTopicsVariable])
	}
}

// The gather reads spec.remotes in spec order and resolves each keymap the
// way a Play does: the Player entry's own override, or the Remote's base
// keymap.
func TestGatherIdleRemotesResolvesEachKeymap(t *testing.T) {
	cluster := newFakeCluster()
	cluster.remotes["sofa"] = houseRemote("gamepad")
	media := testOperator(t, cluster, make(chan struct{}, 1))
	player := standingIdlePlayer()
	player.Spec.Remotes = []PlayerRemote{{Name: "sofa"}, {Name: "armchair", Keymap: "pad"}}

	remotes := gatherIdleRemotes(media.client, player, testTopicBase)

	want := []idleRemoteTopics{
		{
			Events: remoteEventsTopic(testTopicBase, "house", "sofa"),
			Keymap: keymapTopic(testTopicBase, "gamepad"),
			Focus:  remoteFocusTopic(testTopicBase, "house", "sofa"),
		},
		{
			Events: remoteEventsTopic(testTopicBase, "house", "armchair"),
			Keymap: keymapTopic(testTopicBase, "pad"),
			Focus:  remoteFocusTopic(testTopicBase, "house", "armchair"),
		},
	}
	if !reflect.DeepEqual(remotes, want) {
		t.Errorf("remotes = %+v, want %+v", remotes, want)
	}
}

// A Remote a Player names that does not exist leaves that controller with
// no keymap. Its presses still wake the screen, and they name no action, so
// a missing object dims nothing and breaks nothing.
func TestGatherIdleRemotesLeavesAMissingRemoteWithoutAKeymap(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	player := standingIdlePlayer()
	player.Spec.Remotes = []PlayerRemote{{Name: "sofa"}}

	remotes := gatherIdleRemotes(media.client, player, testTopicBase)

	want := []idleRemoteTopics{{
		Events: remoteEventsTopic(testTopicBase, "house", "sofa"),
		Focus:  remoteFocusTopic(testTopicBase, "house", "sofa"),
	}}
	if !reflect.DeepEqual(remotes, want) {
		t.Errorf("remotes = %+v, want %+v", remotes, want)
	}
}

// the off window and the panel topic are set on every idle
// command pod, because the resolver settles both for every Player and
// zero is a policy the pod reads. The off mode stays with the operator,
// so the pod carries none.
func TestBuildIdleCommandPodCarriesTheOffWindow(t *testing.T) {
	player := standingIdlePlayer()
	idle := resolveIdle(&IdlePolicy{
		OffAfterSeconds: ptr(int64(1800)),
		OffMode:         offModePower,
	}, nil, testIdleImage)

	pod := buildIdleCommandPod(player, testSidecarImage, testBusAddress, testTopicBase, idle, nil)

	env := containerEnv(pod.Spec.Containers[0])
	mustMatch(t, env[idleOffAfterSecondsVariable], "1800")
	mustMatch(t, env[idlePanelTopicVariable],
		playerPanelTopic(testTopicBase, "house", "theater"))
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
		resolveIdle(&IdlePolicy{Image: image}, nil, testIdleImage))
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

// the volume topic reaches the client on the rule the idle
// command pod reads it on: a unit with speakers has a level to draw, and
// a unit without has none.
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

// the idle command pod states its four moments on the screen
// topic for whichever client draws, so it and every idle client pod
// carry the same topic.
func TestTheIdlePodsCarryTheScreenTopic(t *testing.T) {
	player := standingIdlePlayer()
	want := playerScreenTopic(testTopicBase, "house", "theater")

	mustMatch(t, envValue(plainIdleCommandPod(player).Spec.Containers[0], playerScreenTopicVariable), want)

	fallback := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "")
	mustMatch(t, envValue(fallback.Spec.Containers[0], playerScreenTopicVariable), want)

	named := namedIdlePod(player, namedIdleImage)
	mustMatch(t, envValue(named.Spec.Containers[0], playerScreenTopicVariable), want)
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

// the resolved controller decides which objects stand. This
// operator's own name stands the claim, the idle client pod, and the idle
// command pod. A delegate stands the claim and the idle command pod, and
// the delegate's own operator builds the pod that draws. Under
// media.liken.sh/none nothing stands.
func TestReconcileIdleStandsWhatTheControllerCallsFor(t *testing.T) {
	cases := []struct {
		name       string
		controller string
		want       []string
	}{
		{
			name: "the built-in default",
			want: []string{"claim/theater-idle-devices", "pod/theater-idle", "pod/theater-idle-command"},
		},
		{
			name:       "this operator by name",
			controller: idleControllerOwn,
			want:       []string{"claim/theater-idle-devices", "pod/theater-idle", "pod/theater-idle-command"},
		},
		{
			name:       "a delegate",
			controller: "library.liken.sh/media-browser",
			want:       []string{"claim/theater-idle-devices", "pod/theater-idle-command"},
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

	mustMatchAll(t, idleObjects(cluster),
		[]string{"claim/theater-idle-devices", "pod/theater-idle-command"})
}

// a unit switched to media.liken.sh/none loses the claim, its
// holders, and both pods, so nothing draws and the compositor's
// background is what the screen shows.
func TestReconcileIdleClearsWhatStandsUnderNone(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := standingIdlePlayer()
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	player.Spec.Idle = &IdlePolicy{Controller: idleControllerNone}
	// one pass clears all three. The claim takes its holders with
	// it, and the idle command pod holds no claim, so it goes on the same
	// pass rather than waiting for the claim to finish terminating.
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	if got := idleObjects(cluster); len(got) != 0 {
		t.Errorf("objects stand under none: %v", got)
	}
}
