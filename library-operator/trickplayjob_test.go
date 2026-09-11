package main

// What these tests read: the Job the trickplay fact runs in, the image it
// decodes on, the claims it mounts, and the render node its pod holds.

import (
	"testing"
)

// The trickplay Job of one Library, built the way a pass builds it.
func testTrickplayJob(library *Library, path string) *Job {
	return buildTrickplayJob(library, trickplayJobName(library.Metadata.Name), path,
		testFFmpegImage, testCorrosionImage, testBusAddress, defaultTopicBase)
}

// The pod holds the catalog agent and the one container that runs the trickplay
// fact, on the image that carries ffmpeg.
func TestTheTrickplayJobRunsTheOneFact(t *testing.T) {
	job := testTrickplayJob(studioMovies(), "")

	spec := job.Spec.Template.Spec
	if len(spec.InitContainers) != 1 || spec.InitContainers[0].Name != catalogContainer {
		t.Fatalf("initContainers = %+v, want the catalog agent alone", spec.InitContainers)
	}
	if len(spec.Containers) != 1 {
		t.Fatalf("containers = %+v, want the one trickplay container", spec.Containers)
	}
	tiles := spec.Containers[0]
	if tiles.Name != trickplayContainerName || tiles.Image != testFFmpegImage {
		t.Errorf("the container is %q on %q, want %q on the ffmpeg image",
			tiles.Name, tiles.Image, trickplayContainerName)
	}
	if len(tiles.Command) != 2 || tiles.Command[1] != factsMode {
		t.Errorf("the container runs %v, want the binary in the facts role", tiles.Command)
	}
	if got := containerEnvironment(tiles)[libraryFactsVariable]; got != factTrickplay {
		t.Errorf("%s = %q, want %q", libraryFactsVariable, got, factTrickplay)
	}
}

// The decode takes a memory line and a CPU request of its own, because it reads
// a video where every other container reads rows.
func TestTheTrickplayContainerAsksForMoreThanAScanner(t *testing.T) {
	tiles := testTrickplayJob(studioMovies(), "").Spec.Template.Spec.Containers[0]

	if tiles.Resources.Limits["memory"] != trickplayMemoryLimit ||
		trickplayMemoryLimit == scannerMemoryLimit {
		t.Errorf("memory limit = %q, want %q, above the scanner's %q",
			tiles.Resources.Limits["memory"], trickplayMemoryLimit, scannerMemoryLimit)
	}
	if tiles.Resources.Requests["cpu"] != trickplayCPURequest ||
		trickplayCPURequest == scannerCPURequest {
		t.Errorf("cpu request = %q, want %q, above the scanner's %q",
			tiles.Resources.Requests["cpu"], trickplayCPURequest, scannerCPURequest)
	}
}

// The Job runs its pod once and mounts the media volume read-write and a
// catalog claim its agent keeps to itself, so it never waits on the enricher's.
func TestTheTrickplayJobRunsOnceOnItsOwnClaim(t *testing.T) {
	job := testTrickplayJob(studioMovies(), "")

	spec := job.Spec.Template.Spec
	if spec.RestartPolicy != "Never" {
		t.Errorf("restartPolicy = %q, want Never", spec.RestartPolicy)
	}
	if job.Metadata.Labels[workerLabelKey] != workerTrickplay {
		t.Errorf("labels = %v, want the trickplay worker label", job.Metadata.Labels)
	}
	claims := map[string]string{}
	for _, volume := range spec.Volumes {
		claims[volume.Name] = volume.PersistentVolumeClaim.ClaimName
	}
	if claims[catalogVolumeName] != "movies-trickplay-catalog" {
		t.Errorf("catalog volume = %q, want the trickplay Job's own claim", claims[catalogVolumeName])
	}
	if claims[catalogVolumeName] == enrichCatalogClaimName("movies") {
		t.Error("the trickplay Job mounts the enricher's claim, want a claim of its own")
	}
	if claims[libraryVolumeName] != "movies" {
		t.Errorf("library volume = %q, want the Library's claim", claims[libraryVolumeName])
	}
	mounts := spec.Containers[0].VolumeMounts
	if len(mounts) != 1 || mounts[0].Name != libraryVolumeName || mounts[0].ReadOnly {
		t.Errorf("the container mounts %+v, want the library volume read-write", mounts)
	}
}

// The pod holds the render claim where the Library names a render block, and
// none where it names nothing, which is the software path.
func TestTheTrickplayPodHoldsTheRenderClaim(t *testing.T) {
	cases := []struct {
		name   string
		render *TrickplayDevice
		want   string
	}{
		{name: "a library that names a render node",
			render: &TrickplayDevice{Class: "gpu.liken.sh"}, want: "movies-trickplay"},
		{name: "a library that names none"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			library := studioMovies()
			library.Spec.Trickplay.Render = test.render

			spec := testTrickplayJob(library, "").Spec.Template.Spec

			held := map[string]string{}
			for _, claim := range spec.ResourceClaims {
				held[claim.Name] = claim.ResourceClaimTemplateName
			}
			if held[renderRequestName] != test.want {
				t.Errorf("the pod claims %+v, want the template %q", spec.ResourceClaims, test.want)
			}
			taken := map[string]bool{}
			for _, claim := range spec.Containers[0].Resources.Claims {
				taken[claim.Name] = true
			}
			if taken[renderRequestName] != (test.want != "") {
				t.Errorf("the container takes %+v, want it to take the render claim: %t",
					spec.Containers[0].Resources.Claims, test.want != "")
			}
		})
	}
}
