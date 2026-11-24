package main

// These tests cover the playback pod that buildPod returns for a Play.

import (
	"encoding/json"
	"reflect"
	"testing"
)

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

func envValue(container Container, name string) string {
	for _, variable := range container.Env {
		if variable.Name == name {
			return variable.Value
		}
	}
	return ""
}

func mountsIPC(container Container) bool {
	for _, mount := range container.VolumeMounts {
		if mount.Name == ipcVolumeName {
			return true
		}
	}
	return false
}

const (
	// Each image has its own name, so a container that runs the wrong
	// image fails a test.
	testPlayerImage  = "ghcr.io/liken-sh/media-operator-player:test"
	testSidecarImage = "ghcr.io/liken-sh/media-operator-sidecar:test"
	testDisplayImage = "ghcr.io/liken-sh/media-operator-display:test"
	testBusAddress   = "bus.media.svc:1883"
	testTopicBase    = "liken/media"
)

// testResolution resolves a playlist with an nfs item, so the pod under
// test has a volume and a mount as well as arguments.
func testResolution(t *testing.T) resolution {
	t.Helper()
	resolved, err := resolvePlay("house", mediaItems(
		"https://films.example/trailer.mkv",
		"nfs://nas.example/export/films/film.mkv",
	), nil, frame{})

	if err != nil {
		t.Fatalf("resolving the test play: %v", err)
	}
	return resolved
}

func testPod(t *testing.T) *Pod {
	t.Helper()
	play := testPlay()
	claim := buildClaim(play, testPlayer())
	return buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase, nil, resolvedPreferences{}, nil, "")
}

// testPodWithRemotes is testPod with two controllers bound to the
// player.
func testPodWithRemotes(t *testing.T) *Pod {
	t.Helper()
	play := testPlay()
	claim := buildClaim(play, testPlayer())
	return buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase, testBoundRemotes(), resolvedPreferences{}, nil, "")
}

// restartPolicy is Never because a film that finished is not a failure
// to restart. The Play owns the pod, so deleting the Play deletes the
// pod.
func TestBuildPodNamesThePodForThePlayThatOwnsIt(t *testing.T) {
	pod := testPod(t)

	if pod.APIVersion != podAPIVersion || pod.Kind != "Pod" {
		t.Errorf("apiVersion = %q, kind = %q, want %q and Pod", pod.APIVersion, pod.Kind, podAPIVersion)
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
		t.Fatal("terminationGracePeriodSeconds is not set, want 5")
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

// The player container runs mpv with only the resolved list as
// arguments. With no start declared, its only environment variable is
// the presentation blocks.
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
	// The shim reads the blocks to expand a music album, so every run
	// has them.
	env := []EnvVar{
		{Name: presentationsVariable, Value: "[{}]"},
		{Name: playerOptionsVariable, Value: houseStyleLines},
	}
	if !reflect.DeepEqual(container.Env, env) {
		t.Errorf("env = %+v, want %+v", container.Env, env)
	}
}

// A declared start is an environment variable of the player container,
// next to the presentation blocks.
func TestBuildPodCarriesTheDeclaredStart(t *testing.T) {
	play := testPlay()
	play.Spec.Start = "0:10:00"
	claim := buildClaim(play, testPlayer())
	pod := buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase, nil, resolvedPreferences{}, nil, "")

	env := pod.Spec.Containers[0].Env
	want := []EnvVar{
		{Name: presentationsVariable, Value: "[{}]"},
		{Name: playStartVariable, Value: "0:10:00"},
		{Name: playerOptionsVariable, Value: houseStyleLines},
	}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("env = %+v, want %+v", env, want)
	}
}

// The operator copies MEDIA_PLAYER_VERBOSE to the player container, so
// one `kubectl set env` on the Deployment turns on mpv's full output in
// every new playback pod.
func TestBuildPodPassesTheVerboseSwitchToThePlayer(t *testing.T) {
	play := testPlay()
	claim := buildClaim(play, testPlayer())
	pod := buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase, nil, resolvedPreferences{}, nil, "1")

	if got := envValue(pod.Spec.Containers[0], playerVerboseVariable); got != "1" {
		t.Errorf("%s = %q, want 1", playerVerboseVariable, got)
	}
}

