package main

// These tests cover what a Play becomes at run time: one pod that runs
// mpv on the resolved list, holds the claim's every role, and carries
// one bridge sidecar that speaks to the bus.

import (
	"encoding/json"
	"reflect"
	"testing"
)

const (
	testImage      = "ghcr.io/liken-sh/media-operator:test"
	testBusAddress = "bus.media.svc:1883"
	testTopicBase  = "liken/media"
)

// One playlist that costs a volume, so the pod under test carries a
// mount as well as arguments.
func testResolution(t *testing.T) resolution {
	t.Helper()
	resolved, err := resolveURIs([]string{
		"https://films.example/trailer.mkv",
		"nfs://nas.example/export/films/film.mkv",
	})
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
		Volume{Name: "ipc", EmptyDir: &EmptyDirVolumeSource{}})
	if !reflect.DeepEqual(pod.Spec.Volumes, volumes) {
		t.Errorf("volumes = %+v, want %+v", pod.Spec.Volumes, volumes)
	}
	mounts := append(append([]VolumeMount{}, resolved.Mounts...),
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

// A pod with no remotes still carries the IPC volume, because mpv
// serves its socket at one path either way, and it still carries the
// bridge sidecar, its one bus client.
func TestBuildPodAlwaysCarriesTheIPCVolumeAndTheBridge(t *testing.T) {
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
	if len(pod.Spec.InitContainers) != 1 || pod.Spec.InitContainers[0].Name != "bridge" {
		t.Errorf("initContainers = %+v, want one bridge", pod.Spec.InitContainers)
	}
}

// The bridge sidecar is the player image in its bridge mode, holding no
// device claim, carrying the play's identity, the bus, the base, and
// the set of bound remotes.
func TestBuildPodRunsOneBridgeSidecar(t *testing.T) {
	pod := testPodWithRemotes(t)

	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("initContainers = %+v, want one bridge", pod.Spec.InitContainers)
	}
	bridge := pod.Spec.InitContainers[0]

	remotes, err := json.Marshal([]remoteBindings{
		{EventsTopic: "liken/media/remotes/house/armchair/events", Bindings: testBoundRemotes()[0].Bindings},
		{EventsTopic: "liken/media/remotes/house/sofa/events", Bindings: testBoundRemotes()[1].Bindings},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Container{
		Name:    "bridge",
		Image:   testImage,
		Command: []string{"/media-operator", "bridge"},
		Env: []EnvVar{
			{Name: playNamespaceVariable, Value: "house"},
			{Name: playNameVariable, Value: "movie"},
			{Name: busAddressVariable, Value: testBusAddress},
			{Name: topicBaseVariable, Value: testTopicBase},
			{Name: remotesVariable, Value: string(remotes)},
		},
		VolumeMounts:  []VolumeMount{{Name: "ipc", MountPath: "/ipc"}},
		RestartPolicy: "Always",
	}
	if !reflect.DeepEqual(bridge, want) {
		t.Errorf("bridge = %+v, want %+v", bridge, want)
	}
	if len(bridge.Resources.Claims) != 0 {
		t.Errorf("the bridge holds a device claim: %+v", bridge.Resources.Claims)
	}
}

// A pod with no bound remotes still carries the bridge, and its
// MEDIA_REMOTES is an empty JSON array rather than null.
func TestBuildPodBridgeCarriesAnEmptyRemoteSetWhenNoneBind(t *testing.T) {
	pod := testPod(t)

	bridge := pod.Spec.InitContainers[0]
	var value string
	for _, variable := range bridge.Env {
		if variable.Name == remotesVariable {
			value = variable.Value
		}
	}
	if value != "[]" {
		t.Errorf("%s = %q, want []", remotesVariable, value)
	}
}
