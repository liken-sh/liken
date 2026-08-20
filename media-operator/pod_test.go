package main

// These tests cover what a Play becomes at run time: one pod that
// holds the claim's every role, plays the resolved list in order,
// and carries the four values that let it report back.

import (
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
	claim := buildClaim(play, testPlayer())
	return buildPod(play, claim, testResolution(t), testImage, testToken, testOperatorURL)
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
	claim := buildClaim(play, testPlayer())
	pod := buildPod(play, claim, testResolution(t), testImage, testToken, testOperatorURL)

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
// container, so the resolution splits across the two.
func TestBuildPodCarriesTheResolvedVolumesAndMounts(t *testing.T) {
	resolved := testResolution(t)
	play := testPlay()
	pod := buildPod(play, buildClaim(play, testPlayer()), resolved, testImage, testToken, testOperatorURL)

	if !reflect.DeepEqual(pod.Spec.Volumes, resolved.Volumes) {
		t.Errorf("volumes = %+v, want %+v", pod.Spec.Volumes, resolved.Volumes)
	}
	if got := pod.Spec.Containers[0].VolumeMounts; !reflect.DeepEqual(got, resolved.Mounts) {
		t.Errorf("volumeMounts = %+v, want %+v", got, resolved.Mounts)
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