// An operator with no MEDIA_PLAYER_VERBOSE creates a pod without it, so
// mpv's output stays quiet.
func TestBuildPodCarriesNoVerboseSwitchWhenTheOperatorStatesNone(t *testing.T) {
	pod := testPod(t)

	for _, entry := range pod.Spec.Containers[0].Env {
		if entry.Name == playerVerboseVariable {
			t.Errorf("the player container sets %s, want it unset", playerVerboseVariable)
		}
	}
}

// The player container sets no IDLE_WINDOW_GRACE_SECONDS. A Play on an
// audio-only Player opens no window, and a client that exited for a
// missing window would end a run that plays sound correctly.
func TestBuildPodArmsNoWindowWatchdog(t *testing.T) {
	pod := testPod(t)

	for _, entry := range pod.Spec.Containers[0].Env {
		if entry.Name == idleWindowGraceVariable {
			t.Errorf("the player container sets %s, want it unset", idleWindowGraceVariable)
		}
	}
}

// The pod declares the claim once, and the player container lists one
// resources.claims entry for each request of the claim.
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

// The playback pod tolerates the player-node taint only for the
// NoSchedule effect, so a NoExecute taint still evicts it.
func TestBuildPodToleratesThePlayerNodeTaint(t *testing.T) {
	pod := testPod(t)

	want := []Toleration{{Key: "media.liken.sh/player", Operator: "Exists", Effect: "NoSchedule"}}
	if !reflect.DeepEqual(pod.Spec.Tolerations, want) {
		t.Errorf("tolerations = %+v, want %+v", pod.Spec.Tolerations, want)
	}
}

// The resolution's volumes go in the pod spec and its mounts go in the
// container. The IPC volume and mount come after the media in both
// lists.
func TestBuildPodCarriesTheResolvedVolumesAndMounts(t *testing.T) {
	resolved := testResolution(t)
	play := testPlay()
	pod := buildPod(play, buildClaim(play, testPlayer()), resolved, testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase, nil, resolvedPreferences{}, nil, "")

	volumes := append(append([]Volume{}, resolved.Volumes...),
		Volume{Name: "ipc", EmptyDir: &EmptyDirVolumeSource{}})
	if !reflect.DeepEqual(pod.Spec.Volumes, volumes) {
		t.Errorf("volumes = %+v, want %+v", pod.Spec.Volumes, volumes)
	}
	mounts := append(append([]VolumeMount{}, resolved.Mounts...),
		VolumeMount{Name: "ipc", MountPath: "/ipc"})
	if got := pod.Spec.Containers[0].VolumeMounts; !reflect.DeepEqual(got, mounts) {
		t.Errorf("volumeMounts = %+v, want %+v", got, mounts)
	}
	// The builder appends to copies, so the resolution is unchanged.
	if len(resolved.Mounts) != 1 || len(resolved.Volumes) != 1 {
		t.Errorf("the resolution is %+v after the build, want its original one mount and one volume", resolved)
	}
}

// A pod with no remotes still has the IPC volume, because mpv serves
// its socket at the same path either way.
func TestBuildPodWithNoRemotesCarriesTheIPCVolume(t *testing.T) {
	pod := testPod(t)

	last := pod.Spec.Volumes[len(pod.Spec.Volumes)-1]
	want := Volume{Name: "ipc", EmptyDir: &EmptyDirVolumeSource{}}
	if !reflect.DeepEqual(last, want) {
		t.Fatalf("volume = %+v, want %+v", last, want)
	}
	written, err := json.Marshal(last)
	if err != nil {
		t.Fatalf("encoding the ipc volume: %v", err)
	}
	if string(written) != `{"name":"ipc","emptyDir":{}}` {
		t.Errorf("the ipc volume encodes as %s, want {\"name\":\"ipc\",\"emptyDir\":{}}", written)
	}
}

