package main

// These tests read the screen pod the operator would send to the API
// server, and what a pass does with the pod that stands. The pod is the
// whole of what a delegated Player becomes at run time, so what it
// carries is worth reading field by field: the arguments that name the
// catalog and every library root, the mounts behind them, and the
// display claim the browser draws through.

import (
	"net/http"
	"strings"
	"testing"
)

// DenScreen is the Player these tests start from: a screen media-operator
// delegated to this operator, with the claim and the requests it published.
func denScreen() *Player {
	return &Player{
		Metadata: ObjectMeta{Name: "den-tv", Namespace: testLibraryNamespace, UID: "den-tv-uid"},
		Status: PlayerStatus{Idle: &PlayerIdleStatus{
			Controller: screenController,
			Claim:      "den-tv-idle-devices",
			Requests:   []string{"draw", "render"},
		}},
	}
}

// HouseLibraries is the namespace's two libraries, out of name order, so
// a test reads the order the pod puts them in rather than the order they
// arrived in.
func houseLibraries() []Library {
	return []Library{
		{
			Metadata: ObjectMeta{Name: "shows", Namespace: testLibraryNamespace},
			Spec: LibrarySpec{
				Storage: LibraryStorage{Claim: "shows-volume", Root: "/"},
				Kind:    libraryKindSeries,
			},
		},
		{
			Metadata: ObjectMeta{Name: "films", Namespace: testLibraryNamespace},
			Spec: LibrarySpec{
				Storage: LibraryStorage{Claim: "films-volume", Root: "/exports/films"},
				Kind:    libraryKindMovies,
			},
		},
	}
}

func testScreenPod(player *Player, libraries []Library) *Pod {
	return buildScreenPod(player, libraries, testBrowserImage, testCorrosionImage, defaultTopicBase)
}

// The pod's name, namespace, owner, and marks are what tie it to the
// Player: the owner reference is the whole teardown, and the labels are
// what a person's kubectl and this operator's own list select on.
func TestScreenPodBelongsToItsPlayer(t *testing.T) {
	pod := testScreenPod(denScreen(), houseLibraries())

	if pod.Metadata.Name != "den-tv-media-browser" {
		t.Errorf("name = %q, want den-tv-media-browser", pod.Metadata.Name)
	}
	if pod.Metadata.Namespace != testLibraryNamespace {
		t.Errorf("namespace = %q, want %s", pod.Metadata.Namespace, testLibraryNamespace)
	}
	if len(pod.Metadata.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences = %v, want one", pod.Metadata.OwnerReferences)
	}
	owner := pod.Metadata.OwnerReferences[0]
	want := OwnerReference{
		APIVersion: playerAPIVersion, Kind: "Player",
		Name: "den-tv", UID: "den-tv-uid", Controller: true,
	}
	if owner != want {
		t.Errorf("owner = %+v, want %+v", owner, want)
	}
	if pod.Metadata.Labels[scannerLabelKey] != screenLabelValue {
		t.Errorf("labels = %v, want the screen name label", pod.Metadata.Labels)
	}
	if pod.Metadata.Labels[playerLabelKey] != "den-tv" {
		t.Errorf("labels = %v, want the player label", pod.Metadata.Labels)
	}
}

// A screen is a standing service, so the pod restarts in place, and the
// grace period is the scanner pod's, long enough for a busy catalog
// agent to finish its exit. The pod holds no ServiceAccount token,
// because nothing in it speaks to the API server.
func TestScreenPodStandsAndStopsSlowly(t *testing.T) {
	pod := testScreenPod(denScreen(), houseLibraries())

	if pod.Spec.RestartPolicy != "Always" {
		t.Errorf("restartPolicy = %q, want Always", pod.Spec.RestartPolicy)
	}
	if pod.Spec.TerminationGracePeriodSeconds == nil {
		t.Fatal("terminationGracePeriodSeconds is unset")
	}
	if *pod.Spec.TerminationGracePeriodSeconds != scannerGracePeriod {
		t.Errorf("terminationGracePeriodSeconds = %d, want %d",
			*pod.Spec.TerminationGracePeriodSeconds, scannerGracePeriod)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Error("automountServiceAccountToken is not false; the browser holds no credential")
	}
}

