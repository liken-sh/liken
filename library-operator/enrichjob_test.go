package main

// what these tests read: the enricher Job one Library becomes, the
// order of the containers in its pod, the key the identity container
// receives, and the claim its catalog agent runs on.

import (
	"testing"
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
	want := []string{catalogContainer, factProbe, factIdentity}
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

	identity := job.Spec.Template.Spec.InitContainers[2]
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

// a Library whose sources name no ready provider that serves identity
// still runs the probe.
func TestEnrichJobWithoutAProviderRunsTheProbeAlone(t *testing.T) {
	job := testEnrichJob(studioMovies(), "")

	spec := job.Spec.Template.Spec
	if len(spec.InitContainers) != 2 || spec.InitContainers[1].Name != factProbe {
		t.Fatalf("initContainers = %+v, want the agent and the probe", spec.InitContainers)
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