// The command sidecar uses no device claim and mounts only the IPC
// volume, because it opens no media itself.
func TestBuildPodRunsOneCommandSidecar(t *testing.T) {
	pod := testPod(t)

	command := initContainer(t, pod, commandContainer)
	want := Container{
		Name:    commandContainer,
		Image:   testSidecarImage,
		Command: []string{"/media-operator-pod", "command"},
		Env: []EnvVar{
			{Name: playNamespaceVariable, Value: "house"},
			{Name: playNameVariable, Value: "movie"},
			{Name: podUIDVariable, ValueFrom: &EnvVarSource{
				FieldRef: &ObjectFieldSelector{FieldPath: "metadata.uid"},
			}},
			{Name: busAddressVariable, Value: testBusAddress},
			{Name: topicBaseVariable, Value: testTopicBase},
			{Name: presentationsVariable, Value: "[{}]"},
			{Name: playerNameVariable, Value: "theater"},
			{Name: playerVolumeTopicVariable, Value: playerVolumeTopic(testTopicBase, "house", "theater")},
			{Name: metricsAddressVariable, Value: "0.0.0.0:9200"},
			{Name: mediaVersionVariable, Value: "test"},
		},
		Ports:         []ContainerPort{{Name: metricsPortName, ContainerPort: commandMetricsPort}},
		VolumeMounts:  []VolumeMount{{Name: "ipc", MountPath: "/ipc"}},
		RestartPolicy: "Always",
	}
	if !reflect.DeepEqual(command, want) {
		t.Errorf("command = %+v, want %+v", command, want)
	}
	if len(command.Resources.Claims) != 0 {
		t.Errorf("the command sidecar has resources.claims %+v, want none", command.Resources.Claims)
	}
}

// The command sidecar gets every item's presentation block as one JSON
// array in item order. An item with no presentation is an empty object,
// so the sidecar can forward index i for playlist-pos i.
func TestBuildPodBakesThePresentationBlocks(t *testing.T) {
	play := testPlay()
	play.Spec.Items = []PlayItem{
		{URI: "https://films.example/loose.mkv"},
		{
			URI: "nfs://nas.example/export/shows/ep.mkv",
			Presentation: &Presentation{
				Type:         "video",
				Hint:         "series",
				Role:         "trailer",
				Series:       "The Show",
				Season:       2,
				Episode:      5,
				EpisodeTitle: "The Pilot",
			},
		},
	}
	claim := buildClaim(play, testPlayer())
	pod := buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase, nil, resolvedPreferences{}, nil, "")

	command := initContainer(t, pod, commandContainer)
	got := envValue(command, presentationsVariable)
	want := `[{},{"type":"video","hint":"series","role":"trailer","series":"The Show","season":2,"episode":5,"episodeTitle":"The Pilot"}]`
	if got != want {
		t.Errorf("%s = %s, want %s", presentationsVariable, got, want)
	}
}

// The image measurement of a subtitle track reaches the display with the
// track, and a track with no measurement carries no image object. The
// operator copies the field and does not read it.
func TestBuildPodBakesTheImageMeasurementOfASubtitleTrack(t *testing.T) {
	stream, other, events := 3, 4, int64(1382)
	play := testPlay()
	play.Spec.Items = []PlayItem{{
		URI: "nfs://nas.example/export/films/film.mkv",
		Presentation: &Presentation{Subtitles: []PlaySubtitle{
			{Stream: &stream, Events: &events, Image: &PlaySubtitleImage{CanvasHeight: 1080, LineHeight: 61, InkBottom: 918}},
			{Stream: &other, Events: &events},
		}},
	}}
	claim := buildClaim(play, testPlayer())
	pod := buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase, nil, resolvedPreferences{}, nil, "")

	got := envValue(initContainer(t, pod, commandContainer), presentationsVariable)
	want := `[{"subtitles":[{"stream":3,"events":1382,"image":{"canvasHeight":1080,"lineHeight":61,"inkBottom":918}},{"stream":4,"events":1382}]}]`
	mustMatch(t, got, want)
}

// The format of each audio stream reaches the display as the library wrote
// it, and a fact that ffprobe did not report stays absent. The operator
// copies the entries and does not read them.
func TestBuildPodBakesTheFormatOfEachAudioStream(t *testing.T) {
	play := testPlay()
	play.Spec.Items = []PlayItem{{
		URI: "nfs://nas.example/export/films/film.mkv",
		Presentation: &Presentation{Audio: []PlayAudio{
			{Stream: 1, Codec: "eac3", Profile: "Dolby Digital Plus + Dolby Atmos", Channels: 6, Layout: "5.1(side)"},
			{Stream: 2, Codec: "aac", Channels: 2},
		}},
	}}
	claim := buildClaim(play, testPlayer())
	pod := buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase, nil, resolvedPreferences{}, nil, "")

	got := envValue(initContainer(t, pod, commandContainer), presentationsVariable)
	want := `[{"audio":[{"stream":1,"codec":"eac3","profile":"Dolby Digital Plus + Dolby Atmos","channels":6,"layout":"5.1(side)"},{"stream":2,"codec":"aac","channels":2}]}]`
	mustMatch(t, got, want)
}

