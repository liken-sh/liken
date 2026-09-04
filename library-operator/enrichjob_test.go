package main

// what these tests read: the enricher Job one Library becomes, the
// order of the containers in its pod, the key the identity container
// receives, and the claim its catalog agent runs on.

import (
	"testing"
	"time"
)

// the provider a Library's sources resolve to, as the pass would have
// checked it.
func readyProvider(name, namespace string, facts ...string) *MetadataProvider {
	provider := seedProvider(newFakeCluster(), name, namespace, facts...)
	provider.Status.Conditions = []Condition{
		{Type: conditionReady, Status: ConditionTrue, Reason: reasonReachable},
	}
	return provider
}

// the enricher Job of one Library, built the way a pass builds it. Every
// provider named here is a source of the Library, in the order given.
func testEnrichJob(library *Library, path string, providers ...*MetadataProvider) *Job {
	set := providerSet{}
	for _, provider := range providers {
		set[libraryKey(provider.Metadata.Namespace, provider.Metadata.Name)] = provider
		library.Spec.Sources = append(library.Spec.Sources, provider.Metadata.Name)
	}
	return buildEnrichJob(library, set, enrichJobName(library.Metadata.Name), path,
		testScannerImage, testCorrosionImage, testBusAddress, defaultTopicBase)
}

// the pod holds the catalog agent, then the two facts that edit the
// sidecar in order, then the container that writes the runs row.
func TestEnrichJobHoldsItsContainersInOrder(t *testing.T) {
	job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house", factIdentity))

	spec := job.Spec.Template.Spec
	names := []string{}
	for _, container := range spec.InitContainers {
		names = append(names, container.Name)
	}
	want := []string{catalogContainer, factProbe, arrivalContainerName, factIdentity}
	if len(names) != len(want) {
		t.Fatalf("initContainers = %v, want %v", names, want)
	}
	for index, name := range want {
		if names[index] != name {
			t.Errorf("initContainer %d = %q, want %q", index, names[index], name)
		}
	}
	if len(spec.Containers) != 1 || spec.Containers[0].Name != enrichMode {
		t.Fatalf("containers = %+v, want the one enrich container", spec.Containers)
	}
}

// each init container after the agent runs the facts role and names the one
// fact it fills, and the enrich container runs its own role.
func TestEnrichJobNamesTheFactsOfEachContainer(t *testing.T) {
	job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house", factIdentity))

	spec := job.Spec.Template.Spec
	for _, container := range spec.InitContainers[1:] {
		if len(container.Command) != 2 || container.Command[1] != factsMode {
			t.Errorf("%s runs %v, want the binary in the facts role", container.Name, container.Command)
		}
		if got := containerEnvironment(container)[libraryFactsVariable]; got != container.Name {
			t.Errorf("%s reads %s = %q, want its own fact", container.Name, libraryFactsVariable, got)
		}
	}
	enrich := spec.Containers[0]
	if len(enrich.Command) != 2 || enrich.Command[1] != enrichMode {
		t.Errorf("%s runs %v, want the binary in the enrich role", enrich.Name, enrich.Command)
	}
	if got := containerEnvironment(enrich)[libraryFactsVariable]; got != "" {
		t.Errorf("%s reads %s = %q, want none", enrich.Name, libraryFactsVariable, got)
	}
}

// the enricher writes the volume, where a scanner reads it, and its
// agent runs on a claim of the Library's own.
func TestEnrichJobMountsTheVolumeReadWrite(t *testing.T) {
	job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house", factIdentity))

	spec := job.Spec.Template.Spec
	for _, container := range append(spec.InitContainers[1:], spec.Containers...) {
		if len(container.VolumeMounts) != 1 {
			t.Fatalf("%s mounts %+v, want the library volume alone", container.Name, container.VolumeMounts)
		}
		mount := container.VolumeMounts[0]
		if mount.Name != libraryVolumeName || mount.MountPath != libraryMountPath || mount.ReadOnly {
			t.Errorf("%s mounts %+v, want the library volume read-write", container.Name, mount)
		}
	}
	claims := map[string]string{}
	for _, volume := range spec.Volumes {
		claims[volume.Name] = volume.PersistentVolumeClaim.ClaimName
	}
	if claims[libraryVolumeName] != "movies" {
		t.Errorf("library volume = %q, want the Library's claim", claims[libraryVolumeName])
	}
	if claims[catalogVolumeName] != "movies-enrich-catalog" {
		t.Errorf("catalog volume = %q, want the enricher's own claim", claims[catalogVolumeName])
	}
}