// The browser reads the sidecar's own file and its loopback API, and it
// takes one library root per Library in the namespace, in name order.
func TestScreenPodBrowserReadsTheCatalogAndEveryLibraryRoot(t *testing.T) {
	pod := testScreenPod(denScreen(), houseLibraries())

	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("containers = %d, want the browser alone", len(pod.Spec.Containers))
	}
	browser := pod.Spec.Containers[0]
	if browser.Name != browserContainer {
		t.Errorf("container = %q, want %s", browser.Name, browserContainer)
	}
	if browser.Image != testBrowserImage {
		t.Errorf("image = %q, want %q", browser.Image, testBrowserImage)
	}
	want := "--catalog /var/lib/corrosion/state.db --updates http://127.0.0.1:8080 " +
		"--library-root house/films=/libraries/films/exports/films " +
		"--library-root house/shows=/libraries/shows"
	if got := strings.Join(browser.Args, " "); got != want {
		t.Errorf("args = %q,\nwant %q", got, want)
	}
}

// The browser reads the agent's database file itself, so the catalog
// volume reaches the browser container as well as the agent, at the
// path its --catalog argument names.
func TestScreenPodBrowserMountsTheCatalogFile(t *testing.T) {
	pod := testScreenPod(denScreen(), houseLibraries())
	browser := pod.Spec.Containers[0]

	for _, mount := range browser.VolumeMounts {
		if mount.Name == catalogVolumeName && mount.MountPath == catalogStatePath && !mount.ReadOnly {
			return
		}
	}
	t.Errorf("the browser mounts %+v, want %s at %s", browser.VolumeMounts, catalogVolumeName, catalogStatePath)
}

// A namespace with no Library still stands a screen: the browser draws
// the wall the catalog holds, and it mounts the catalog and no media
// volume.
func TestScreenPodWithNoLibrariesMountsNone(t *testing.T) {
	pod := testScreenPod(denScreen(), nil)

	browser := pod.Spec.Containers[0]
	if len(browser.VolumeMounts) != 1 || browser.VolumeMounts[0].Name != catalogVolumeName {
		t.Errorf("volumeMounts = %+v, want the catalog alone", browser.VolumeMounts)
	}
	if strings.Contains(strings.Join(browser.Args, " "), "--library-root") {
		t.Errorf("args = %v, want no library root", browser.Args)
	}
	if len(pod.Spec.Volumes) != 1 || pod.Spec.Volumes[0].Name != catalogVolumeName {
		t.Errorf("volumes = %+v, want the catalog volume alone", pod.Spec.Volumes)
	}
}

// Every Library's claim is mounted read-only under its own directory,
// and the catalog agent's state is an emptyDir it rebuilds from its
// peers.
func TestScreenPodMountsEveryLibraryReadOnly(t *testing.T) {
	pod := testScreenPod(denScreen(), houseLibraries())

	mounts := map[string]VolumeMount{}
	for _, mount := range pod.Spec.Containers[0].VolumeMounts {
		mounts[mount.Name] = mount
	}
	for name, path := range map[string]string{
		"library-films": "/libraries/films",
		"library-shows": "/libraries/shows",
	} {
		mount, mounted := mounts[name]
		if !mounted {
			t.Fatalf("volumeMounts = %+v, want one named %s", pod.Spec.Containers[0].VolumeMounts, name)
		}
		if mount.MountPath != path || !mount.ReadOnly {
			t.Errorf("mount = %+v, want %s read-only", mount, path)
		}
	}

	volumes := map[string]Volume{}
	for _, volume := range pod.Spec.Volumes {
		volumes[volume.Name] = volume
	}
	films := volumes["library-films"]
	if films.PersistentVolumeClaim == nil || films.PersistentVolumeClaim.ClaimName != "films-volume" {
		t.Errorf("volume = %+v, want the films library's claim", films)
	}
	if !films.PersistentVolumeClaim.ReadOnly {
		t.Error("the library volume is not read-only")
	}
	catalog := volumes[catalogVolumeName]
	if catalog.EmptyDir == nil || catalog.PersistentVolumeClaim != nil {
		t.Errorf("catalog volume = %+v, want an emptyDir", catalog)
	}
}