// The marks reach the display as the library wrote them: every
// candidate in order, a missing start or end still missing, and an
// unknown kind still present. The display interprets the marks, and the
// operator only copies them.
func TestBuildPodBakesTheMarksAsWritten(t *testing.T) {
	end, start, later := 107.0, 7.007, 3316.0
	play := testPlay()
	play.Spec.Items = []PlayItem{{
		URI: "nfs://nas.example/export/shows/ep.mkv",
		Presentation: &Presentation{Marks: []PlayMark{
			{Kind: "intro", End: &end, Source: "theintrodb"},
			{Kind: "intro", Start: &start, End: &end},
			{Kind: "credits", Start: &start, End: &later},
			{Kind: "cold-open"},
		}},
	}}
	claim := buildClaim(play, testPlayer())
	pod := buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase, nil, resolvedPreferences{}, nil, "")

	got := envValue(initContainer(t, pod, commandContainer), presentationsVariable)
	want := `[{"marks":[{"kind":"intro","end":107,"source":"theintrodb"},{"kind":"intro","start":7.007,"end":107},{"kind":"credits","start":7.007,"end":3316},{"kind":"cold-open"}]}]`
	if got != want {
		t.Errorf("%s = %s, want %s", presentationsVariable, got, want)
	}
}

// The command sidecar gets the events topic and the focus topic of
// every controller bound to the Player, in the same order.
func TestBuildPodGivesTheCommandSidecarEveryRemote(t *testing.T) {
	pod := testPodWithRemotes(t)

	command := initContainer(t, pod, commandContainer)
	mustMatch(t, envValue(command, playerNameVariable), "theater")
	mustMatch(t, envValue(command, remoteEventsTopicsVariable),
		"liken/media/remotes/house/armchair/events\nliken/media/remotes/house/sofa/events")
	mustMatch(t, envValue(command, remoteFocusTopicsVariable),
		"liken/media/remotes/house/armchair/focus\nliken/media/remotes/house/sofa/focus")
}

// A Play on a Player with no controller gets neither list, so its
// sidecar subscribes to no controller.
func TestBuildPodCarriesNoRemoteListsWithoutRemotes(t *testing.T) {
	command := initContainer(t, testPod(t), commandContainer)
	mustMatch(t, envValue(command, remoteEventsTopicsVariable), "")
	mustMatch(t, envValue(command, remoteFocusTopicsVariable), "")
}

