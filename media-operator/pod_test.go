package main

// These tests cover what a Play becomes at run time: one pod that runs
// mpv on the resolved list, holds the claim's every role, and carries
// the command sidecar that owns the mpv socket and reads every
// controller the unit owns.

import (
	"encoding/json"
	"reflect"
	"testing"
)

// initContainer finds one of the pod's init containers by name.
func initContainer(t *testing.T, pod *Pod, name string) Container {
	t.Helper()
	for _, container := range pod.Spec.InitContainers {
		if container.Name == name {
			return container
		}
	}
	t.Fatalf("the pod has no init container named %q: %+v", name, pod.Spec.InitContainers)
	return Container{}
}

// envValue reads one environment variable off a container.
func envValue(container Container, name string) string {
	for _, variable := range container.Env {
		if variable.Name == name {
			return variable.Value
		}
	}
	return ""
}

// mountsIPC reports whether a container mounts the shared IPC volume.
func mountsIPC(container Container) bool {
	for _, mount := range container.VolumeMounts {
		if mount.Name == ipcVolumeName {
			return true
		}
	}
	return false
}

const (
	testPlayerImage = "ghcr.io/liken-sh/media-operator-player:test"
	// The image every sidecar container runs. It is a different name
	// from the player image, so a pod that confuses the two fails a
	// test.
	testSidecarImage = "ghcr.io/liken-sh/media-operator-sidecar:test"
	testBusAddress   = "bus.media.svc:1883"
	testTopicBase    = "liken/media"
)

// One playlist that costs a volume, so the pod under test carries a
// mount as well as arguments.
func testResolution(t *testing.T) resolution {
	t.Helper()
	resolved, err := resolvePlay(mediaItems(
		"https://films.example/trailer.mkv",
		"nfs://nas.example/export/films/film.mkv",
	))
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func testPod(t *testing.T) *Pod {
	t.Helper()
	play := testPlay()
	claim := buildClaim(play, testPlayer())
	return buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testBusAddress, testTopicBase, nil, resolvedPreferences{})
}

// The same pod, with two controllers bound to the player.
func testPodWithRemotes(t *testing.T) *Pod {
	t.Helper()
	play := testPlay()
	claim := buildClaim(play, testPlayer())
	return buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testBusAddress, testTopicBase, testBoundRemotes(), resolvedPreferences{})
}

// restartPolicy is Never, because a finished film is not a failure to
// restart, and the Play owns the pod so deleting the Play takes it
// away.
func TestBuildPodNamesThePodForThePlayThatOwnsIt(t *testing.T) {
	pod := testPod(t)

	if pod.APIVersion != podAPIVersion || pod.Kind != "Pod" {
		t.Errorf("apiVersion = %q, kind = %q", pod.APIVersion, pod.Kind)
	}
	if pod.Metadata.Name != "movie-playback" {
		t.Errorf("name = %q, want movie-playback", pod.Metadata.Name)
	}
	if pod.Metadata.Namespace != "house" {
		t.Errorf("namespace = %q, want house", pod.Metadata.Namespace)
	}
	if pod.Spec.RestartPolicy != "Never" {
		t.Errorf("restartPolicy = %q, want Never", pod.Spec.RestartPolicy)
	}
	if pod.Spec.TerminationGracePeriodSeconds == nil {
		t.Fatal("the pod states no termination grace period")
	}
	if got := *pod.Spec.TerminationGracePeriodSeconds; got != 5 {
		t.Errorf("terminationGracePeriodSeconds = %d, want 5", got)
	}
	owners := []OwnerReference{{
		APIVersion: mediaAPIVersion,
		Kind:       "Play",
		Name:       "movie",
		UID:        "play-1",
		Controller: true,
	}}
	if !reflect.DeepEqual(pod.Metadata.OwnerReferences, owners) {
		t.Errorf("ownerReferences = %+v, want %+v", pod.Metadata.OwnerReferences, owners)
	}
}