// The pod holds the claim media-operator stood, and the browser takes
// one request of it per name in the Player's status.
func TestScreenPodHoldsTheDisplayClaim(t *testing.T) {
	pod := testScreenPod(denScreen(), houseLibraries())

	want := []PodResourceClaim{{Name: displayClaimName, ResourceClaimName: "den-tv-idle-devices"}}
	if len(pod.Spec.ResourceClaims) != 1 || pod.Spec.ResourceClaims[0] != want[0] {
		t.Errorf("resourceClaims = %+v, want %+v", pod.Spec.ResourceClaims, want)
	}
	claims := pod.Spec.Containers[0].Resources.Claims
	if len(claims) != 2 {
		t.Fatalf("claims = %+v, want one per request", claims)
	}
	if claims[0] != (ResourceClaim{Name: displayClaimName, Request: "draw"}) {
		t.Errorf("claims[0] = %+v, want the draw request", claims[0])
	}
	if claims[1] != (ResourceClaim{Name: displayClaimName, Request: "render"}) {
		t.Errorf("claims[1] = %+v, want the render request", claims[1])
	}
}

// A Player with one request takes one, because media-operator names
// render only for a Player whose display claim holds one.
func TestScreenPodTakesTheRequestsThePlayerNames(t *testing.T) {
	player := denScreen()
	player.Status.Idle.Requests = []string{"draw"}

	claims := testScreenPod(player, nil).Spec.Containers[0].Resources.Claims

	if len(claims) != 1 || claims[0].Request != "draw" {
		t.Errorf("claims = %+v, want the draw request alone", claims)
	}
}

// The browser arms its window watchdog from the environment, and it
// runs with no capability, as both containers of the scanner pod do.
func TestScreenPodBrowserArmsTheWatchdogAndRunsUnprivileged(t *testing.T) {
	pod := testScreenPod(denScreen(), houseLibraries())

	browser := pod.Spec.Containers[0]
	if len(browser.Env) != 1 || browser.Env[0].Name != windowGraceVariable {
		t.Fatalf("env = %+v, want the window grace alone", browser.Env)
	}
	if browser.Env[0].Value != "15" {
		t.Errorf("%s = %q, want 15", windowGraceVariable, browser.Env[0].Value)
	}
	if browser.SecurityContext == nil || browser.SecurityContext.AllowPrivilegeEscalation == nil ||
		*browser.SecurityContext.AllowPrivilegeEscalation {
		t.Errorf("securityContext = %+v, want privilege escalation refused", browser.SecurityContext)
	}
}

// The catalog agent is the same native sidecar the scanner pod runs,
// with the same probes, so the browser starts against an API that is
// already listening.
func TestScreenPodRunsTheSameCatalogSidecar(t *testing.T) {
	pod := testScreenPod(denScreen(), houseLibraries())

	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("initContainers = %d, want the catalog sidecar", len(pod.Spec.InitContainers))
	}
	sidecar := pod.Spec.InitContainers[0]
	if sidecar.Name != catalogContainer || sidecar.Image != testCorrosionImage {
		t.Errorf("sidecar = %q on %q, want the catalog agent", sidecar.Name, sidecar.Image)
	}
	if sidecar.RestartPolicy != "Always" || sidecar.StartupProbe == nil {
		t.Errorf("sidecar = %+v, want a native sidecar with a startup probe", sidecar)
	}
	if len(sidecar.VolumeMounts) != 1 || sidecar.VolumeMounts[0].MountPath != catalogStatePath {
		t.Errorf("volumeMounts = %+v, want the catalog state directory", sidecar.VolumeMounts)
	}
}

// A pass stands one pod per delegated Player, stamped with the template
// hash a later pass compares against, and it mounts the Libraries of
// that Player's namespace and no other.
func TestReconcileScreensStandsAPodForADelegatedPlayer(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)
	operator := testOperator(t, cluster)

	operator.reconcileScreens(t.Context(), testLibraryNamespace,
		[]Player{*cluster.players["den-tv"]}, []Library{*cluster.libraries["movies"]}, nil)

	pod := cluster.heldPod("den-tv-media-browser")
	if pod == nil {
		t.Fatal("no screen pod was created")
	}
	if pod.Metadata.Annotations[templateHashAnnotation] == "" {
		t.Error("the pod carries no template hash")
	}
	if got := strings.Join(pod.Spec.Containers[0].Args, " "); !strings.Contains(got, "--library-root house/movies=") {
		t.Errorf("args = %q, want the namespace's library", got)
	}
}

