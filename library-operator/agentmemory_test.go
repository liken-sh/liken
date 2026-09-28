package main

import "testing"

// A Library's Job holds a higher limit for its agent than the standing
// pods, because the first Job on a node syncs the whole namespace onto an
// empty claim. The catalog, progress, and screen pods keep the lower
// limit.
func TestEachAgentCarriesTheMemoryLimitOfItsPod(t *testing.T) {
	cleanup := buildCleanupJob(studioMovies(), testScannerImage, testCorrosionImage).Spec.Template
	cases := []struct {
		name      string
		pod       *Pod
		container string
		limit     string
	}{
		{name: "the Library's walk Job", pod: testScanPod(studioMovies()), container: catalogContainer, limit: "1Gi"},
		{name: "the cleanup Job", pod: &Pod{Spec: cleanup.Spec}, container: catalogContainer, limit: "1Gi"},
		{name: "the catalog pod", pod: testCatalogPod(housekeepingCatalog(), 0), container: catalogContainer, limit: "512Mi"},
		{name: "the progress pod", pod: testProgressPod(housekeepingCatalog(), 0), container: progressContainer, limit: "512Mi"},
		{name: "the screen pod", pod: testScreenPod(denScreen(), houseLibraries()), container: catalogContainer, limit: "512Mi"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			resources := podContainer(t, one.pod, one.container).Resources
			if resources.Limits["memory"] != one.limit {
				t.Errorf("memory limit = %q, want %q", resources.Limits["memory"], one.limit)
			}
			if resources.Requests["memory"] != "64Mi" {
				t.Errorf("memory request = %q, want 64Mi", resources.Requests["memory"])
			}
		})
	}
}