// mpv is the pod's own process, so the player container carries only
// the resolved list and, with no declared start, no environment.
func TestBuildPodRunsThePlayerOnTheResolvedList(t *testing.T) {
	pod := testPod(t)

	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("containers = %+v, want one", pod.Spec.Containers)
	}
	container := pod.Spec.Containers[0]
	if container.Name != "player" {
		t.Errorf("name = %q, want player", container.Name)
	}
	if container.Image != testPlayerImage {
		t.Errorf("image = %q, want %q", container.Image, testPlayerImage)
	}
	args := []string{"https://films.example/trailer.mkv", "/media/1/film.mkv"}
	if !reflect.DeepEqual(container.Args, args) {
		t.Errorf("args = %v, want %v", container.Args, args)
	}
	// The blocks travel on the player container because the shim reads them
	// to expand a music album, so every run carries them.
	env := []EnvVar{{Name: presentationsVariable, Value: "[{}]"}}
	if !reflect.DeepEqual(container.Env, env) {
		t.Errorf("env = %+v, want %+v", container.Env, env)
	}
}

// A declared start reaches the player container as one variable, beside the
// blocks every run carries.
func TestBuildPodCarriesTheDeclaredStart(t *testing.T) {
	play := testPlay()
	play.Spec.Start = "0:10:00"
	claim := buildClaim(play, testPlayer())
	pod := buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testBusAddress, testTopicBase, nil, resolvedPreferences{})

	env := pod.Spec.Containers[0].Env
	want := []EnvVar{
		{Name: presentationsVariable, Value: "[{}]"},
		{Name: playStartVariable, Value: "0:10:00"},
	}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("env = %+v, want %+v", env, want)
	}
}

// A playback pod arms no window watchdog. A Play on an
// audio-only unit expects no window at all, and an exit there would
// kill a run that is playing sound correctly.
func TestBuildPodArmsNoWindowWatchdog(t *testing.T) {
	pod := testPod(t)

	for _, entry := range pod.Spec.Containers[0].Env {
		if entry.Name == idleWindowGraceVariable {
			t.Errorf("the player container carries %s", idleWindowGraceVariable)
		}
	}
}

// The pod names the claim once and the player container repeats that
// name for each role, because the playback claim holds the player's
// roles alone.
func TestBuildPodHoldsEveryRequestTheClaimAsksFor(t *testing.T) {
	pod := testPod(t)

	claims := []PodResourceClaim{{Name: "devices", ResourceClaimName: "movie-devices"}}
	if !reflect.DeepEqual(pod.Spec.ResourceClaims, claims) {
		t.Errorf("resourceClaims = %+v, want %+v", pod.Spec.ResourceClaims, claims)
	}
	held := []ContainerClaim{
		{Name: "devices", Request: "screen"},
		{Name: "devices", Request: "audio0"},
		{Name: "devices", Request: "audio1"},
		{Name: "devices", Request: "render"},
	}
	if got := pod.Spec.Containers[0].Resources.Claims; !reflect.DeepEqual(got, held) {
		t.Errorf("resources.claims = %+v, want %+v", got, held)
	}
}

// The volume belongs to the pod and the mount belongs to the
// container, so the resolution splits across the two. The IPC volume
// follows the media in both lists.
func TestBuildPodCarriesTheResolvedVolumesAndMounts(t *testing.T) {
	resolved := testResolution(t)
	play := testPlay()
	pod := buildPod(play, buildClaim(play, testPlayer()), resolved, testPlayerImage, testSidecarImage, testBusAddress, testTopicBase, nil, resolvedPreferences{})

	volumes := append(append([]Volume{}, resolved.Volumes...),
		Volume{Name: "art", EmptyDir: &EmptyDirVolumeSource{SizeLimit: artSizeLimit}},
		Volume{Name: "ipc", EmptyDir: &EmptyDirVolumeSource{}})
	if !reflect.DeepEqual(pod.Spec.Volumes, volumes) {
		t.Errorf("volumes = %+v, want %+v", pod.Spec.Volumes, volumes)
	}
	mounts := append(append([]VolumeMount{}, resolved.Mounts...),
		VolumeMount{Name: "art", MountPath: "/art"},
		VolumeMount{Name: "ipc", MountPath: "/ipc"})
	if got := pod.Spec.Containers[0].VolumeMounts; !reflect.DeepEqual(got, mounts) {
		t.Errorf("volumeMounts = %+v, want %+v", got, mounts)
	}
	// The resolution keeps what it resolved; the pod builder appends
	// into a copy.
	if len(resolved.Mounts) != 1 || len(resolved.Volumes) != 1 {
		t.Errorf("the builder wrote into the resolution: %+v", resolved)
	}
}

