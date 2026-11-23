package main

import (
	"testing"
)

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
				display: "ghcr.io/liken-sh/media-operator-display:2026.09.03-007",
			},
		},
		{
			name:      "a development build",
			reference: "ghcr.io/liken-sh/media-operator:2026.09.02-005-dev-003-83f15cab",
			want: companionImages{
				player:  "ghcr.io/liken-sh/media-operator-player:2026.09.02-005-dev-003-83f15cab",
				idle:    "ghcr.io/liken-sh/media-operator-idle:2026.09.02-005-dev-003-83f15cab",
				sidecar: "ghcr.io/liken-sh/media-operator-sidecar:2026.09.02-005-dev-003-83f15cab",
				display: "ghcr.io/liken-sh/media-operator-display:2026.09.02-005-dev-003-83f15cab",
			},
		},
		{
			name:      "a registry with a port",
			reference: "registry:5000/liken-sh/media-operator:2026.09.03-007",
			want: companionImages{
				player:  "registry:5000/liken-sh/media-operator-player:2026.09.03-007",
				idle:    "registry:5000/liken-sh/media-operator-idle:2026.09.03-007",
				sidecar: "registry:5000/liken-sh/media-operator-sidecar:2026.09.03-007",
				display: "registry:5000/liken-sh/media-operator-display:2026.09.03-007",
			},
		},
		{
			name:      "a reference with no registry",
			reference: "media-operator:2026.09.03-007",
			want: companionImages{
				player:  "media-operator-player:2026.09.03-007",
				idle:    "media-operator-idle:2026.09.03-007",
				sidecar: "media-operator-sidecar:2026.09.03-007",
				display: "media-operator-display:2026.09.03-007",
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

// operatorPodAPI serves the operator pod that the tests set in POD_NAME
// and POD_NAMESPACE.
func operatorPodAPI(image string) *cannedAPI {
	return &cannedAPI{responses: map[string]any{
		"GET /api/v1/namespaces/liken-system/pods/media-operator-59f4c8d7b5-2xk9q": Pod{
			Metadata: ObjectMeta{Name: "media-operator-59f4c8d7b5-2xk9q", Namespace: "liken-system"},
			Spec: PodSpec{Containers: []Container{
				{Name: "operator", Image: image},
			}},
		},
	}}
}

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
		display: "ghcr.io/liken-sh/media-operator-display:2026.09.03-007",
	})
}

// A set variable overrides its image, and the others are still derived.
func TestResolveImagesUsesOneEnvironmentOverride(t *testing.T) {
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
		display: "ghcr.io/liken-sh/media-operator-display:2026.09.03-007",
	})
}

// With every variable set, an operator image named by digest can start.
func TestResolveImagesUsesEveryEnvironmentOverrideWithoutReadingAPod(t *testing.T) {
	t.Setenv(playerImageVariable, "ghcr.io/liken-sh/media-operator-player:2026.09.03-007")
	t.Setenv(idleImageVariable, "ghcr.io/liken-sh/media-operator-idle:2026.09.03-007")
	t.Setenv(sidecarImageVariable, "ghcr.io/liken-sh/media-operator-sidecar:2026.09.03-007")
	t.Setenv(displayImageVariable, "ghcr.io/liken-sh/media-operator-display:2026.09.03-007")
	api := &cannedAPI{}

	images, err := resolveImages(testAPIClient(t, api.handler()))

	mustSucceed(t, err)
	mustMatch(t, images, companionImages{
		player:  "ghcr.io/liken-sh/media-operator-player:2026.09.03-007",
		idle:    "ghcr.io/liken-sh/media-operator-idle:2026.09.03-007",
		sidecar: "ghcr.io/liken-sh/media-operator-sidecar:2026.09.03-007",
		display: "ghcr.io/liken-sh/media-operator-display:2026.09.03-007",
	})
	mustMatch(t, len(api.requests), 0)
}

