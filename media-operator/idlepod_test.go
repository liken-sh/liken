package main

import (
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// playerWithDisplay returns a Player with a display and a render node.
func playerWithDisplay() *Player {
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

func containerEnv(container Container) map[string]string {
	env := map[string]string{}
	for _, entry := range container.Env {
		env[entry.Name] = entry.Value
	}
	return env
}

// testIdleImage stands for IDLE_IMAGE, the image a Player runs when no
// idle policy sets one.
const testIdleImage = "ghcr.io/liken-sh/media-operator-idle:2026.09.01-001"

func plainIdlePod(player *Player, claim *ResourceClaim, busAddress, topicBase, timeZone string) *Pod {
	return buildIdlePod(player, claim, busAddress, topicBase, timeZone,
		resolveIdle(nil, nil, testIdleImage), nil)
}

func TestBuildIdleClaimRequestsTheDrawDeviceAndRenderNode(t *testing.T) {
	claim := buildIdleClaim(playerWithDisplay(), "display-draw")

	if claim.APIVersion != claimAPIVersion || claim.Kind != "ResourceClaim" {
		t.Errorf("apiVersion = %q, kind = %q, want %q and ResourceClaim", claim.APIVersion, claim.Kind, claimAPIVersion)
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

func TestBuildIdleClaimOmitsTheRenderRequestWithoutARenderNode(t *testing.T) {
	player := playerWithDisplay()
	player.Spec.Render = nil

	requests := buildIdleClaim(player, "display-draw").Spec.Devices.Requests
	if len(requests) != 1 {
		t.Fatalf("requests = %+v, want one", requests)
	}
	if requests[0].Name != "draw" {
		t.Errorf("request name = %q, want draw", requests[0].Name)
	}
}

func TestBuildIdlePodRunsTheIdleImage(t *testing.T) {
	player := playerWithDisplay()
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
		playerPowerTopicVariable:     playerPowerTopic(testTopicBase, "house", "theater"),
		idleFadeAfterSecondsVariable: "600",
		idleOffAfterSecondsVariable:  "0",
		metricsAddressVariable:       "0.0.0.0:9200",
		mediaVersionVariable:         "2026.09.01-001",
	}
	if env := containerEnv(container); !reflect.DeepEqual(env, wantEnv) {
		t.Errorf("env = %+v, want %+v", env, wantEnv)
	}

	wantPorts := []ContainerPort{{Name: "metrics", ContainerPort: 9200}}
	if !reflect.DeepEqual(container.Ports, wantPorts) {
		t.Errorf("ports = %+v, want %+v", container.Ports, wantPorts)
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

func TestBuildIdlePodOmitsTheVersionForAnUntaggedImage(t *testing.T) {
	player := playerWithDisplay()
	pod := buildIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "",
		resolveIdle(nil, nil, "ghcr.io/liken-sh/media-operator-idle@sha256:abc"), nil)

	env := containerEnv(pod.Spec.Containers[0])
	if version, present := env[mediaVersionVariable]; present {
		t.Errorf("%s = %q, want unset for an untagged image", mediaVersionVariable, version)
	}
}

// The label value differs from the playback pod's, so isPlaybackPod does
// not match the idle pod.
func TestBuildIdlePodHasTheIdleComponentLabel(t *testing.T) {
	player := playerWithDisplay()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "America/New_York")

	want := map[string]string{"media.liken.sh/component": "idle"}
	if !reflect.DeepEqual(pod.Metadata.Labels, want) {
		t.Errorf("labels = %+v, want %+v", pod.Metadata.Labels, want)
	}
}

// The idle pod tolerates the NoSchedule taint that keeps other workloads
// off a screen machine, and nothing else, so a NoExecute taint still
// evicts it.
func TestBuildIdlePodToleratesThePlayerNodeTaint(t *testing.T) {
	player := playerWithDisplay()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "America/New_York")

	want := []Toleration{{Key: "media.liken.sh/player", Operator: "Exists", Effect: "NoSchedule"}}
	if !reflect.DeepEqual(pod.Spec.Tolerations, want) {
		t.Errorf("tolerations = %+v, want %+v", pod.Spec.Tolerations, want)
	}
}

func TestBuildIdlePodMountsNoServiceAccountToken(t *testing.T) {
	player := playerWithDisplay()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "America/New_York")

	mustMatch(t, pod.Spec.AutomountServiceAccountToken != nil, true)
	mustMatch(t, *pod.Spec.AutomountServiceAccountToken, false)
}

func TestBuildIdlePodHasOnlyTheClientContainer(t *testing.T) {
	player := playerWithDisplay()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "America/New_York")

	if len(pod.Spec.InitContainers) != 0 {
		t.Errorf("initContainers = %+v, want none", pod.Spec.InitContainers)
	}
	if len(pod.Spec.Containers) != 1 {
		t.Errorf("containers = %+v, want one", pod.Spec.Containers)
	}
}

func TestBuildIdlePodArmsTheWindowWatchdog(t *testing.T) {
	player := playerWithDisplay()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "")

	mustMatch(t, envValue(pod.Spec.Containers[0], idleWindowGraceVariable),
		strconv.Itoa(idleWindowGraceSeconds))
}