// the key reaches the identity container through a secretKeyRef, so no
// container reads the API server.
func TestEnrichJobPassesTheKeyThroughASecretKeyRef(t *testing.T) {
	job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house", factIdentity))

	identity := job.Spec.Template.Spec.InitContainers[3]
	var reference *SecretKeySelector
	for _, variable := range identity.Env {
		if variable.Name == tmdbTokenVariable && variable.ValueFrom != nil {
			reference = variable.ValueFrom.SecretKeyRef
		}
	}
	if reference == nil {
		t.Fatalf("%s reads no Secret, env = %+v", tmdbTokenVariable, identity.Env)
	}
	if reference.Name != "tmdb-key" || reference.Key != "token" {
		t.Errorf("secretKeyRef = %+v, want the provider's own Secret and key", reference)
	}
}

// Every facts container carries every key the Library's sources reach, so a
// container that asks a second provider needs no wiring of its own. The
// catalog agent carries none, because it asks no provider.
func TestEnrichJobCarriesEveryProviderKeyIntoEveryFactsContainer(t *testing.T) {
	job := testEnrichJob(studioMovies(), "",
		readyProvider("tmdb", "house", factIdentity),
		providerOfBlock("omdb", providerBlockOMDb))

	spec := job.Spec.Template.Spec
	for _, container := range spec.InitContainers[1:] {
		keys := map[string]*SecretKeySelector{}
		for _, variable := range container.Env {
			if variable.ValueFrom != nil && variable.ValueFrom.SecretKeyRef != nil {
				keys[variable.Name] = variable.ValueFrom.SecretKeyRef
			}
		}
		if len(keys) != 2 || keys[tmdbTokenVariable] == nil {
			t.Errorf("%s reads %v, want the key of each account", container.Name, keys)
		}
		if reference := keys[providerTokenVariable(providerBlockOMDb)]; reference == nil ||
			reference.Name != "omdb-key" {
			t.Errorf("%s reads %+v for OMDb, want the account's own Secret", container.Name, reference)
		}
	}
	for _, variable := range spec.InitContainers[0].Env {
		if variable.ValueFrom != nil && variable.ValueFrom.SecretKeyRef != nil {
			t.Errorf("the catalog agent reads %s, want no provider key", variable.Name)
		}
	}
}

// A Library whose sources name no ready provider that serves identity
// still runs the probe and the arrival fact, the two that ask no
// provider.
func TestEnrichJobWithoutAProviderRunsTheProviderFreeFactsAlone(t *testing.T) {
	job := testEnrichJob(studioMovies(), "")

	spec := job.Spec.Template.Spec
	if len(spec.InitContainers) != 3 || spec.InitContainers[1].Name != factProbe ||
		spec.InitContainers[2].Name != arrivalContainerName {
		t.Fatalf("initContainers = %+v, want the agent, the probe, and the arrival fact", spec.InitContainers)
	}
}

