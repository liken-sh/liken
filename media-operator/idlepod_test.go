package main

// These tests cover the standing idle pod a Player becomes: one claim
// that holds the shared draw device and the render node, one pod that
// runs mpv in its idle mode, and the reconcile that creates each once and
// builds nothing when the idle screen is off.

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// One Player that drives a screen, with a display selector the idle claim
// copies and a render node the idle mpv draws through.
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

// plainIdlePod builds the idle pod of a Player that states no idle
// policy and owns no remotes, for the tests that are about neither.
func plainIdlePod(player *Player, claim *ResourceClaim, image, busAddress, topicBase, timeZone string) *Pod {
	return buildIdlePod(player, claim, image, busAddress, topicBase, timeZone, resolveIdle(nil, nil), nil)
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

// The pod runs mpv in the idle mode, carries the household timezone in
// the environment, holds the draw and render requests, mounts the ipc
// socket, restarts on a crash, and is owned by the Player.
func TestBuildIdlePodRunsTheIdleMode(t *testing.T) {
	player := standingIdlePlayer()
	claim := buildIdleClaim(player, "display-draw")
	pod := plainIdlePod(player, claim, testImage, testBusAddress, testTopicBase, "America/New_York")

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
	command := []string{"/media-operator", idleMode}
	if !reflect.DeepEqual(container.Command, command) {
		t.Errorf("command = %v, want %v", container.Command, command)
	}
	env := map[string]string{}
	for _, entry := range container.Env {
		env[entry.Name] = entry.Value
	}
	wantEnv := map[string]string{
		timeZoneVariable:             "America/New_York",
		idlePlayerNameVariable:       "theater",
		idlePlayerComponentsVariable: "display-output",
	}
	if !reflect.DeepEqual(env, wantEnv) {
		t.Errorf("env = %+v, want %+v", env, wantEnv)
	}
	wantMounts := []VolumeMount{ipcMount()}
	if !reflect.DeepEqual(container.VolumeMounts, wantMounts) {
		t.Errorf("volumeMounts = %+v, want %+v", container.VolumeMounts, wantMounts)
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

// The pod carries one native sidecar in the idle-command mode and one ipc
// volume the sidecar and the idle mpv share. The sidecar reads the bus
// address and the Player's commands topic, mounts the ipc socket, and
// restarts alone on a crash. It holds no device claim, because it drives
// mpv over the socket and reaches nothing else.
func TestBuildIdlePodCarriesTheCommandSidecar(t *testing.T) {
	player := standingIdlePlayer()
	claim := buildIdleClaim(player, "display-draw")
	pod := plainIdlePod(player, claim, testImage, testBusAddress, testTopicBase, "America/New_York")

	wantVolumes := []Volume{{Name: ipcVolumeName, EmptyDir: &EmptyDirVolumeSource{}}}
	if !reflect.DeepEqual(pod.Spec.Volumes, wantVolumes) {
		t.Errorf("volumes = %+v, want %+v", pod.Spec.Volumes, wantVolumes)
	}
	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("initContainers = %+v, want one", pod.Spec.InitContainers)
	}

	sidecar := pod.Spec.InitContainers[0]
	if sidecar.Name != idleCommandContainer {
		t.Errorf("sidecar name = %q, want %q", sidecar.Name, idleCommandContainer)
	}
	command := []string{"/media-operator", idleCommandMode}
	if !reflect.DeepEqual(sidecar.Command, command) {
		t.Errorf("sidecar command = %v, want %v", sidecar.Command, command)
	}
	if sidecar.RestartPolicy != sidecarRestartPolicy {
		t.Errorf("sidecar restartPolicy = %q, want %q", sidecar.RestartPolicy, sidecarRestartPolicy)
	}
	if len(sidecar.Resources.Claims) != 0 {
		t.Errorf("sidecar claims = %+v, want none", sidecar.Resources.Claims)
	}
	wantMounts := []VolumeMount{ipcMount()}
	if !reflect.DeepEqual(sidecar.VolumeMounts, wantMounts) {
		t.Errorf("sidecar volumeMounts = %+v, want %+v", sidecar.VolumeMounts, wantMounts)
	}
	env := map[string]string{}
	for _, entry := range sidecar.Env {
		env[entry.Name] = entry.Value
	}
	wantEnv := map[string]string{
		busAddressVariable:           testBusAddress,
		playerNameVariable:           "theater",
		playerCommandsTopicVariable:  playerCommandsTopic(testTopicBase, "house", "theater"),
		playerStatusTopicVariable:    playerStatusTopic(testTopicBase, "house", "theater"),
		idleFadeAfterSecondsVariable: "600",
		idleOffAfterSecondsVariable:  "0",
		idlePanelTopicVariable:       playerPanelTopic(testTopicBase, "house", "theater"),
	}
	if !reflect.DeepEqual(env, wantEnv) {
		t.Errorf("sidecar env = %+v, want %+v", env, wantEnv)
	}
}

// An unset household zone carries no TZ, so the clock stays on UTC. The
// identity variables still travel, because they name the unit and do not
// depend on the zone.
func TestBuildIdlePodOmitsTheTimeZoneWhenUnset(t *testing.T) {
	player := standingIdlePlayer()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"), testImage, testBusAddress, testTopicBase, "")

	env := map[string]string{}
	for _, entry := range pod.Spec.Containers[0].Env {
		env[entry.Name] = entry.Value
	}
	if _, held := env[timeZoneVariable]; held {
		t.Errorf("env carries %s, want it omitted: %+v", timeZoneVariable, env)
	}
	wantEnv := map[string]string{
		idlePlayerNameVariable:       "theater",
		idlePlayerComponentsVariable: "display-output",
	}
	if !reflect.DeepEqual(env, wantEnv) {
		t.Errorf("env = %+v, want %+v", env, wantEnv)
	}
}

// The idle pod carries the unit's friendly name and its parts, resolved from
// spec.displayName and each selection's displayName. The parts join with
// newlines in display, sink, remote order, so the display Lua splits them into
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
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"), testImage, testBusAddress, testTopicBase, "")

	env := map[string]string{}
	for _, entry := range pod.Spec.Containers[0].Env {
		env[entry.Name] = entry.Value
	}
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
		testImage, testBusAddress, testTopicBase, "")

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

// The sidecar carries the resolved quiet window and the two remote lists,
// one per line and aligned by position, so it pairs each controller's
// presses with the keymap that names them.
func TestBuildIdlePodCarriesTheFadePolicyAndTheRemotes(t *testing.T) {
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
	pod := buildIdlePod(player, buildIdleClaim(player, "display-draw"),
		testImage, testBusAddress, testTopicBase, "", resolveIdle(fadeAfter(60), nil), remotes)

	env := map[string]string{}
	for _, entry := range pod.Spec.InitContainers[0].Env {
		env[entry.Name] = entry.Value
	}
	mustMatch(t, env[idleFadeAfterSecondsVariable], "60")
	mustMatch(t, env[idleRemoteEventsTopicsVariable],
		"liken/media/remotes/house/sofa/events\nliken/media/remotes/house/armchair/events")
	mustMatch(t, env[idleRemoteKeymapTopicsVariable], "liken/media/keymaps/gamepad\n")
	mustMatch(t, env[idleRemoteFocusTopicsVariable],
		"liken/media/remotes/house/sofa/focus\nliken/media/remotes/house/armchair/focus")
}

// The sidecar gates every press on the mark, so it carries the Player's own
// object name. The friendly name is the one the screen draws, and a mark never
// carries it, so the two variables differ on a Player that states a
// displayName.
func TestBuildIdlePodCarriesThePlayerName(t *testing.T) {
	player := standingIdlePlayer()
	player.Spec.DisplayName = "The Theater"
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testImage, testBusAddress, testTopicBase, "")

	mustMatch(t, envValue(pod.Spec.InitContainers[0], playerNameVariable), "theater")
	mustMatch(t, envValue(pod.Spec.Containers[0], idlePlayerNameVariable), "The Theater")
}