func TestBuildIdlePodOmitsTheTimeZoneWhenUnset(t *testing.T) {
	player := playerWithDisplay()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"), testBusAddress, testTopicBase, "")

	env := containerEnv(pod.Spec.Containers[0])
	if _, held := env[timeZoneVariable]; held {
		t.Errorf("env has %s, want it omitted: %+v", timeZoneVariable, env)
	}
	mustMatch(t, env[idlePlayerNameVariable], "theater")
}

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

func TestIdlePlayerNameFallsBackToTheObjectName(t *testing.T) {
	player := playerWithDisplay()
	if got := idlePlayerName(player); got != "theater" {
		t.Errorf("idlePlayerName = %q, want theater", got)
	}

	player.Spec.DisplayName = "Studio Lab"
	if got := idlePlayerName(player); got != "Studio Lab" {
		t.Errorf("idlePlayerName = %q, want Studio Lab", got)
	}
}

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
		t.Errorf("env has %s, want it omitted: %+v", idlePlayerComponentsVariable, env)
	}
	if env[idlePlayerNameVariable] != "solo" {
		t.Errorf("%s = %q, want solo", idlePlayerNameVariable, env[idlePlayerNameVariable])
	}
}

func TestReconcileIdleCreatesTheClaimAndThePodOnce(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := playerWithDisplay()

	if err := media.reconcileIdle(player, "America/New_York", nil); err != nil {
		t.Fatalf("reconciling the idle pod the first time: %v", err)
	}

	claim, held := cluster.claims["theater-idle-devices"]
	if !held {
		t.Fatalf("no claim was created: %v", cluster.requests)
	}
	if got := claim.Spec.Devices.Requests[0].Name; got != "draw" {
		t.Errorf("request = %q, want draw", got)
	}
	if _, held := cluster.pods["theater-idle"]; !held {
		t.Fatalf("no idle pod was created: %v", cluster.requests)
	}

	created := len(cluster.requests)
	if err := media.reconcileIdle(player, "America/New_York", nil); err != nil {
		t.Fatalf("reconciling the idle pod the second time: %v", err)
	}
	posts := 0
	for _, request := range cluster.requests[created:] {
		if strings.HasPrefix(request, http.MethodPost) {
			posts++
		}
	}
	if posts != 0 {
		t.Errorf("the second reconcile sent POST requests %v, want none", cluster.requests[created:])
	}
}

func TestReconcileIdleBuildsNothingWithoutADisplay(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := playerWithDisplay()
	player.Spec.Display = nil

	if err := media.reconcileIdle(player, "America/New_York", nil); err != nil {
		t.Fatalf("reconciling the idle pod: %v", err)
	}
	if len(cluster.claims) != 0 || len(cluster.pods) != 0 {
		t.Errorf("built claims %v and pods %v, want none", cluster.claims, cluster.pods)
	}
}

func TestReconcileIdleBuildsNothingWhenTheClassIsUnset(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	player := playerWithDisplay()

	if err := media.reconcileIdle(player, "America/New_York", nil); err != nil {
		t.Fatalf("reconciling the idle pod: %v", err)
	}
	if len(cluster.claims) != 0 || len(cluster.pods) != 0 {
		t.Errorf("built claims %v and pods %v, want none", cluster.claims, cluster.pods)
	}
}

func TestBuildIdlePodCarriesTheFadePolicyAndTheRemotes(t *testing.T) {
	player := playerWithDisplay()
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

func TestBuildIdlePodHasBothTheObjectNameAndTheDisplayName(t *testing.T) {
	player := playerWithDisplay()
	player.Spec.DisplayName = "The Theater"
	client := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "")

	mustMatch(t, envValue(client.Spec.Containers[0], playerNameVariable), "theater")
	mustMatch(t, envValue(client.Spec.Containers[0], idlePlayerNameVariable), "The Theater")
}

func TestBuildIdlePodCarriesTheCommandsAndPanelTopics(t *testing.T) {
	player := playerWithDisplay()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "")

	container := pod.Spec.Containers[0]
	mustMatch(t, envValue(container, playerCommandsTopicVariable),
		playerCommandsTopic(testTopicBase, "house", "theater"))
	mustMatch(t, envValue(container, playerPanelTopicVariable),
		playerPanelTopic(testTopicBase, "house", "theater"))
}

func TestBuildIdlePodCarriesThePowerTopic(t *testing.T) {
	player := playerWithDisplay()
	pod := plainIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "")

	mustMatch(t, envValue(pod.Spec.Containers[0], playerPowerTopicVariable),
		playerPowerTopic(testTopicBase, "house", "theater"))
}