// every container learns its Library, its broker, and the Job it runs
// from its environment, and a Job for one folder names that folder.
func TestEnrichJobCarriesTheJobEnvironment(t *testing.T) {
	cases := []struct{ name, path string }{
		{name: "the whole library"},
		{name: "one folder", path: "/library/movies/Arrival (2016)"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			job := testEnrichJob(studioMovies(), one.path, readyProvider("tmdb", "house", factIdentity))

			spec := job.Spec.Template.Spec
			for _, container := range append(spec.InitContainers[1:], spec.Containers...) {
				got := containerEnvironment(container)
				if got[libraryNameVariable] != "movies" || got[libraryKindVariable] != libraryKindMovies {
					t.Errorf("%s reads %v, want the Library it serves", container.Name, got)
				}
				if got[busAddressVariable] != testBusAddress || got[topicBaseVariable] != defaultTopicBase {
					t.Errorf("%s reads %v, want the broker and the topic base", container.Name, got)
				}
				if got[echoTimeoutVariable] != defaultEchoTimeout.String() {
					t.Errorf("%s reads %s = %q", container.Name, echoTimeoutVariable, got[echoTimeoutVariable])
				}
				if got[syncTimeoutVariable] != defaultSyncTimeout.String() {
					t.Errorf("%s reads %s = %q", container.Name, syncTimeoutVariable, got[syncTimeoutVariable])
				}
				if got[scanPathVariable] != one.path {
					t.Errorf("%s reads %s = %q, want %q", container.Name,
						scanPathVariable, got[scanPathVariable], one.path)
				}
				if !readsTheJobName(container) {
					t.Errorf("%s reads no %s", container.Name, jobNameVariable)
				}
			}
		})
	}
}

// whether a container reads its own Job's name off the pod, which is
// what its runs row carries.
func readsTheJobName(container Container) bool {
	for _, variable := range container.Env {
		if variable.Name != jobNameVariable || variable.ValueFrom == nil {
			continue
		}
		return variable.ValueFrom.FieldRef != nil && variable.ValueFrom.FieldRef.FieldPath == jobNameFieldPath
	}
	return false
}

// the Job belongs to its Library, runs to completion, and stays for an
// hour after it finishes.
func TestEnrichJobBelongsToItsLibrary(t *testing.T) {
	job := testEnrichJob(studioMovies(), "")

	if job.Metadata.Name != "movies-enrich" || job.Metadata.Namespace != "house" {
		t.Errorf("metadata = %+v, want the Library's own enricher", job.Metadata)
	}
	if job.Metadata.Labels[workerLabelKey] != workerEnrich {
		t.Errorf("labels = %v, want the enrich worker label", job.Metadata.Labels)
	}
	if len(job.Metadata.OwnerReferences) != 1 || job.Metadata.OwnerReferences[0].Name != "movies" {
		t.Errorf("ownerReferences = %+v, want the Library", job.Metadata.OwnerReferences)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != scanBackoffLimit {
		t.Errorf("backoffLimit = %+v, want %d", job.Spec.BackoffLimit, scanBackoffLimit)
	}
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != scanJobTTL {
		t.Errorf("ttlSecondsAfterFinished = %+v, want %d", job.Spec.TTLSecondsAfterFinished, scanJobTTL)
	}
	if job.Spec.Template.Spec.Containers[0].SecurityContext == nil {
		t.Error("the enrich container carries no security context")
	}
}

// the enricher's claim is sized by the namespace's Catalog and classed
// by its durable storage, and it is created once.
func TestStandEnrichClaimCreatesItOnce(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	catalog := testNamespaceCatalog()
	catalog.Spec.Storage.Size = "2Gi"
	catalog.Spec.Storage.StorageClassName = "local-path"
	operator := testOperator(t, cluster)

	for range 2 {
		if err := operator.standEnrichClaim(t.Context(), library, catalog); err != nil {
			t.Fatal(err)
		}
	}

	claim := cluster.heldClaim("movies-enrich-catalog")
	if claim == nil {
		t.Fatal("the pass stood no claim for the enricher")
	}
	if claim.Spec.Resources.Requests["storage"] != "2Gi" || claim.Spec.StorageClassName != "local-path" {
		t.Errorf("claim spec = %+v, want the Catalog's size and class", claim.Spec)
	}
	if !slicesContainsOwner(claim.Metadata.OwnerReferences, "movies") {
		t.Errorf("ownerReferences = %+v, want the Library", claim.Metadata.OwnerReferences)
	}
	if got := cluster.countRequests("POST", "persistentvolumeclaims"); got != 1 {
		t.Errorf("the pass created the claim %d times, want once", got)
	}
}

func slicesContainsOwner(owners []OwnerReference, name string) bool {
	for _, owner := range owners {
		if owner.Name == name {
			return true
		}
	}
	return false
}

// The art container runs where a provider of the Library's sources serves an
// art fact, and never where none does.
func TestTheArtContainerRunsWhereAProviderServesArt(t *testing.T) {
	cases := []struct {
		name  string
		facts []string
		want  bool
	}{
		{name: "a provider that serves every fact", facts: nil, want: true},
		{name: "a provider narrowed to one art fact", facts: []string{factPoster}, want: true},
		{name: "a provider narrowed to the identity", facts: []string{factIdentity}, want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house", test.facts...))

			held := false
			for _, container := range job.Spec.Template.Spec.InitContainers {
				if container.Name == artContainerName {
					held = true
				}
			}
			if held != test.want {
				t.Errorf("the pod holds the art container: %t, want %t", held, test.want)
			}
		})
	}
}