func TestResolveImagesFailsOnAPodWithNoTag(t *testing.T) {
	t.Setenv(podNameVariable, "media-operator-59f4c8d7b5-2xk9q")
	t.Setenv(podNamespaceVariable, "liken-system")
	api := operatorPodAPI("ghcr.io/liken-sh/media-operator@sha256:c0ffee")

	_, err := resolveImages(testAPIClient(t, api.handler()))

	mustFail(t, err)
}

func TestResolveImagesFailsWithNoOperatorContainer(t *testing.T) {
	t.Setenv(podNameVariable, "media-operator-59f4c8d7b5-2xk9q")
	t.Setenv(podNamespaceVariable, "liken-system")
	api := &cannedAPI{responses: map[string]any{
		"GET /api/v1/namespaces/liken-system/pods/media-operator-59f4c8d7b5-2xk9q": Pod{
			Metadata: ObjectMeta{Name: "media-operator-59f4c8d7b5-2xk9q", Namespace: "liken-system"},
			Spec:     PodSpec{Containers: []Container{{Name: "bus", Image: "eclipse-mosquitto:2.1.4-alpine"}}},
		},
	}}

	_, err := resolveImages(testAPIClient(t, api.handler()))

	mustFail(t, err)
}

// With neither the downward API variables nor the image variables set,
// startup fails before it sends a request.
func TestResolveImagesFailsWithoutTheDownwardAPI(t *testing.T) {
	api := &cannedAPI{}

	_, err := resolveImages(testAPIClient(t, api.handler()))

	mustFail(t, err)
	mustMatch(t, len(api.requests), 0)
}

func TestOperatorVersionReadsTheOperatorsOwnTag(t *testing.T) {
	t.Setenv(podNameVariable, "media-operator-59f4c8d7b5-2xk9q")
	t.Setenv(podNamespaceVariable, "liken-system")
	api := operatorPodAPI("ghcr.io/liken-sh/media-operator:2026.09.03-007")

	mustMatch(t, operatorVersion(testAPIClient(t, api.handler())), "2026.09.03-007")
}

func TestOperatorVersionIsEmptyWhenItCannotBeRead(t *testing.T) {
	cases := []struct {
		name string
		env  bool
		api  *cannedAPI
	}{
		{name: "no downward API", env: false, api: &cannedAPI{}},
		{name: "no such pod", env: true, api: &cannedAPI{}},
		{name: "an image named by digest", env: true, api: operatorPodAPI("ghcr.io/liken-sh/media-operator@sha256:c0ffee")},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			if each.env {
				t.Setenv(podNameVariable, "media-operator-59f4c8d7b5-2xk9q")
				t.Setenv(podNamespaceVariable, "liken-system")
			}
			mustMatch(t, operatorVersion(testAPIClient(t, each.api.handler())), "")
		})
	}
}

// The api role reads its version from the tag of its own container, the
// one that deploy/api.yaml declares.
func TestContainerVersionReadsTheTagOfTheNamedContainer(t *testing.T) {
	t.Setenv(podNameVariable, "media-api-6c7d9f4b88-t4r2m")
	t.Setenv(podNamespaceVariable, "liken-system")
	api := &cannedAPI{responses: map[string]any{
		"GET /api/v1/namespaces/liken-system/pods/media-api-6c7d9f4b88-t4r2m": Pod{
			Metadata: ObjectMeta{Name: "media-api-6c7d9f4b88-t4r2m", Namespace: "liken-system"},
			Spec: PodSpec{Containers: []Container{
				{Name: apiContainerName, Image: "ghcr.io/liken-sh/media-operator-api:2026.09.16-001"},
			}},
		},
	}}
	client := testAPIClient(t, api.handler())

	mustMatch(t, containerVersion(client, apiContainerName), "2026.09.16-001")
	mustMatch(t, containerVersion(client, operatorContainerName), "")
}