// The resolved preferences become mpv flags. A flag appears only for a
// preference that has a value, and --subs-match-os-language=no appears
// when any other flag does.
func TestMpvPreferenceOptions(t *testing.T) {
	cases := []struct {
		name  string
		prefs resolvedPreferences
		want  []string
	}{
		{
			name:  "no preference passes only the house style",
			prefs: resolvedPreferences{},
			want: []string{
				"--sub-font=Source Sans 3", "--sub-font-size=38", "--sub-color=#FFFFFFFF",
				"--sub-outline-color=#FF000000", "--sub-outline-size=1.65", "--sub-shadow-offset=0",
				"--sub-margin-y=34", "--sub-margin-x=19",
			},
		},
		{
			name:  "audio languages become --alang",
			prefs: resolvedPreferences{AudioLanguages: []string{"en", "ja"}},
			want: []string{
				"--alang=en,ja", "--subs-match-os-language=no",
				"--sub-font=Source Sans 3", "--sub-font-size=38", "--sub-color=#FFFFFFFF",
				"--sub-outline-color=#FF000000", "--sub-outline-size=1.65", "--sub-shadow-offset=0",
				"--sub-margin-y=34", "--sub-margin-x=19",
			},
		},
		{
			name:  "subtitle languages become --slang",
			prefs: resolvedPreferences{SubtitleLanguages: []string{"en"}},
			want: []string{
				"--slang=en", "--subs-match-os-language=no",
				"--sub-font=Source Sans 3", "--sub-font-size=38", "--sub-color=#FFFFFFFF",
				"--sub-outline-color=#FF000000", "--sub-outline-size=1.65", "--sub-shadow-offset=0",
				"--sub-margin-y=34", "--sub-margin-x=19",
			},
		},
		{
			name:  "subtitles on shows them over matching audio and takes no forced track first",
			prefs: resolvedPreferences{Subtitles: subtitlesOn},
			want: []string{
				"--sub-visibility=yes", "--subs-with-matching-audio=yes", "--subs-fallback-forced=no",
				"--reset-on-next-file=sid", "--subs-match-os-language=no",
				"--sub-font=Source Sans 3", "--sub-font-size=38", "--sub-color=#FFFFFFFF",
				"--sub-outline-color=#FF000000", "--sub-outline-size=1.65", "--sub-shadow-offset=0",
				"--sub-margin-y=34", "--sub-margin-x=19",
			},
		},
		{
			name:  "subtitles off loads no subtitle track",
			prefs: resolvedPreferences{Subtitles: subtitlesOff},
			want: []string{
				"--sid=no", "--subs-match-os-language=no",
				"--sub-font=Source Sans 3", "--sub-font-size=38", "--sub-color=#FFFFFFFF",
				"--sub-outline-color=#FF000000", "--sub-outline-size=1.65", "--sub-shadow-offset=0",
				"--sub-margin-y=34", "--sub-margin-x=19",
			},
		},
		{
			name:  "subtitles auto shows only a forced track over audio in a subtitle language",
			prefs: resolvedPreferences{Subtitles: subtitlesAuto},
			want: []string{
				"--subs-with-matching-audio=forced", "--reset-on-next-file=sid", "--subs-match-os-language=no",
				"--sub-font=Source Sans 3", "--sub-font-size=38", "--sub-color=#FFFFFFFF",
				"--sub-outline-color=#FF000000", "--sub-outline-size=1.65", "--sub-shadow-offset=0",
				"--sub-margin-y=34", "--sub-margin-x=19",
			},
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
				"--sub-visibility=yes", "--subs-with-matching-audio=yes", "--subs-fallback-forced=no",
				"--reset-on-next-file=sid", "--subs-match-os-language=no",
				"--sub-font=Source Sans 3", "--sub-font-size=38", "--sub-color=#FFFFFFFF",
				"--sub-outline-color=#FF000000", "--sub-outline-size=1.65", "--sub-shadow-offset=0",
				"--sub-margin-y=34", "--sub-margin-x=19",
			},
		},
		{
			name:  "a stated empty list passes no flag for that field",
			prefs: resolvedPreferences{AudioLanguages: []string{}},
			want: []string{
				"--sub-font=Source Sans 3", "--sub-font-size=38", "--sub-color=#FFFFFFFF",
				"--sub-outline-color=#FF000000", "--sub-outline-size=1.65", "--sub-shadow-offset=0",
				"--sub-margin-y=34", "--sub-margin-x=19",
			},
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

// The player container gets the resolved options as one variable joined
// with newlines, which the shim splits into mpv arguments.
func TestBuildPodCarriesTheResolvedOptions(t *testing.T) {
	play := testPlay()
	claim := buildClaim(play, testPlayer())
	prefs := resolvedPreferences{AudioLanguages: []string{"en", "ja"}, Subtitles: subtitlesAuto}
	pod := buildPod(play, claim, testResolution(t), testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase, nil, prefs, nil, "")

	got := envValue(pod.Spec.Containers[0], playerOptionsVariable)
	want := "--alang=en,ja\n--subs-with-matching-audio=forced\n--reset-on-next-file=sid\n--subs-match-os-language=no\n" + houseStyleLines
	if got != want {
		t.Errorf("%s = %q, want %q", playerOptionsVariable, got, want)
	}
}

// houseStyleLines is the house subtitle style as the options variable
// carries it, one option on each line.
const houseStyleLines = "--sub-font=Source Sans 3\n--sub-font-size=38\n--sub-color=#FFFFFFFF\n" +
	"--sub-outline-color=#FF000000\n--sub-outline-size=1.65\n--sub-shadow-offset=0\n" +
	"--sub-margin-y=34\n--sub-margin-x=19"

// A run with no preferences still carries the house subtitle style,
// because a person can turn subtitles on from the display.
func TestBuildPodWithNoPreferencesCarriesTheHouseStyle(t *testing.T) {
	pod := testPod(t)
	mustMatch(t, envValue(pod.Spec.Containers[0], playerOptionsVariable), houseStyleLines)
}

// The command sidecar gets the subtitle preferences only in the two
// modes that show a track.
func TestTheCommandSidecarCarriesTheSubtitlePreferences(t *testing.T) {
	cases := []struct {
		name  string
		prefs resolvedPreferences
		want  string
	}{
		{
			name: "on carries every field the rule reads",
			prefs: resolvedPreferences{
				AudioLanguages: []string{"en"}, SubtitleLanguages: []string{"en", "es"},
				Subtitles: subtitlesOn, SDH: sdhPrefer, TimeZone: "America/New_York",
			},
			want: `{"subtitles":"on","sdh":"prefer","audioLanguages":["en"],"subtitleLanguages":["en","es"]}`,
		},
		{
			name:  "auto carries the mode",
			prefs: resolvedPreferences{Subtitles: subtitlesAuto},
			want:  `{"subtitles":"auto"}`,
		},
		{
			name:  "off leaves the choice to mpv's --sid=no",
			prefs: resolvedPreferences{Subtitles: subtitlesOff, SubtitleLanguages: []string{"en"}},
			want:  "",
		},
		{
			name:  "no mode leaves the choice to mpv",
			prefs: resolvedPreferences{SubtitleLanguages: []string{"en"}},
			want:  "",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			play := testPlay()
			pod := buildPod(play, buildClaim(play, testPlayer()), testResolution(t),
				testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase, nil, one.prefs, nil, "")
			mustMatch(t, envValue(initContainer(t, pod, commandContainer), subtitlePreferencesVariable), one.want)
		})
	}
}

// mpv plays at its default level: no volume and no mute option reach
// its command line, because the receiver or the sinks set the level of
// the room.
func TestBuildPodCarriesNoVolumeOption(t *testing.T) {
	mustMatch(t, envValue(testPod(t).Spec.Containers[0], playerOptionsVariable), houseStyleLines)
}

// The command sidecar gets the volume topic only when the Player lists
// sinks.
func TestTheCommandSidecarCarriesTheVolumeTopicOnlyWithSpeakers(t *testing.T) {
	speakerless := &Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house"},
		Spec:     PlayerSpec{Display: &PlayerDevice{Class: "display-output"}},
	}
	play := testPlay()
	pod := buildPod(play, buildClaim(play, speakerless), testResolution(t),
		testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase, nil, resolvedPreferences{}, nil, "")

	mustMatch(t, envValue(initContainer(t, pod, commandContainer), playerVolumeTopicVariable), "")
	mustMatch(t, envValue(initContainer(t, testPod(t), commandContainer), playerVolumeTopicVariable),
		playerVolumeTopic(testTopicBase, "house", "theater"))
}