// The volume topic reaches the idle sidecar only for a unit that
// has speakers. A Player with no sinks has no level to mean anything, so
// its idle sidecar reads no topic at all.
func TestBuildIdlePodCarriesTheVolumeTopicOnlyWithSinks(t *testing.T) {
	speakerless := standingIdlePlayer()
	pod := plainIdlePod(speakerless, buildIdleClaim(speakerless, "display-draw"),
		testImage, testBusAddress, testTopicBase, "")
	mustMatch(t, envValue(pod.Spec.InitContainers[0], playerVolumeTopicVariable), "")

	speakered := standingIdlePlayer()
	speakered.Spec.Sinks = []PlayerDevice{{Class: "audio-sink"}}
	pod = plainIdlePod(speakered, buildIdleClaim(speakered, "display-draw"),
		testImage, testBusAddress, testTopicBase, "")
	mustMatch(t, envValue(pod.Spec.InitContainers[0], playerVolumeTopicVariable),
		playerVolumeTopic(testTopicBase, "house", "theater"))
}

// A Player that owns no controller sends neither remote list, so its idle
// screen fades on the window alone and wakes on a Play.
func TestBuildIdlePodOmitsTheRemoteListsWithoutRemotes(t *testing.T) {
	player := standingIdlePlayer()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testImage, testBusAddress, testTopicBase, "")

	env := map[string]string{}
	for _, entry := range pod.Spec.InitContainers[0].Env {
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

// The off window and the panel topic are set on every idle pod,
// because the resolver settles both for every Player and zero is a
// policy the sidecar reads. The off mode stays with the operator, so
// the pod carries none.
func TestBuildIdlePodCarriesTheOffWindow(t *testing.T) {
	player := standingIdlePlayer()
	idle := resolveIdle(&IdlePolicy{
		OffAfterSeconds: ptr(int64(1800)),
		OffMode:         offModePower,
	}, nil)

	pod := buildIdlePod(player, buildIdleClaim(player, "display-draw"),
		testImage, testBusAddress, testTopicBase, "", idle, nil)

	env := map[string]string{}
	for _, entry := range pod.Spec.InitContainers[0].Env {
		env[entry.Name] = entry.Value
	}
	mustMatch(t, env[idleOffAfterSecondsVariable], "1800")
	mustMatch(t, env[idlePanelTopicVariable],
		playerPanelTopic(testTopicBase, "house", "theater"))
}
