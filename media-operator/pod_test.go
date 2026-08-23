package main

// These tests cover what a Play becomes at run time: one pod that runs
// mpv on the resolved list, holds the claim's every role, carries one
// command sidecar that owns the mpv socket, and carries one translator
// sidecar per bound remote.

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
	testImage      = "ghcr.io/liken-sh/media-operator:test"
	testBusAddress = "bus.media.svc:1883"
	testTopicBase  = "liken/media"
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
	return buildPod(play, claim, testResolution(t), testImage, testBusAddress, testTopicBase, nil)
}

// The same pod, with two controllers bound to the player.
func testPodWithRemotes(t *testing.T) *Pod {
	t.Helper()
	play := testPlay()
	claim := buildClaim(play, testPlayer())
	return buildPod(play, claim, testResolution(t), testImage, testBusAddress, testTopicBase, testBoundRemotes())
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
	if container.Image != testImage {
		t.Errorf("image = %q, want %q", container.Image, testImage)
	}
	args := []string{"https://films.example/trailer.mkv", "/media/1/film.mkv"}
	if !reflect.DeepEqual(container.Args, args) {
		t.Errorf("args = %v, want %v", container.Args, args)
	}
	if len(container.Env) != 0 {
		t.Errorf("env = %+v, want none", container.Env)
	}
}

// A declared start reaches the player container as one variable, and an
// ordinary run's player container carries nothing extra.
func TestBuildPodCarriesTheDeclaredStart(t *testing.T) {
	play := testPlay()
	play.Spec.Start = "0:10:00"
	claim := buildClaim(play, testPlayer())
	pod := buildPod(play, claim, testResolution(t), testImage, testBusAddress, testTopicBase, nil)

	env := pod.Spec.Containers[0].Env
	want := []EnvVar{{Name: playStartVariable, Value: "0:10:00"}}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("env = %+v, want %+v", env, want)
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
	pod := buildPod(play, buildClaim(play, testPlayer()), resolved, testImage, testBusAddress, testTopicBase, nil)

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

// The command sidecar is the player image in its command mode, holding
// no device claim, mounting the IPC socket, and carrying the play's
// identity, the bus, and the base.
func TestBuildPodRunsOneCommandSidecar(t *testing.T) {
	pod := testPodWithRemotes(t)

	command := initContainer(t, pod, commandContainer)
	want := Container{
		Name:    commandContainer,
		Image:   testImage,
		Command: []string{"/media-operator", "command"},
		Env: []EnvVar{
			{Name: playNamespaceVariable, Value: "house"},
			{Name: playNameVariable, Value: "movie"},
			{Name: busAddressVariable, Value: testBusAddress},
			{Name: topicBaseVariable, Value: testTopicBase},
			{Name: presentationsVariable, Value: "[{}]"},
		},
		VolumeMounts:  []VolumeMount{{Name: "ipc", MountPath: "/ipc"}, {Name: "art", MountPath: "/art"}},
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
	pod := buildPod(play, claim, testResolution(t), testImage, testBusAddress, testTopicBase, nil)

	command := initContainer(t, pod, commandContainer)
	got := envValue(command, presentationsVariable)
	want := `[{},{"type":"video","hint":"series","series":"The Show","season":2,"episode":5,"episodeTitle":"The Pilot"}]`
	if got != want {
		t.Errorf("%s = %s, want %s", presentationsVariable, got, want)
	}
}

// One translator sidecar runs per bound remote. Each is the player image
// in its translate mode, holding no device claim, mounting no IPC socket,
// and carrying the remote's name and its three topics.
func TestBuildPodRunsOneTranslatorPerRemote(t *testing.T) {
	pod := testPodWithRemotes(t)

	// The command sidecar first, then one translator per remote, in order.
	names := []string{}
	for _, container := range pod.Spec.InitContainers {
		names = append(names, container.Name)
	}
	if !reflect.DeepEqual(names, []string{"command", "translate-armchair", "translate-sofa"}) {
		t.Fatalf("init containers = %v, want the command sidecar and one translator per remote", names)
	}

	sofa := initContainer(t, pod, "translate-sofa")
	want := Container{
		Name:    "translate-sofa",
		Image:   testImage,
		Command: []string{"/media-operator", "translate"},
		Env: []EnvVar{
			{Name: playNamespaceVariable, Value: "house"},
			{Name: playNameVariable, Value: "movie"},
			{Name: busAddressVariable, Value: testBusAddress},
			{Name: topicBaseVariable, Value: testTopicBase},
			{Name: remoteNameVariable, Value: "sofa"},
			{Name: remoteEventsVariable, Value: "liken/media/remotes/house/sofa/events"},
			{Name: keymapTopicVariable, Value: "liken/media/keymaps/gamepad"},
			{Name: focusTopicVariable, Value: "liken/media/remotes/house/sofa/focus"},
		},
		RestartPolicy: "Always",
	}
	if !reflect.DeepEqual(sofa, want) {
		t.Errorf("translator = %+v, want %+v", sofa, want)
	}
	if mountsIPC(sofa) {
		t.Error("the translator mounts the IPC socket, which only the command sidecar owns")
	}
	if len(sofa.Resources.Claims) != 0 {
		t.Errorf("the translator holds a device claim: %+v", sofa.Resources.Claims)
	}
}