// A Player that names another idle controller, and one that names none
// at all, get no pod, and the pod standing for one is deleted. That
// delete is the switch away, and it is the only delete this pass sends.
func TestReconcileScreensStopsThePodOfAPlayerItNoLongerServes(t *testing.T) {
	cases := []struct {
		name       string
		controller string
	}{
		{name: "another controller", controller: "media.liken.sh/idle-screen"},
		{name: "no idle block", controller: ""},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			player := seedPlayer(cluster, "den-tv", testLibraryNamespace, one.controller)
			cluster.pods["den-tv-media-browser"] = &Pod{
				Metadata: ObjectMeta{Name: "den-tv-media-browser", Namespace: testLibraryNamespace},
			}

			testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace,
				[]Player{*player}, nil, []Pod{*cluster.pods["den-tv-media-browser"]})

			if cluster.heldPod("den-tv-media-browser") != nil {
				t.Error("the screen pod still stands for a Player this operator does not serve")
			}
		})
	}
}

// A Player this operator does not serve costs the pass nothing when no
// pod stands for it, so a cluster of undelegated units sends no delete
// on every pass.
func TestReconcileScreensSendsNoDeleteWhenNoPodStands(t *testing.T) {
	cluster := newFakeCluster()
	player := seedPlayer(cluster, "den-tv", testLibraryNamespace, "media.liken.sh/idle-screen")

	testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace,
		[]Player{*player}, nil, nil)

	if got := cluster.countRequests(http.MethodDelete, "pods"); got != 0 {
		t.Errorf("the pass sent %d deletes for a Player with no pod", got)
	}
}

// A pod built from a different template is stale, so the pass deletes
// it, and the pass after that creates the replacement.
func TestReconcileScreensReplacesAStalePod(t *testing.T) {
	cluster := newFakeCluster()
	player := seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)
	stale := testScreenPod(player, nil)
	stale.Metadata.Annotations = map[string]string{templateHashAnnotation: "an-older-template"}
	cluster.pods["den-tv-media-browser"] = stale
	operator := testOperator(t, cluster)

	operator.reconcileScreens(t.Context(), testLibraryNamespace, []Player{*player}, nil, nil)

	if cluster.heldPod("den-tv-media-browser") != nil {
		t.Fatal("the stale pod still stands")
	}

	operator.reconcileScreens(t.Context(), testLibraryNamespace, []Player{*player}, nil, nil)

	replacement := cluster.heldPod("den-tv-media-browser")
	if replacement == nil {
		t.Fatal("no replacement pod was created")
	}
	if replacement.Metadata.Annotations[templateHashAnnotation] == "an-older-template" {
		t.Error("the replacement carries the stale hash")
	}
}

// A pod that matches the template is left as it stands: the pass reads
// it and writes nothing.
func TestReconcileScreensKeepsAMatchingPod(t *testing.T) {
	cluster := newFakeCluster()
	player := seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)
	operator := testOperator(t, cluster)
	operator.reconcileScreens(t.Context(), testLibraryNamespace, []Player{*player}, nil, nil)

	operator.reconcileScreens(t.Context(), testLibraryNamespace, []Player{*player}, nil, nil)

	if cluster.countRequests(http.MethodDelete, "pods") != 0 {
		t.Error("the pass deleted a pod that matched the template")
	}
	if cluster.countRequests(http.MethodPost, "pods") != 1 {
		t.Error("the pass created a second pod")
	}
}