// A Play with a next block gives the whole block to both containers in
// one variable, with the art at the path inside the pod that the
// resolver rewrote it to.
func TestBuildPodCarriesTheNextBlockToBothContainers(t *testing.T) {
	play := testPlay()
	if err := json.Unmarshal([]byte(`{
		"reason": "Next in Harbor Lights",
		"title": "E05",
		"detail": "45 min",
		"art": "claim://house/library/shows/next.jpg",
		"remainingSeconds": 2160,
		"request": {"library": "living-room/shows"}
	}`), &play.Spec.Next); err != nil {
		t.Fatalf("decoding the next block: %v", err)
	}
	resolved, err := resolvePlay("house", play.Spec.Items, play.Spec.Next, frame{})
	mustSucceed(t, err)
	pod := buildPod(play, buildClaim(play, testPlayer()), resolved,
		testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase, nil, resolvedPreferences{}, nil, "")

	want := `{"reason":"Next in Harbor Lights","title":"E05","detail":"45 min","art":"/media/1/shows/next.jpg","remainingSeconds":2160,"request":{"library":"living-room/shows"}}`
	mustMatch(t, envValue(pod.Spec.Containers[0], nextVariable), want)
	mustMatch(t, envValue(initContainer(t, pod, commandContainer), nextVariable), want)
}

// A Play with no next block sets the variable on neither container.
func TestBuildPodSetsNoNextBlockWhereThePlayCarriesNone(t *testing.T) {
	pod := testPod(t)

	mustMatch(t, envValue(pod.Spec.Containers[0], nextVariable), "")
	mustMatch(t, envValue(initContainer(t, pod, commandContainer), nextVariable), "")
}