// The art container names every art fact, takes the provider key, holds a
// memory line of its own above the scanner's, and mounts the volume the way
// the facts before it do.
func TestTheArtContainerNamesItsFactsAndItsMemory(t *testing.T) {
	job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house"))

	var art Container
	for _, container := range job.Spec.Template.Spec.InitContainers {
		if container.Name == artContainerName {
			art = container
		}
	}
	if art.Name == "" {
		t.Fatal("the pod holds no art container")
	}
	facts := ""
	key := false
	for _, variable := range art.Env {
		if variable.Name == libraryFactsVariable {
			facts = variable.Value
		}
		if variable.Name == tmdbTokenVariable {
			key = variable.ValueFrom != nil && variable.ValueFrom.SecretKeyRef != nil
		}
	}
	want := "poster,backdrop,logo,season-poster,episode-thumb"
	if facts != want {
		t.Errorf("%s = %q, want %q", libraryFactsVariable, facts, want)
	}
	if !key {
		t.Error("the art container reads no provider key")
	}
	if art.Resources.Limits["memory"] != artMemoryLimit || artMemoryLimit == scannerMemoryLimit {
		t.Errorf("memory limit = %q, want %q, above the scanner's %q",
			art.Resources.Limits["memory"], artMemoryLimit, scannerMemoryLimit)
	}
	if len(art.VolumeMounts) != 1 || art.VolumeMounts[0].Name != libraryVolumeName ||
		art.VolumeMounts[0].ReadOnly {
		t.Errorf("mounts = %+v, want the library volume read-write", art.VolumeMounts)
	}
}

// The enrich container is still the last container of the Job, because it
// writes the runs row and waits for the echo.
func TestTheEnrichContainerStillRunsLast(t *testing.T) {
	job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house"))

	spec := job.Spec.Template.Spec
	last := spec.InitContainers[len(spec.InitContainers)-1]
	if last.Name != contributorsContainerName {
		t.Errorf("the last init container is %q, want the contributors container", last.Name)
	}
	if len(spec.Containers) != 1 || spec.Containers[0].Name != enrichMode {
		t.Fatalf("containers = %+v, want the one enrich container", spec.Containers)
	}
}