// A pass over one namespace reads the Players and the Libraries of that
// namespace alone, so a screen in one house never mounts another's
// volumes.
func TestReconcileScreensReadsOneNamespace(t *testing.T) {
	cluster := newFakeCluster()
	house := seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)
	studio := seedPlayer(cluster, "studio-tv", "studio", screenController)
	libraries := []Library{
		{
			Metadata: ObjectMeta{Name: "films", Namespace: testLibraryNamespace},
			Spec:     LibrarySpec{Storage: LibraryStorage{Claim: "films-volume", Root: "/"}},
		},
		{
			Metadata: ObjectMeta{Name: "shows", Namespace: "studio"},
			Spec:     LibrarySpec{Storage: LibraryStorage{Claim: "shows-volume", Root: "/"}},
		},
	}

	testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace,
		[]Player{*house, *studio}, libraries, nil)

	if cluster.heldPod("studio-tv-media-browser") != nil {
		t.Error("the pass stood a pod for a Player in another namespace")
	}
	pod := cluster.heldPod("den-tv-media-browser")
	if pod == nil {
		t.Fatal("no screen pod was created")
	}
	if got := strings.Join(pod.Spec.Containers[0].Args, " "); strings.Contains(got, "studio/shows") {
		t.Errorf("args = %q, want no library from another namespace", got)
	}
}

// A failure on one Player is reported and the pass carries on, so one
// broken screen does not hold up another room's.
func TestReconcileScreensCarriesOnPastAFailure(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		controller string
	}{
		{
			name: "the pod cannot be read", controller: screenController,
			path: "/api/v1/namespaces/house/pods/den-tv-media-browser",
		},
		{
			name: "the pod cannot be deleted", controller: "media.liken.sh/idle-screen",
			path: "/api/v1/namespaces/house/pods/den-tv-media-browser",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			broken := seedPlayer(cluster, "den-tv", testLibraryNamespace, one.controller)
			standing := seedPlayer(cluster, "kitchen-tv", testLibraryNamespace, screenController)
			cluster.broken[one.path] = http.StatusInternalServerError

			testOperator(t, cluster).reconcileScreens(t.Context(), testLibraryNamespace,
				[]Player{*broken, *standing}, nil, nil)

			if cluster.heldPod("kitchen-tv-media-browser") == nil {
				t.Error("the pass stopped at the broken Player")
			}
		})
	}
}

// The namespaces are read from the Players themselves, in name order,
// so a pass reconciles each namespace once however many screens it
// holds.
func TestScreenNamespacesAreEveryPlayersNamespaceInOrder(t *testing.T) {
	players := []Player{
		{Metadata: ObjectMeta{Name: "studio-tv", Namespace: "studio"}},
		{Metadata: ObjectMeta{Name: "den-tv", Namespace: testLibraryNamespace}},
		{Metadata: ObjectMeta{Name: "kitchen-tv", Namespace: testLibraryNamespace}},
	}

	got := screenNamespaces(players)

	if len(got) != 2 || got[0] != testLibraryNamespace || got[1] != "studio" {
		t.Errorf("namespaces = %v, want house and studio", got)
	}
	if len(screenNamespaces(nil)) != 0 {
		t.Errorf("namespaces = %v, want none", screenNamespaces(nil))
	}
}

// The bus block on the status reaches the browser as the variables
// the media-screen crate reads, so a remote's presses arrive on that
// controller's events topic and the browser runs the two windows itself.
func denScreenOnTheBus() *Player {
	player := denScreen()
	player.Status.Idle.FadeAfterSeconds = 600
	player.Status.Idle.OffAfterSeconds = 1800
	player.Status.Idle.Bus = &PlayerIdleBus{
		Address:       "bus.liken-system.svc:1883",
		StatusTopic:   "liken/media/players/house/den-tv/status",
		VolumeTopic:   "liken/media/players/house/den-tv/volume",
		CommandsTopic: "liken/media/players/house/den-tv/commands",
		PanelTopic:    "liken/media/players/house/den-tv/panel",
		Remotes: []PlayerIdleRemote{
			{
				Events: "liken/media/remotes/house/sofa/events",
				Focus:  "liken/media/remotes/house/sofa/focus",
			},
			{
				Events: "liken/media/remotes/house/armchair/events",
				Focus:  "liken/media/remotes/house/armchair/focus",
			},
		},
	}
	return player
}

// The browser container's environment, by name.
func browserEnvironment(player *Player) map[string]string {
	environment := map[string]string{}
	for _, variable := range testScreenPod(player, houseLibraries()).Spec.Containers[0].Env {
		environment[variable.Name] = variable.Value
	}
	return environment
}

