package main

// what these tests read: the cleanup pod field by field, the claim, the
// missing media volume, the labels.

import (
	"slices"
	"testing"
	"time"
)

func testCleanupPod(library *Library) *Pod {
	return buildCleanupPod(library, testScannerImage, testCorrosionImage)
}

// name, namespace, and owner tie the pod to the Library so the collector
// takes it after a release.
func TestCleanupPodBelongsToItsLibrary(t *testing.T) {
	pod := testCleanupPod(studioMovies())

	if pod.Metadata.Name != "movies-cleanup" {
		t.Errorf("name = %q, want movies-cleanup", pod.Metadata.Name)
	}
	if pod.Metadata.Namespace != "house" {
		t.Errorf("namespace = %q, want house", pod.Metadata.Namespace)
	}
	want := OwnerReference{
		APIVersion: libraryAPIVersion, Kind: "Library",
		Name: "movies", UID: "library-uid", Controller: true,
	}
	if !slices.Equal(pod.Metadata.OwnerReferences, []OwnerReference{want}) {
		t.Errorf("ownerReferences = %+v, want %+v", pod.Metadata.OwnerReferences, want)
	}
}

// the cleanup pod's own name label keeps it out of the webhook Service,
// which it could not answer.
func TestCleanupPodIsNotSelectedByTheWebhookService(t *testing.T) {
	library := studioMovies()
	labels := testCleanupPod(library).Metadata.Labels

	selected := true
	for key, value := range buildWebhookService(library).Spec.Selector {
		if labels[key] != value {
			selected = false
		}
	}

	if selected {
		t.Errorf("labels = %v are selected by the webhook Service", labels)
	}
}

// the shared library label lists both of a Library's pods with one selector.
func TestCleanupPodNamesItsLibrary(t *testing.T) {
	pod := testCleanupPod(studioMovies())

	if got := pod.Metadata.Labels[libraryLabelKey]; got != "movies" {
		t.Errorf("%s = %q, want movies", libraryLabelKey, got)
	}
	if got := pod.Metadata.Labels[scannerLabelKey]; got != cleanupLabelValue {
		t.Errorf("%s = %q, want %s", scannerLabelKey, got, cleanupLabelValue)
	}
}

// it mounts the catalog claim, which holds every row, and no media volume,
// because it reads none.
func TestCleanupPodMountsTheCatalogClaimAlone(t *testing.T) {
	pod := testCleanupPod(studioMovies())

	if len(pod.Spec.Volumes) != 1 {
		t.Fatalf("volumes = %+v, want the catalog claim alone", pod.Spec.Volumes)
	}
	volume := pod.Spec.Volumes[0]
	if volume.Name != catalogVolumeName {
		t.Errorf("volume = %q, want %s", volume.Name, catalogVolumeName)
	}
	if volume.PersistentVolumeClaim.ClaimName != "movies-catalog" {
		t.Errorf("claim = %q, want movies-catalog", volume.PersistentVolumeClaim.ClaimName)
	}
}

// runs to completion, holds no credential, gains no capability: every
// restriction the scanner runs under, without the walk.
func TestCleanupPodRunsUnprivilegedAndWithNoToken(t *testing.T) {
	pod := testCleanupPod(studioMovies())

	if pod.Spec.RestartPolicy != "Never" {
		t.Errorf("restartPolicy = %q, want Never", pod.Spec.RestartPolicy)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Error("the cleanup pod mounts a ServiceAccount token")
	}
	for _, container := range append(slices.Clone(pod.Spec.InitContainers), pod.Spec.Containers...) {
		if container.SecurityContext == nil {
			t.Errorf("%s carries no security context", container.Name)
			continue
		}
		if !slices.Contains(container.SecurityContext.Capabilities.Drop, "ALL") {
			t.Errorf("%s does not drop every capability", container.Name)
		}
	}
}

// the same native catalog sidecar and probes the scanner runs, so the
// sweep never reaches a dead API.
func TestCleanupPodRunsTheSameCatalogSidecar(t *testing.T) {
	pod := testCleanupPod(studioMovies())

	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("initContainers = %+v, want the catalog agent alone", pod.Spec.InitContainers)
	}
	agent := pod.Spec.InitContainers[0]
	if agent.Name != catalogContainer || agent.Image != testCorrosionImage {
		t.Errorf("agent = %s on %s, want the catalog image", agent.Name, agent.Image)
	}
	if agent.RestartPolicy != "Always" {
		t.Errorf("restartPolicy = %q, want the native sidecar's Always", agent.RestartPolicy)
	}
	if agent.StartupProbe == nil || agent.LivenessProbe == nil {
		t.Error("the catalog agent carries fewer than the two probes the scanner pod gives it")
	}
}

// identity comes from the environment alone (it holds no credential), and
// the image is always this operator's.
func TestCleanupPodCarriesTheLibraryIdentity(t *testing.T) {
	library := studioMovies()
	library.Spec.Movies = &LibrarySettings{Image: "example.test/somebody-elses-scanner:1"}
	pod := testCleanupPod(library)

	container := pod.Spec.Containers[0]
	if container.Image != testScannerImage {
		t.Errorf("image = %q, want this operator's own image", container.Image)
	}
	if !slices.Equal(container.Command, []string{"/library-operator", cleanupMode}) {
		t.Errorf("command = %v, want the cleanup role", container.Command)
	}
	want := map[string]string{
		libraryNamespaceVariable: "house",
		libraryNameVariable:      "movies",
		catalogAPIVariable:       defaultCatalogAPI,
	}
	got := map[string]string{}
	for _, variable := range container.Env {
		got[variable.Name] = variable.Value
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
	if len(container.Env) != len(want) {
		t.Errorf("env = %+v, want the three settings alone", container.Env)
	}
	if len(container.VolumeMounts) != 0 {
		t.Errorf("volumeMounts = %+v, want none: the sweeper reads no volume", container.VolumeMounts)
	}
}

// a blocked stand retries on a growing per-library wait, capped, so the
// operator gives up on no timer.
func TestTheCleanupStandBacksOff(t *testing.T) {
	baseWas, capWas := cleanupBackoffBase, cleanupBackoffCap
	t.Cleanup(func() { cleanupBackoffBase, cleanupBackoffCap = baseWas, capWas })
	cleanupBackoffBase = 20 * time.Millisecond
	cleanupBackoffCap = 40 * time.Millisecond
	operator := &operator{cleanupStands: map[string]cleanupStand{}}

	if !operator.mayStandCleanup("house/movies") {
		t.Fatal("the first stand was refused")
	}
	if operator.mayStandCleanup("house/movies") {
		t.Error("a second stand ran before the wait")
	}
	if !operator.mayStandCleanup("studio/films") {
		t.Error("one library's backoff held another library back")
	}

	time.Sleep(2 * cleanupBackoffCap)
	if !operator.mayStandCleanup("house/movies") {
		t.Error("the stand after the wait was refused")
	}
}

// the wait doubles from the base and stops at the cap, so a long block
// never overflows the delay.
func TestTheCleanupBackoffStopsAtTheCap(t *testing.T) {
	cases := []struct {
		count int
		want  time.Duration
	}{
		{count: 1, want: cleanupBackoffBase},
		{count: 2, want: 2 * cleanupBackoffBase},
		{count: 3, want: 4 * cleanupBackoffBase},
		{count: 40, want: cleanupBackoffCap},
	}
	for _, one := range cases {
		if got := cleanupBackoffDelay(one.count); got != one.want {
			t.Errorf("delay after %d stands = %s, want %s", one.count, got, one.want)
		}
	}
}
