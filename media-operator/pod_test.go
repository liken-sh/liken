package main

// These tests cover what a Play becomes at run time: one pod that
// holds the claim's every role, plays the resolved list in order,
// and carries the four values that let it report back.

import (
	"encoding/json"
	"reflect"
	"testing"
)

const (
	testImage       = "ghcr.io/liken-sh/media-operator:test"
	testToken       = "s3cret-token"
	testOperatorURL = "http://media-operator.media.svc:8080"
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
	claim := buildClaim(play, testPlayer(), nil)
	return buildPod(play, claim, testResolution(t), testImage, testToken, testOperatorURL, nil)
}

// The same pod, with two controllers bound to the player.
func testPodWithRemotes(t *testing.T) *Pod {
	t.Helper()
	play := testPlay()
	remotes := testBoundRemotes()
	claim := buildClaim(play, testPlayer(), remotes)
	return buildPod(play, claim, testResolution(t), testImage, testToken, testOperatorURL, remotes)
}

// restartPolicy is Never, because a finished film is not a failure
// to restart, and the Play owns the pod so deleting the Play takes
// it away.
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

// The four variables are the whole of what the pod knows about the
// control plane: who it is, what proves it, and where to report.
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
	env := []EnvVar{
		{Name: playNamespaceVariable, Value: "house"},
		{Name: playNameVariable, Value: "movie"},
		{Name: playTokenVariable, Value: testToken},
		{Name: operatorURLVariable, Value: testOperatorURL},
	}
	if !reflect.DeepEqual(container.Env, env) {
		t.Errorf("env = %+v, want %+v", container.Env, env)
	}
}

// A declared start rides into the pod as one more variable, and an
// ordinary run's pod carries nothing extra.
func TestBuildPodCarriesTheDeclaredStart(t *testing.T) {
	play := testPlay()
	play.Spec.Start = "0:10:00"
	claim := buildClaim(play, testPlayer(), nil)
	pod := buildPod(play, claim, testResolution(t), testImage, testToken, testOperatorURL, nil)

	env := pod.Spec.Containers[0].Env
	last := env[len(env)-1]
	want := EnvVar{Name: playStartVariable, Value: "0:10:00"}
	if last != want {
		t.Errorf("last env = %+v, want %+v", last, want)
	}
}

// The pod names the claim once and the container repeats that name
// for each role, which is what keeps the roles separate inside one
// claim.
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
	pod := buildPod(play, buildClaim(play, testPlayer(), nil), resolved, testImage, testToken, testOperatorURL, nil)

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

// A pod with no remotes at all still carries the volume, because the
// supervisor serves mpv's socket at one path either way.
func TestBuildPodAlwaysCarriesTheIPCVolume(t *testing.T) {
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
	if len(pod.Spec.InitContainers) != 0 {
		t.Errorf("initContainers = %+v, want none", pod.Spec.InitContainers)
	}
}

// One sidecar per remote: the same image in its remote mode, holding
// its own controller and its own compiled table.
func TestBuildPodRunsOneSidecarPerBoundRemote(t *testing.T) {
	pod := testPodWithRemotes(t)

	if len(pod.Spec.InitContainers) != 2 {
		t.Fatalf("initContainers = %+v, want two", pod.Spec.InitContainers)
	}
	want := Container{
		Name:    "remote-armchair",
		Image:   testImage,
		Command: []string{"/media-operator", "remote"},
		Env: []EnvVar{{
			Name:  "MEDIA_KEYMAP",
			Value: `[{"type":1,"code":304,"value":1,"action":"pause"}]`,
		}},
		Resources: ResourceRequirements{Claims: []ContainerClaim{
			{Name: "devices", Request: "remote-armchair"},
		}},
		VolumeMounts:  []VolumeMount{{Name: "ipc", MountPath: "/ipc"}},
		RestartPolicy: "Always",
	}
	if !reflect.DeepEqual(pod.Spec.InitContainers[0], want) {
		t.Errorf("sidecar = %+v, want %+v", pod.Spec.InitContainers[0], want)
	}
	second := pod.Spec.InitContainers[1]
	if second.Name != "remote-sofa" {
		t.Errorf("name = %q, want remote-sofa", second.Name)
	}
	value := `[{"type":3,"code":17,"value":-1,"action":"volume","amount":5}]`
	if second.Env[0].Value != value {
		t.Errorf("keymap = %s, want %s", second.Env[0].Value, value)
	}
}

// The controller's input nodes stay out of the container that decodes
// media from the network.
func TestBuildPodKeepsTheRemoteRequestsOutOfThePlayer(t *testing.T) {
	pod := testPodWithRemotes(t)

	held := []ContainerClaim{
		{Name: "devices", Request: "screen"},
		{Name: "devices", Request: "audio0"},
		{Name: "devices", Request: "audio1"},
		{Name: "devices", Request: "render"},
	}
	if got := pod.Spec.Containers[0].Resources.Claims; !reflect.DeepEqual(got, held) {
		t.Errorf("resources.claims = %+v, want %+v", got, held)
	}
	claims := []PodResourceClaim{{Name: "devices", ResourceClaimName: "movie-devices"}}
	if !reflect.DeepEqual(pod.Spec.ResourceClaims, claims) {
		t.Errorf("resourceClaims = %+v, want %+v", pod.Spec.ResourceClaims, claims)
	}
}

// The minted token survives an operator restart in the pod spec
// alone, so reading it back out is how the next pass adopts a pod it
// did not create.
func TestTokenFromPodReadsBackTheTokenItWasBuiltWith(t *testing.T) {
	if got := tokenFromPod(testPod(t)); got != testToken {
		t.Errorf("token = %q, want %q", got, testToken)
	}
}

func TestTokenFromPodReadsNoTokenThatIsNotThere(t *testing.T) {
	cases := []struct {
		name string
		pod  *Pod
	}{
		{name: "a pod with no containers at all", pod: &Pod{}},
		{
			name: "a pod with no player container",
			pod: &Pod{Spec: PodSpec{Containers: []Container{{
				Name: "sidecar",
				Env:  []EnvVar{{Name: playTokenVariable, Value: testToken}},
			}}}},
		},
		{
			name: "a player container with no token variable",
			pod: &Pod{Spec: PodSpec{Containers: []Container{{
				Name: playerContainer,
				Env:  []EnvVar{{Name: playNameVariable, Value: "movie"}},
			}}}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tokenFromPod(c.pod); got != "" {
				t.Errorf("token = %q, want empty", got)
			}
		})
	}
}