// A pod with no remotes still carries the IPC volume, because mpv serves
// its socket at one path either way, and its only sidecar is the command
// sidecar.
func TestBuildPodWithNoRemotesCarriesOnlyTheCommandSidecar(t *testing.T) {
	pod := testPod(t)

	last := pod.Spec.Volumes[len(pod.Spec.Volumes)-1]
	want := Volume{Name: "ipc", EmptyDir: &EmptyDirVolumeSource{}}
	if !reflect.DeepEqual(last, want) {
		t.Fatalf("volume = %+v, want %+v", last, want)
	}
	written, err := json.Marshal(last)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != `{"name":"ipc","emptyDir":{}}` {
		t.Errorf("volume = %s", written)
	}
	if len(pod.Spec.InitContainers) != 1 || pod.Spec.InitContainers[0].Name != commandContainer {
		t.Errorf("initContainers = %+v, want one command sidecar", pod.Spec.InitContainers)
	}
}

// The command sidecar is the sidecar image in its command mode. It
// holds no device claim, and it mounts the IPC socket, the art volume,
// and the same media mounts the player holds, so it can open the source
// art. It carries the play's identity, the bus, and the base.
func TestBuildPodRunsOneCommandSidecar(t *testing.T) {
	pod := testPod(t)

	command := initContainer(t, pod, commandContainer)
	want := Container{
		Name:    commandContainer,
		Image:   testSidecarImage,
		Command: []string{"/media-operator", "command"},
		Env: []EnvVar{
			{Name: playNamespaceVariable, Value: "house"},
			{Name: playNameVariable, Value: "movie"},
			{Name: busAddressVariable, Value: testBusAddress},
			{Name: topicBaseVariable, Value: testTopicBase},
			{Name: presentationsVariable, Value: "[{}]"},
			{Name: trickplayIntervalVariable, Value: defaultTrickplayInterval},
			{Name: playerNameVariable, Value: "theater"},
			{Name: playerVolumeTopicVariable, Value: playerVolumeTopic(testTopicBase, "house", "theater")},
		},
		VolumeMounts: append([]VolumeMount{{Name: "ipc", MountPath: "/ipc"}, {Name: "art", MountPath: "/art"}},
			testResolution(t).Mounts...),
		RestartPolicy: "Always",
	}
	if !reflect.DeepEqual(command, want) {
		t.Errorf("command = %+v, want %+v", command, want)
	}
	if len(command.Resources.Claims) != 0 {
		t.Errorf("the command sidecar holds a device claim: %+v", command.Resources.Claims)
	}
}

// The command sidecar carries every item's presentation block as one JSON
// array in item order. An item with no presentation is an empty object, and
// an item with a presentation is its block, so the sidecar forwards index i
// for playlist-pos i.
func TestBuildPodBakesThePresentationBlocks(t *testing.T) {
	play := testPlay()
	play.Spec.Items = []PlayItem{
		{URI: "https://films.example/loose.mkv"},
		{
			URI: "nfs://nas.example/export/shows/ep.mkv",
			Presentation: &Presentation{
				Type:         "video",
				Hint:         "series",
				Series:       "The Show",
				Season:       2,
				Episode:      5,
				EpisodeTitle: "The Pilot",
			},
		},
	}
	claim := buildClaim(play, testPlayer())
	pod := buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testBusAddress, testTopicBase, nil, resolvedPreferences{})

	command := initContainer(t, pod, commandContainer)
	got := envValue(command, presentationsVariable)
	want := `[{},{"type":"video","hint":"series","series":"The Show","season":2,"episode":5,"episodeTitle":"The Pilot"}]`
	if got != want {
		t.Errorf("%s = %s, want %s", presentationsVariable, got, want)
	}
}

