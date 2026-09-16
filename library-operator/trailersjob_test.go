package main

// What these tests read: the Job the trailerfile fact runs in, the image it
// remuxes on, the claims it mounts, and the settings that reach its
// container.

import (
	"testing"
)

// A Ready provider of one block, as the pass would have checked it.
func readyBlockProvider(name, namespace string, spec MetadataProviderSpec) *MetadataProvider {
	return &MetadataProvider{
		Metadata: ObjectMeta{Name: name, Namespace: namespace, UID: name + "-uid"},
		Spec:     spec,
		Status: MetadataProviderStatus{Conditions: []Condition{
			{Type: conditionReady, Status: ConditionTrue, Reason: reasonReachable},
		}},
	}
}

// The trailers Job of one Library, built the way a pass builds it.
func testTrailersJob(library *Library, providers providerSet, path string) *Job {
	return buildTrailersJob(library, providers, nil, trailersJobName(library.Metadata.Name),
		path, testFFmpegImage, testCorrosionImage)
}

// The pod holds the catalog agent and the one container that runs the fact,
// on the image that holds ffmpeg and ffprobe.
func TestTheTrailersJobRunsTheOneFact(t *testing.T) {
	library, providers := libraryWithTrailers()

	spec := testTrailersJob(library, providers, "").Spec.Template.Spec

	if len(spec.InitContainers) != 1 || spec.InitContainers[0].Name != catalogContainer {
		t.Fatalf("initContainers = %+v, want the catalog agent alone", spec.InitContainers)
	}
	if len(spec.Containers) != 1 {
		t.Fatalf("containers = %+v, want the one trailers container", spec.Containers)
	}
	pulls := spec.Containers[0]
	if pulls.Name != trailerFileContainerName || pulls.Image != testFFmpegImage {
		t.Errorf("the container is %q on %q, want %q on the ffmpeg image",
			pulls.Name, pulls.Image, trailerFileContainerName)
	}
	if len(pulls.Command) != 2 || pulls.Command[1] != factsMode {
		t.Errorf("the container runs %v, want the binary in the facts role", pulls.Command)
	}
	environment := containerEnvironment(pulls)
	if environment[libraryFactsVariable] != factTrailerFile {
		t.Errorf("%s = %q, want %q", libraryFactsVariable,
			environment[libraryFactsVariable], factTrailerFile)
	}
	if environment[libraryWorkerVariable] != workerTrailers {
		t.Errorf("%s = %q, want %q", libraryWorkerVariable,
			environment[libraryWorkerVariable], workerTrailers)
	}
	if pulls.Resources.Limits["memory"] != trailersMemoryLimit {
		t.Errorf("memory limit = %q, want %q", pulls.Resources.Limits["memory"],
			trailersMemoryLimit)
	}
}

// The container reads the source order and the address of every site it can
// fetch from, because it holds no API credential of its own.
func TestTheTrailersContainerReadsItsSites(t *testing.T) {
	library := studioMovies()
	library.Spec.Trailers.Enabled = true
	library.Spec.Sources = []string{"archive", "videos"}
	providers := providerSet{
		libraryKey("house", "archive"): readyBlockProvider("archive", "house",
			MetadataProviderSpec{Archive: &ProviderArchive{}}),
		libraryKey("house", "videos"): readyBlockProvider("videos", "house",
			MetadataProviderSpec{PeerTube: &ProviderPeerTube{Endpoint: "https://videos.example"}}),
	}

	spec := testTrailersJob(library, providers, "").Spec.Template.Spec
	environment := containerEnvironment(spec.Containers[0])

	if environment[librarySourcesVariable] != "archive,peertube" {
		t.Errorf("%s = %q, want the blocks in the Library's own order",
			librarySourcesVariable, environment[librarySourcesVariable])
	}
	if got := environment[providerEndpointVariable(providerBlockPeerTube)]; got != "https://videos.example" {
		t.Errorf("%s = %q, want the instance's own address",
			providerEndpointVariable(providerBlockPeerTube), got)
	}
	if *spec.AutomountServiceAccountToken {
		t.Error("the pod mounts a service account token, want none")
	}
}

// The Job runs its pod once, mounts the media volume read-write, and keeps a
// catalog claim of its own, so it never waits on the enricher's.
func TestTheTrailersJobRunsOnceOnItsOwnClaim(t *testing.T) {
	library, providers := libraryWithTrailers()

	job := testTrailersJob(library, providers, "")

	spec := job.Spec.Template.Spec
	if spec.RestartPolicy != "Never" {
		t.Errorf("restartPolicy = %q, want Never", spec.RestartPolicy)
	}
	if job.Metadata.Labels[workerLabelKey] != workerTrailers {
		t.Errorf("labels = %v, want the trailers worker label", job.Metadata.Labels)
	}
	claims := map[string]string{}
	for _, volume := range spec.Volumes {
		claims[volume.Name] = volume.PersistentVolumeClaim.ClaimName
	}
	if claims[catalogVolumeName] != "movies-trailers-catalog" {
		t.Errorf("catalog volume = %q, want the trailers Job's own claim",
			claims[catalogVolumeName])
	}
	if claims[catalogVolumeName] == enrichCatalogClaimName("movies") ||
		claims[catalogVolumeName] == trickplayCatalogClaimName("movies") {
		t.Error("the trailers Job shares another Job's claim, want a claim of its own")
	}
	if claims[libraryVolumeName] != "movies" {
		t.Errorf("library volume = %q, want the Library's claim", claims[libraryVolumeName])
	}
	mounts := spec.Containers[0].VolumeMounts
	if len(mounts) != 1 || mounts[0].Name != libraryVolumeName || mounts[0].ReadOnly {
		t.Errorf("the container mounts %+v, want the library volume read-write", mounts)
	}
}