func TestScreenPodBrowserTakesTheBusThePlayerPublishes(t *testing.T) {
	environment := browserEnvironment(denScreenOnTheBus())

	want := map[string]string{
		windowGraceVariable:        windowGraceSeconds,
		mediaBusAddressVariable:    "bus.liken-system.svc:1883",
		mediaPlayerNameVariable:    "den-tv",
		mediaStatusTopicVariable:   "liken/media/players/house/den-tv/status",
		mediaVolumeTopicVariable:   "liken/media/players/house/den-tv/volume",
		mediaCommandsTopicVariable: "liken/media/players/house/den-tv/commands",
		mediaPanelTopicVariable:    "liken/media/players/house/den-tv/panel",
		mediaRemoteEventsTopicsVariable: "liken/media/remotes/house/sofa/events\n" +
			"liken/media/remotes/house/armchair/events",
		mediaRemoteFocusTopicsVariable: "liken/media/remotes/house/sofa/focus\n" +
			"liken/media/remotes/house/armchair/focus",
		idleFadeAfterSecondsVariable: "600",
		idleOffAfterSecondsVariable:  "1800",
		libraryPlayTopicVariable:     "liken/library/players/house/den-tv/play",
	}
	for name, value := range want {
		if environment[name] != value {
			t.Errorf("%s = %q, want %q", name, environment[name], value)
		}
	}
	if len(environment) != len(want) {
		t.Errorf("env = %v, want %d variables", environment, len(want))
	}
}

// Both windows are set whatever they hold, because zero is a policy:
// no fade, and a panel that stays lit.
func TestScreenPodStatesBothWindowsEvenAtZero(t *testing.T) {
	player := denScreenOnTheBus()
	player.Status.Idle.FadeAfterSeconds = 0
	player.Status.Idle.OffAfterSeconds = 0

	environment := browserEnvironment(player)

	if environment[idleFadeAfterSecondsVariable] != "0" {
		t.Errorf("fade = %q, want 0", environment[idleFadeAfterSecondsVariable])
	}
	if environment[idleOffAfterSecondsVariable] != "0" {
		t.Errorf("off = %q, want 0", environment[idleOffAfterSecondsVariable])
	}
}

// The volume topic is the speaker gate, so a unit with no sinks
// carries no variable at all.
func TestScreenPodWithNoSinksNamesNoVolumeTopic(t *testing.T) {
	player := denScreenOnTheBus()
	player.Status.Idle.Bus.VolumeTopic = ""

	environment := browserEnvironment(player)

	if _, set := environment[mediaVolumeTopicVariable]; set {
		t.Errorf("env = %v, want no volume topic", environment)
	}
}

// A unit with no controllers carries neither list, because an empty
// list and one empty line are not the same thing to the crate.
func TestScreenPodWithNoRemotesNamesNeitherList(t *testing.T) {
	player := denScreenOnTheBus()
	player.Status.Idle.Bus.Remotes = nil

	environment := browserEnvironment(player)

	for _, name := range []string{
		mediaRemoteEventsTopicsVariable, mediaRemoteFocusTopicsVariable,
	} {
		if _, set := environment[name]; set {
			t.Errorf("env = %v, want no %s", environment, name)
		}
	}
}

// A controller with no focus topic contributes an empty line, so the
// two lists stay paired by position.
func TestScreenPodKeepsTheRemoteListsPairedByPosition(t *testing.T) {
	player := denScreenOnTheBus()
	player.Status.Idle.Bus.Remotes[0].Focus = ""

	environment := browserEnvironment(player)

	if environment[mediaRemoteFocusTopicsVariable] !=
		"\nliken/media/remotes/house/armchair/focus" {
		t.Errorf("focus topics = %q, want an empty first line",
			environment[mediaRemoteFocusTopicsVariable])
	}
}

// A Player under an older media-operator publishes no bus block,
// and its browser opens no connection and takes the keyboard alone.
func TestScreenPodWithNoBusTakesTheKeyboardAlone(t *testing.T) {
	environment := browserEnvironment(denScreen())

	if len(environment) != 1 || environment[windowGraceVariable] != windowGraceSeconds {
		t.Errorf("env = %v, want the window grace alone", environment)
	}
}