func initContainerNames(pod *Pod) []string {
	names := make([]string, 0, len(pod.Spec.InitContainers))
	for _, container := range pod.Spec.InitContainers {
		names = append(names, container.Name)
	}
	return names
}

func testPodWithPreferences(t *testing.T, prefs resolvedPreferences) *Pod {
	t.Helper()
	play := testPlay()
	claim := buildClaim(play, testPlayer())
	return buildPod(play, claim, testResolution(t),
		testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase,
		nil, prefs, nil, "")
}

func TestThePlaybackPodMountsNoServiceAccountToken(t *testing.T) {
	pod := testPod(t)

	mustMatch(t, pod.Spec.AutomountServiceAccountToken != nil, true)
	mustMatch(t, *pod.Spec.AutomountServiceAccountToken, false)
}

func TestBuildPodRunsThePlayerTheCommandSidecarAndTheDisplay(t *testing.T) {
	pod := testPod(t)

	mustMatch(t, len(pod.Spec.Containers), 1)
	mustMatchAll(t, initContainerNames(pod), []string{commandContainer, displayContainer})

	display := initContainer(t, pod, displayContainer)
	mustMatch(t, display.Image, testDisplayImage)
	mustMatch(t, display.RestartPolicy, sidecarRestartPolicy)
	mustMatchAll(t, display.Command, nil)
}

func TestAPlayerWithNoDisplayGetsNoDisplayContainer(t *testing.T) {
	play := testPlay()
	player := testPlayer()
	player.Spec.Display = nil
	pod := buildPod(play, buildClaim(play, player), testResolution(t),
		testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase,
		nil, resolvedPreferences{}, nil, "")

	mustMatch(t, len(pod.Spec.Containers), 1)
	mustMatchAll(t, initContainerNames(pod), []string{commandContainer})
}

func TestTheDisplayContainerHoldsThePlayersRequests(t *testing.T) {
	pod := testPod(t)

	display := initContainer(t, pod, displayContainer)
	mustMatchAll(t, display.Resources.Claims, pod.Spec.Containers[0].Resources.Claims)
}

// The display container mounts the player's media read-only, and the
// IPC volume.
func TestTheDisplayContainerMountsTheIPCVolumeAndTheMedia(t *testing.T) {
	display := initContainer(t, testPod(t), displayContainer)

	mustMatchAll(t, display.VolumeMounts,
		append(testResolution(t).Mounts, VolumeMount{Name: "ipc", MountPath: "/ipc"}))
	for _, mount := range display.VolumeMounts[:len(display.VolumeMounts)-1] {
		mustMatch(t, mount.ReadOnly, true)
	}
}

// The display crops each trickplay tile by the interval. A Play with no
// interval gets Jellyfin's default.
func TestTheDisplayContainerCarriesTheTrickplayInterval(t *testing.T) {
	display := initContainer(t, testPod(t), displayContainer)
	mustMatchAll(t, display.Env,
		[]EnvVar{{Name: trickplayIntervalVariable, Value: defaultTrickplayInterval}})

	play := testPlay()
	play.Spec.TrickplayInterval = "5s"
	pod := buildPod(play, buildClaim(play, testPlayer()), testResolution(t),
		testPlayerImage, testSidecarImage, testDisplayImage, testBusAddress, testTopicBase,
		nil, resolvedPreferences{}, nil, "")
	mustMatchAll(t, initContainer(t, pod, displayContainer).Env,
		[]EnvVar{{Name: trickplayIntervalVariable, Value: "5s"}})
}

// A resolved time zone is the TZ variable of the display container, so
// the display clock shows local time.
func TestTheDisplayContainerCarriesTheResolvedTimeZone(t *testing.T) {
	pod := testPodWithPreferences(t, resolvedPreferences{TimeZone: "America/New_York"})

	display := initContainer(t, pod, displayContainer)
	mustMatchAll(t, display.Env, []EnvVar{
		{Name: trickplayIntervalVariable, Value: defaultTrickplayInterval},
		{Name: timeZoneVariable, Value: "America/New_York"},
	})
}

// A run with no time zone has no TZ variable.
func TestTheDisplayContainerWithNoTimeZoneCarriesNoTZ(t *testing.T) {
	display := initContainer(t, testPod(t), displayContainer)

	mustMatchAll(t, display.Env,
		[]EnvVar{{Name: trickplayIntervalVariable, Value: defaultTrickplayInterval}})
}