// the nfo container stands only where the Library's sources serve one of its
// facts, and it names the facts they serve in the order the group runs them.
func TestTheNFOContainerStandsWhereASourceServesItsFacts(t *testing.T) {
	cases := []struct {
		name  string
		facts []string
		want  string
	}{
		{name: "a provider that serves identity alone", facts: []string{factIdentity}},
		{name: "a provider narrowed to the overview", facts: []string{factIdentity, factOverview}, want: factOverview},
		{
			name:  "a provider that serves every nfo fact",
			facts: nil,
			want:  "overview,certification,rating.tmdb,credits",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house", test.facts...))

			var nfo *Container
			for at, container := range job.Spec.Template.Spec.InitContainers {
				if container.Name == nfoContainerName {
					nfo = &job.Spec.Template.Spec.InitContainers[at]
				}
			}
			if test.want == "" {
				if nfo != nil {
					t.Fatal("the pod holds an nfo container, want none")
				}
				return
			}
			if nfo == nil {
				t.Fatalf("the pod holds no nfo container, initContainers = %+v",
					job.Spec.Template.Spec.InitContainers)
			}
			if got := containerEnvironment(*nfo)[libraryFactsVariable]; got != test.want {
				t.Errorf("%s = %q, want %q", libraryFactsVariable, got, test.want)
			}
		})
	}
}

// the nfo container runs after the identity container, because a title takes
// its id before a fact asks about it, and before the art container.
func TestTheNFOContainerRunsAfterIdentityAndBeforeArt(t *testing.T) {
	job := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house"))

	names := []string{}
	for _, container := range job.Spec.Template.Spec.InitContainers {
		names = append(names, container.Name)
	}
	want := []string{catalogContainer, factProbe, arrivalContainerName, factIdentity, nfoContainerName,
		artContainerName, contributorsContainerName}
	if len(names) != len(want) {
		t.Fatalf("initContainers = %v, want %v", names, want)
	}
	for at, name := range want {
		if names[at] != name {
			t.Errorf("initContainer %d = %q, want %q", at, names[at], name)
		}
	}
}

// The refresh times travel as one JSON value into every container of
// the Job, so a fact of any name reaches the container whole, and a
// Library that names none writes an empty value.
func TestEnrichJobCarriesTheRefreshTimes(t *testing.T) {
	library := studioMovies()
	library.Spec.Refresh = map[string]time.Time{
		factCredits: time.Date(2026, 9, 3, 21, 0, 0, 0, time.UTC),
	}

	job := testEnrichJob(library, "", readyProvider("tmdb", "house", factIdentity))

	spec := job.Spec.Template.Spec
	for _, container := range append(spec.InitContainers[1:], spec.Containers...) {
		got := containerEnvironment(container)[libraryRefreshVariable]
		if got != `{"credits":"2026-09-03T21:00:00Z"}` {
			t.Errorf("%s reads %s = %q, want the JSON-encoded map",
				container.Name, libraryRefreshVariable, got)
		}
	}
	empty := testEnrichJob(studioMovies(), "", readyProvider("tmdb", "house", factIdentity))
	got := containerEnvironment(empty.Spec.Template.Spec.Containers[0])
	if got[libraryRefreshVariable] != "" {
		t.Errorf("%s = %q, want no value for a Library that names no refresh",
			libraryRefreshVariable, got[libraryRefreshVariable])
	}
}

// What the container reads back out of that environment, and what it
// reads out of a value it cannot parse: no refresh at all, because a
// container that took a bad value for a refresh would ask a provider
// about every title.
func TestTheContainerReadsTheRefreshTimesItIsGiven(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want refreshTimes
	}{
		{name: "no value at all", want: refreshTimes{}},
		{
			name: "the map the operator writes",
			raw:  `{"credits":"2026-09-03T21:00:00Z"}`,
			want: refreshTimes{factCredits: time.Date(2026, 9, 3, 21, 0, 0, 0, time.UTC)},
		},
		{name: "a value that is not JSON", raw: "{", want: refreshTimes{}},
		{name: "a time this image cannot read", raw: `{"credits":"soon"}`, want: refreshTimes{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := parseRefresh(test.raw)

			if len(got) != len(test.want) {
				t.Fatalf("refresh = %v, want %v", got, test.want)
			}
			for fact, at := range test.want {
				if !got[fact].Equal(at) {
					t.Errorf("refresh[%s] = %v, want %v", fact, got[fact], at)
				}
			}
		})
	}
}
