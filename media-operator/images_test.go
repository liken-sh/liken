package main

// These tests prove the derivation against reference strings, and
// the start-up against a pod the fake API server serves.

import (
	"testing"
)

// A tagged reference gives every companion image the operator's
// repository with a suffix, at the operator's tag.
func TestDeriveCompanionImages(t *testing.T) {
	cases := []struct {
		name      string
		reference string
		want      companionImages
	}{
		{
			name:      "a tagged reference",
			reference: "ghcr.io/liken-sh/media-operator:2026.09.03-007",
			want: companionImages{
				player:  "ghcr.io/liken-sh/media-operator-player:2026.09.03-007",
				idle:    "ghcr.io/liken-sh/media-operator-idle:2026.09.03-007",
				sidecar: "ghcr.io/liken-sh/media-operator-sidecar:2026.09.03-007",
			},
		},
		{
			name:      "a development build",
			reference: "ghcr.io/liken-sh/media-operator:2026.09.02-005-dev-003-83f15cab",
			want: companionImages{
				player:  "ghcr.io/liken-sh/media-operator-player:2026.09.02-005-dev-003-83f15cab",
				idle:    "ghcr.io/liken-sh/media-operator-idle:2026.09.02-005-dev-003-83f15cab",
				sidecar: "ghcr.io/liken-sh/media-operator-sidecar:2026.09.02-005-dev-003-83f15cab",
			},
		},
		{
			name:      "a registry with a port",
			reference: "registry:5000/liken-sh/media-operator:2026.09.03-007",
			want: companionImages{
				player:  "registry:5000/liken-sh/media-operator-player:2026.09.03-007",
				idle:    "registry:5000/liken-sh/media-operator-idle:2026.09.03-007",
				sidecar: "registry:5000/liken-sh/media-operator-sidecar:2026.09.03-007",
			},
		},
		{
			name:      "a reference with no registry",
			reference: "media-operator:2026.09.03-007",
			want: companionImages{
				player:  "media-operator-player:2026.09.03-007",
				idle:    "media-operator-idle:2026.09.03-007",
				sidecar: "media-operator-sidecar:2026.09.03-007",
			},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			derived, err := deriveCompanionImages(each.reference)
			mustSucceed(t, err)
			mustMatch(t, derived, each.want)
		})
	}
}

// A reference with no tag has no version to share, so the
// derivation fails and says so.
func TestDeriveCompanionImagesNeedsATag(t *testing.T) {
	cases := []struct {
		name      string
		reference string
	}{
		{
			name:      "a digest",
			reference: "ghcr.io/liken-sh/media-operator@sha256:c0ffee",
		},
		{
			name:      "a tag and a digest",
			reference: "ghcr.io/liken-sh/media-operator:2026.09.03-007@sha256:c0ffee",
		},
		{
			name:      "no tag",
			reference: "ghcr.io/liken-sh/media-operator",
		},
		{
			name:      "a registry with a port and no tag",
			reference: "registry:5000/liken-sh/media-operator",
		},
		{
			name:      "an empty tag",
			reference: "ghcr.io/liken-sh/media-operator:",
		},
		{
			name:      "an empty reference",
			reference: "",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			_, err := deriveCompanionImages(each.reference)
			mustFail(t, err)
		})
	}
}

// operatorPodAPI serves one operator pod, the way the API server serves
// the pod the downward API names.
func operatorPodAPI(image string) *cannedAPI {
	return &cannedAPI{answers: map[string]any{
		"GET /api/v1/namespaces/liken-system/pods/media-operator-59f4c8d7b5-2xk9q": Pod{
			Metadata: ObjectMeta{Name: "media-operator-59f4c8d7b5-2xk9q", Namespace: "liken-system"},
			Spec: PodSpec{Containers: []Container{
				{Name: "operator", Image: image},
			}},
		},
	}}
}

// At start the operator reads its own pod and derives the three
// companion images from the operator container's image.
func TestResolveImagesReadsTheOperatorsOwnPod(t *testing.T) {
	t.Setenv(podNameVariable, "media-operator-59f4c8d7b5-2xk9q")
	t.Setenv(podNamespaceVariable, "liken-system")
	api := operatorPodAPI("ghcr.io/liken-sh/media-operator:2026.09.03-007")

	images, err := resolveImages(testAPIClient(t, api.handler()))

	mustSucceed(t, err)
	mustMatch(t, images, companionImages{
		player:  "ghcr.io/liken-sh/media-operator-player:2026.09.03-007",
		idle:    "ghcr.io/liken-sh/media-operator-idle:2026.09.03-007",
		sidecar: "ghcr.io/liken-sh/media-operator-sidecar:2026.09.03-007",
	})
}

