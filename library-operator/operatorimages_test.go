package main

// These tests prove the derivation against reference strings, and
// the start-up against a pod the fake API server serves.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDerivingTheCompanionImages(t *testing.T) {
	cases := []struct {
		name      string
		reference string
		corrosion string
		browser   string
	}{
		{name: "a release tag",
			reference: "ghcr.io/liken-sh/library-operator:2026.09.03-007",
			corrosion: "ghcr.io/liken-sh/library-operator-corrosion:2026.09.03-007",
			browser:   "ghcr.io/liken-sh/library-operator-media-browser:2026.09.03-007"},
		{name: "a development build",
			reference: "ghcr.io/liken-sh/library-operator:2026.09.03-007-dev-003-abcdef01",
			corrosion: "ghcr.io/liken-sh/library-operator-corrosion:2026.09.03-007-dev-003-abcdef01",
			browser:   "ghcr.io/liken-sh/library-operator-media-browser:2026.09.03-007-dev-003-abcdef01"},
		{name: "a registry with a port",
			reference: "registry:5000/liken-sh/library-operator:2026.09.03-007",
			corrosion: "registry:5000/liken-sh/library-operator-corrosion:2026.09.03-007",
			browser:   "registry:5000/liken-sh/library-operator-media-browser:2026.09.03-007"},
		{name: "no registry",
			reference: "library-operator:2026.09.03-007",
			corrosion: "library-operator-corrosion:2026.09.03-007",
			browser:   "library-operator-media-browser:2026.09.03-007"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			derived, err := deriveImages(one.reference)

			if err != nil {
				t.Fatal(err)
			}
			want := images{scanner: one.reference, corrosion: one.corrosion, browser: one.browser}
			if derived != want {
				t.Errorf("images = %+v, want %+v", derived, want)
			}
		})
	}
}

func TestAReferenceWithNoTagDerivesNothing(t *testing.T) {
	const digest = "@sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
	cases := []struct {
		name      string
		reference string
	}{
		{name: "a digest", reference: "ghcr.io/liken-sh/library-operator" + digest},
		{name: "a tag and a digest", reference: "ghcr.io/liken-sh/library-operator:2026.09.03-007" + digest},
		{name: "no tag", reference: "ghcr.io/liken-sh/library-operator"},
		{name: "no tag and no registry", reference: "library-operator"},
		{name: "a registry port and no tag", reference: "registry:5000/liken-sh/library-operator"},
		{name: "an empty tag", reference: "ghcr.io/liken-sh/library-operator:"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			_, err := deriveImages(one.reference)

			if err == nil || !strings.Contains(err.Error(), one.reference) {
				t.Fatalf("err = %v, want it to name %s", err, one.reference)
			}
		})
	}
}

// The operator's own pod as the fake cluster serves it. It carries a
// second container, so the derivation has to pick the one named
// operator.
const testOperatorPod = "library-operator-6d8f4c9b7-2xqzt"

func operatorPod(image string) *Pod {
	return &Pod{
		Metadata: ObjectMeta{Name: testOperatorPod, Namespace: testOperatorNamespace},
		Spec: PodSpec{Containers: []Container{
			{Name: "sidecar", Image: "ghcr.io/liken-sh/something-else:1"},
			{Name: operatorContainer, Image: image},
		}},
	}
}

// testImageClient is a client against the fake cluster, the API the
// operator reads its own pod through at start-up.
func testImageClient(t *testing.T, cluster *fakeCluster) *Client {
	t.Helper()
	server := httptest.NewServer(cluster.handler())
	t.Cleanup(server.Close)
	t.Setenv(scannerImageVariable, "")
	t.Setenv(corrosionImageVariable, "")
	t.Setenv(browserImageVariable, "")
	t.Setenv(podNameVariable, testOperatorPod)
	return NewClient(server.URL, server.Client(), "")
}

func TestTheImagesComeFromTheOperatorsOwnPod(t *testing.T) {
	cluster := newFakeCluster()
	cluster.pods[testOperatorPod] = operatorPod("ghcr.io/liken-sh/library-operator:2026.09.03-007")
	client := testImageClient(t, cluster)

	got, err := operatorImages(context.Background(), client, testOperatorNamespace)

	if err != nil {
		t.Fatal(err)
	}
	want := images{
		scanner:   "ghcr.io/liken-sh/library-operator:2026.09.03-007",
		corrosion: "ghcr.io/liken-sh/library-operator-corrosion:2026.09.03-007",
		browser:   "ghcr.io/liken-sh/library-operator-media-browser:2026.09.03-007",
	}
	if got != want {
		t.Errorf("images = %+v, want %+v", got, want)
	}
}

func TestOneImageVariableWinsAndTheRestDerive(t *testing.T) {
	cluster := newFakeCluster()
	cluster.pods[testOperatorPod] = operatorPod("ghcr.io/liken-sh/library-operator:2026.09.03-007")
	client := testImageClient(t, cluster)
	t.Setenv(browserImageVariable, "ghcr.io/liken-sh/library-operator-media-browser:mine")

	got, err := operatorImages(context.Background(), client, testOperatorNamespace)

	if err != nil {
		t.Fatal(err)
	}
	want := images{
		scanner:   "ghcr.io/liken-sh/library-operator:2026.09.03-007",
		corrosion: "ghcr.io/liken-sh/library-operator-corrosion:2026.09.03-007",
		browser:   "ghcr.io/liken-sh/library-operator-media-browser:mine",
	}
	if got != want {
		t.Errorf("images = %+v, want %+v", got, want)
	}
}

func TestEveryImageVariableSetReadsNoPod(t *testing.T) {
	cluster := newFakeCluster()
	client := testImageClient(t, cluster)
	t.Setenv(scannerImageVariable, testScannerImage)
	t.Setenv(corrosionImageVariable, testCorrosionImage)
	t.Setenv(browserImageVariable, testBrowserImage)
	t.Setenv(podNameVariable, "")

	got, err := operatorImages(context.Background(), client, testOperatorNamespace)

	if err != nil {
		t.Fatal(err)
	}
	want := images{scanner: testScannerImage, corrosion: testCorrosionImage, browser: testBrowserImage}
	if got != want {
		t.Errorf("images = %+v, want %+v", got, want)
	}
	if cluster.countRequests("GET", "pods") != 0 {
		t.Error("the operator read its pod with every image already named")
	}
}

func TestTheImagesFailWhenThePodCannotBeRead(t *testing.T) {
	cases := []struct {
		name    string
		pod     *Pod
		podName string
		want    string
	}{
		{name: "no pod name", podName: "", want: podNameVariable},
		{name: "no such pod", podName: testOperatorPod, want: "not found"},
		{name: "no operator container", podName: testOperatorPod,
			pod:  &Pod{Metadata: ObjectMeta{Name: testOperatorPod}},
			want: operatorContainer},
		{name: "an image with no tag", podName: testOperatorPod,
			pod:  operatorPod("ghcr.io/liken-sh/library-operator"),
			want: "ghcr.io/liken-sh/library-operator"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			if one.pod != nil {
				cluster.pods[testOperatorPod] = one.pod
			}
			client := testImageClient(t, cluster)
			t.Setenv(podNameVariable, one.podName)

			_, err := operatorImages(context.Background(), client, testOperatorNamespace)

			if err == nil || !strings.Contains(err.Error(), one.want) {
				t.Fatalf("err = %v, want it to name %s", err, one.want)
			}
		})
	}
}