// The pod carries one sidecar, and it names every controller the unit
// owns: their events topics and their focus topics, aligned.
func TestBuildPodGivesTheCommandSidecarEveryRemote(t *testing.T) {
	pod := testPodWithRemotes(t)

	names := []string{}
	for _, container := range pod.Spec.InitContainers {
		names = append(names, container.Name)
	}
	if !reflect.DeepEqual(names, []string{commandContainer}) {
		t.Fatalf("init containers = %v, want the command sidecar alone", names)
	}

	command := initContainer(t, pod, commandContainer)
	mustMatch(t, envValue(command, playerNameVariable), "theater")
	mustMatch(t, envValue(command, remoteEventsTopicsVariable),
		"liken/media/remotes/house/armchair/events\nliken/media/remotes/house/sofa/events")
	mustMatch(t, envValue(command, remoteFocusTopicsVariable),
		"liken/media/remotes/house/armchair/focus\nliken/media/remotes/house/sofa/focus")
}

// A Play on a Player that names no controller carries neither list, so
// its sidecar subscribes to no controller at all.
func TestBuildPodCarriesNoRemoteListsWithoutRemotes(t *testing.T) {
	command := initContainer(t, testPod(t), commandContainer)
	mustMatch(t, envValue(command, remoteEventsTopicsVariable), "")
	mustMatch(t, envValue(command, remoteFocusTopicsVariable), "")
}

// The resolved preferences map to mpv flags. A flag rides only for a field that
// resolved, and --subs-match-os-language=no rides when any other flag does.
func TestMpvPreferenceOptions(t *testing.T) {
	cases := []struct {
		name  string
		prefs resolvedPreferences
		want  []string
	}{
		{
			name:  "no preference passes nothing",
			prefs: resolvedPreferences{},
			want:  nil,
		},
		{
			name:  "audio languages become --alang",
			prefs: resolvedPreferences{AudioLanguages: []string{"en", "ja"}},
			want:  []string{"--alang=en,ja", "--subs-match-os-language=no"},
		},
		{
			name:  "subtitle languages become --slang",
			prefs: resolvedPreferences{SubtitleLanguages: []string{"en"}},
			want:  []string{"--slang=en", "--subs-match-os-language=no"},
		},
		{
			name:  "subtitles on shows them over matching audio",
			prefs: resolvedPreferences{Subtitles: subtitlesOn},
			want:  []string{"--sub-visibility=yes", "--subs-with-matching-audio=yes", "--subs-match-os-language=no"},
		},
		{
			name:  "subtitles off loads no subtitle track",
			prefs: resolvedPreferences{Subtitles: subtitlesOff},
			want:  []string{"--sid=no", "--subs-match-os-language=no"},
		},
		{
			name:  "subtitles auto shows them only over other-language audio",
			prefs: resolvedPreferences{Subtitles: subtitlesAuto},
			want:  []string{"--subs-with-matching-audio=no", "--subs-match-os-language=no"},
		},
		{
			name: "every field together maps to every flag",
			prefs: resolvedPreferences{
				AudioLanguages:    []string{"ja"},
				SubtitleLanguages: []string{"en"},
				Subtitles:         subtitlesOn,
			},
			want: []string{
				"--alang=ja", "--slang=en",
				"--sub-visibility=yes", "--subs-with-matching-audio=yes",
				"--subs-match-os-language=no",
			},
		},
		{
			name:  "a stated empty list passes no flag for that field",
			prefs: resolvedPreferences{AudioLanguages: []string{}},
			want:  nil,
		},
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := mpvPreferenceOptions(one.prefs)
			if !reflect.DeepEqual(got, one.want) {
				t.Errorf("options = %v, want %v", got, one.want)
			}
		})
	}
}