// A variable that is set wins for its one image, and the other two
// still derive.
func TestResolveImagesTakesOneEnvironmentOverride(t *testing.T) {
	t.Setenv(podNameVariable, "media-operator-59f4c8d7b5-2xk9q")
	t.Setenv(podNamespaceVariable, "liken-system")
	t.Setenv(idleImageVariable, "ghcr.io/liken-sh/media-browser:2026.09.03-004")
	api := operatorPodAPI("ghcr.io/liken-sh/media-operator:2026.09.03-007")

	images, err := resolveImages(testAPIClient(t, api.handler()))

	mustSucceed(t, err)
	mustMatch(t, images, companionImages{
		player:  "ghcr.io/liken-sh/media-operator-player:2026.09.03-007",
		idle:    "ghcr.io/liken-sh/media-browser:2026.09.03-004",
		sidecar: "ghcr.io/liken-sh/media-operator-sidecar:2026.09.03-007",
	})
}

// When every variable is set, the operator reads no pod at all. That
// is the way out for a pod whose image names a digest.
func TestResolveImagesTakesEveryEnvironmentOverrideWithNoPod(t *testing.T) {
	t.Setenv(playerImageVariable, "ghcr.io/liken-sh/media-operator-player:2026.09.03-007")
	t.Setenv(idleImageVariable, "ghcr.io/liken-sh/media-operator-idle:2026.09.03-007")
	t.Setenv(sidecarImageVariable, "ghcr.io/liken-sh/media-operator-sidecar:2026.09.03-007")
	api := &cannedAPI{}

	images, err := resolveImages(testAPIClient(t, api.handler()))

	mustSucceed(t, err)
	mustMatch(t, images, companionImages{
		player:  "ghcr.io/liken-sh/media-operator-player:2026.09.03-007",
		idle:    "ghcr.io/liken-sh/media-operator-idle:2026.09.03-007",
		sidecar: "ghcr.io/liken-sh/media-operator-sidecar:2026.09.03-007",
	})
	mustMatch(t, len(api.requests), 0)
}

// A pod whose operator container names its image by digest has no
// tag to share, so start-up fails.
func TestResolveImagesFailsOnAPodWithNoTag(t *testing.T) {
	t.Setenv(podNameVariable, "media-operator-59f4c8d7b5-2xk9q")
	t.Setenv(podNamespaceVariable, "liken-system")
	api := operatorPodAPI("ghcr.io/liken-sh/media-operator@sha256:c0ffee")

	_, err := resolveImages(testAPIClient(t, api.handler()))

	mustFail(t, err)
}

// A pod with no container named operator gives no image to derive
// from, so start-up fails.
func TestResolveImagesFailsWithNoOperatorContainer(t *testing.T) {
	t.Setenv(podNameVariable, "media-operator-59f4c8d7b5-2xk9q")
	t.Setenv(podNamespaceVariable, "liken-system")
	api := &cannedAPI{answers: map[string]any{
		"GET /api/v1/namespaces/liken-system/pods/media-operator-59f4c8d7b5-2xk9q": Pod{
			Metadata: ObjectMeta{Name: "media-operator-59f4c8d7b5-2xk9q", Namespace: "liken-system"},
			Spec:     PodSpec{Containers: []Container{{Name: "bus", Image: "eclipse-mosquitto:2"}}},
		},
	}}

	_, err := resolveImages(testAPIClient(t, api.handler()))

	mustFail(t, err)
}

// Without the downward API variables the operator cannot name its
// own pod, and with no variable set it has nothing else to go on, so
// start-up fails before it asks the API for anything.
func TestResolveImagesFailsWithoutTheDownwardAPI(t *testing.T) {
	api := &cannedAPI{}

	_, err := resolveImages(testAPIClient(t, api.handler()))

	mustFail(t, err)
	mustMatch(t, len(api.requests), 0)
}