// Adding or removing a Receiver must not change the idle pod's spec,
// because a changed spec replaces the pod and blanks the screen.
func TestBuildIdlePodIsTheSameWithAndWithoutAReceiver(t *testing.T) {
	idlePod := func(cluster *fakeCluster) PodSpec {
		claim := cluster.claims[idleClaimName("theater")]
		mustSucceed(t, stampTemplateHash(&claim.Metadata, claim.Spec))
		media := testOperator(t, cluster, make(chan struct{}, 1))
		media.idleDisplayClass = "display-draw"
		runPlayers(media, []Player{*housePlayer()}, nil)
		return cluster.pods[idlePodName("theater")].Spec
	}

	wired, unwired := idlePod(receiverCluster()), idlePod(screenCluster())

	mustMatch(t, len(wired.Containers) > 0, true)
	if !reflect.DeepEqual(wired, unwired) {
		t.Errorf("the idle pod differs with a receiver:\nwired   %+v\nunwired %+v", wired, unwired)
	}
}

func TestBuildIdlePodOmitsTheRemoteListsWithoutRemotes(t *testing.T) {
	player := playerWithDisplay()
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

func TestGatherIdleRemotesBuildsEachControllersTopics(t *testing.T) {
	player := playerWithDisplay()
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

// A Remote that does not exist still gets topics. Nothing publishes on
// them, so the missing Remote causes no error.
func TestGatherIdleRemotesBuildsTopicsForAMissingRemote(t *testing.T) {
	player := playerWithDisplay()
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

func TestBuildIdlePodCarriesTheOffWindow(t *testing.T) {
	player := playerWithDisplay()
	idle := resolveIdle(&IdlePolicy{
		OffAfterSeconds: ptr(int64(1800)),
		OffMode:         offModePower,
	}, nil, testIdleImage)

	pod := buildIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "", idle, nil)

	mustMatch(t, envValue(pod.Spec.Containers[0], idleOffAfterSecondsVariable), "1800")
}

// namedIdleImage differs from testIdleImage, so a pod's image shows
// whether the policy or the default supplied it.
const namedIdleImage = "ghcr.io/liken-sh/media-browser:2026.09.01-001"

func namedIdlePod(player *Player, image string) *Pod {
	return buildIdlePod(player, buildIdleClaim(player, "display-draw"),
		testBusAddress, testTopicBase, "America/New_York",
		resolveIdle(&IdlePolicy{Image: image}, nil, testIdleImage), nil)
}

func TestBuildIdlePodRunsTheNamedImageAndChangesNothingElse(t *testing.T) {
	player := playerWithDisplay()
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

func TestBuildIdlePodSetsTheVolumeTopicOnlyWithSinks(t *testing.T) {
	speakerless := playerWithDisplay()
	pod := plainIdlePod(speakerless, buildIdleClaim(speakerless, "display-draw"),
		testBusAddress, testTopicBase, "")
	mustMatch(t, envValue(pod.Spec.Containers[0], playerVolumeTopicVariable), "")

	speakered := playerWithDisplay()
	speakered.Spec.Sinks = []PlayerDevice{{Class: "audio-sink"}}
	pod = plainIdlePod(speakered, buildIdleClaim(speakered, "display-draw"),
		testBusAddress, testTopicBase, "")
	mustMatch(t, envValue(pod.Spec.Containers[0], playerVolumeTopicVariable),
		playerVolumeTopic(testTopicBase, "house", "theater"))
}

// idleObjects returns the sorted names of every claim and pod in the
// cluster.
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

func TestReconcileIdleCreatesWhatTheControllerNeeds(t *testing.T) {
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
			player := playerWithDisplay()
			if one.controller != "" {
				player.Spec.Idle = &IdlePolicy{Controller: one.controller}
			}

			mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

			mustMatchAll(t, idleObjects(cluster), one.want)
		})
	}
}

func TestReconcileIdleUsesTheControllerFromMediaPreferences(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"

	mustSucceed(t, media.reconcileIdle(playerWithDisplay(), "America/New_York",
		&IdlePolicy{Controller: "library.liken.sh/media-browser"}))

	mustMatchAll(t, idleObjects(cluster), []string{"claim/theater-idle-devices"})
}

func TestReconcileIdleDeletesTheObjectsUnderNone(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := playerWithDisplay()
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	player.Spec.Idle = &IdlePolicy{Controller: idleControllerNone}
	// One pass deletes both, because deleting the claim also deletes the
	// pods that use it.
	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	if got := idleObjects(cluster); len(got) != 0 {
		t.Errorf("objects remain after the idle controller is none: %v, want none", got)
	}
}

func TestReconcileIdleDeletesTheRetiredCommandPod(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := playerWithDisplay()
	retired := idlePodName(player.Metadata.Name) + retiredIdleCommandPodSuffix
	cluster.pods[retired] = &Pod{
		Metadata: ObjectMeta{Name: retired, Namespace: player.Metadata.Namespace},
	}

	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))

	mustMatchAll(t, idleObjects(cluster), []string{
		"claim/theater-idle-devices", "pod/theater-idle"})
}

// The pods watch does not see the retired command pod, so the operator
// reads it from the API server once for each Player and not on every
// pass.
func TestReconcileIdleReadsTheRetiredPodOnceWhenNoneStands(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	player := playerWithDisplay()
	retired := idlePodName(player.Metadata.Name) + retiredIdleCommandPodSuffix

	mustSucceed(t, media.reconcileIdle(player, "America/New_York", nil))
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