// The resolved options reach the player container as one newline-joined
// variable, which the shim splits back into mpv's argv.
func TestBuildPodCarriesTheResolvedOptions(t *testing.T) {
	play := testPlay()
	claim := buildClaim(play, testPlayer())
	prefs := resolvedPreferences{AudioLanguages: []string{"en", "ja"}, Subtitles: subtitlesAuto}
	pod := buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testBusAddress, testTopicBase, nil, prefs)

	got := envValue(pod.Spec.Containers[0], playerOptionsVariable)
	want := "--alang=en,ja\n--subs-with-matching-audio=no\n--subs-match-os-language=no"
	if got != want {
		t.Errorf("%s = %q, want %q", playerOptionsVariable, got, want)
	}
}

// A run with no preferences carries no options variable, so an ordinary pod is
// unchanged.
func TestBuildPodWithNoPreferencesCarriesNoOptions(t *testing.T) {
	pod := testPod(t)
	if got := envValue(pod.Spec.Containers[0], playerOptionsVariable); got != "" {
		t.Errorf("%s = %q, want none", playerOptionsVariable, got)
	}
}

// The level the operator resolved reaches mpv on its command line,
// so the film starts at the level the unit already holds instead of at
// unity. The subscription is the live authority from there.
func TestBuildPodStartsMpvAtTheUnitsLevel(t *testing.T) {
	play := testPlay()
	play.Spec.Volume = &PlayVolume{Level: level(35), Muted: muted(true)}
	pod := buildPod(play, buildClaim(play, testPlayer()), testResolution(t),
		testPlayerImage, testSidecarImage, testBusAddress, testTopicBase, nil, resolvedPreferences{})

	mustMatch(t, envValue(pod.Spec.Containers[0], playerOptionsVariable), "--volume=35\n--mute=yes")
}

// A unit nothing has answered for carries no level onto the pod, so
// mpv keeps its own default and the subscription sets the level a moment
// later.
func TestBuildPodWithNoLevelCarriesNoVolumeOption(t *testing.T) {
	mustMatch(t, envValue(testPod(t).Spec.Containers[0], playerOptionsVariable), "")
}

// The volume topic reaches the command sidecar only for a unit that
// has speakers. The claim answers that: it holds a sink request only for a
// Player that states sinks.
func TestTheCommandSidecarCarriesTheVolumeTopicOnlyWithSpeakers(t *testing.T) {
	speakerless := &Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house"},
		Spec:     PlayerSpec{Display: &PlayerDevice{Class: "display-output"}},
	}
	play := testPlay()
	pod := buildPod(play, buildClaim(play, speakerless), testResolution(t),
		testPlayerImage, testSidecarImage, testBusAddress, testTopicBase, nil, resolvedPreferences{})

	mustMatch(t, envValue(initContainer(t, pod, commandContainer), playerVolumeTopicVariable), "")
	mustMatch(t, envValue(initContainer(t, testPod(t), commandContainer), playerVolumeTopicVariable),
		playerVolumeTopic(testTopicBase, "house", "theater"))
}

// A resolved timezone reaches the player container as TZ, so the display clock
// reads the household's wall-clock zone.
func TestBuildPodCarriesTheResolvedTimeZone(t *testing.T) {
	play := testPlay()
	claim := buildClaim(play, testPlayer())
	prefs := resolvedPreferences{TimeZone: "America/New_York"}
	pod := buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testBusAddress, testTopicBase, nil, prefs)

	got := envValue(pod.Spec.Containers[0], timeZoneVariable)
	if got != "America/New_York" {
		t.Errorf("%s = %q, want %q", timeZoneVariable, got, "America/New_York")
	}
}

// A run with no timezone carries no TZ variable, so an ordinary pod is
// unchanged.
func TestBuildPodWithNoTimeZoneCarriesNoTZ(t *testing.T) {
	pod := testPod(t)
	if got := envValue(pod.Spec.Containers[0], timeZoneVariable); got != "" {
		t.Errorf("%s = %q, want none", timeZoneVariable, got)
	}
}
