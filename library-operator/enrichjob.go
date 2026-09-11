package main

// enrichjob.go builds the enricher Job of one Library and stands the claim
// its catalog agent runs on. The order of the facts is the order of the
// containers in the pod, so a person reads it with kubectl get pod, and the
// operator holds no order of its own.

import (
	"context"
	"encoding/json"
	"strings"
)

// The fixed part of every enricher Job's name, and the claim its catalog
// agent runs on. A standing Job and a chain Job each add their own suffix,
// and the claim keeps the fixed name, so one Library has one claim.
func enrichJobName(library string) string {
	return library + "-enrich"
}

func enrichCatalogClaimName(library string) string {
	return enrichJobName(library) + "-catalog"
}

// The volume the enricher's agent runs on. It is separate from the scan
// Jobs' claim, so a folder enrich never waits on the claim a scan holds,
// and it keeps the agent's actor id and rows between runs, so a run
// syncs a delta. It binds to the libraries' class, as the scan claim
// does, because it is a working copy and not the catalog of record.
func buildEnrichClaim(library *Library, catalog *NamespaceCatalog) *PersistentVolumeClaim {
	return &PersistentVolumeClaim{
		APIVersion: claimAPIVersion,
		Kind:       "PersistentVolumeClaim",
		Metadata: ObjectMeta{
			Name:            enrichCatalogClaimName(library.Metadata.Name),
			Namespace:       library.Metadata.Namespace,
			Labels:          libraryLabels(library.Metadata.Name),
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: PersistentVolumeClaimSpec{
			AccessModes: []string{accessModeReadWriteOnce},
			Resources: VolumeResourceRequirements{
				Requests: map[string]string{"storage": catalogStorageSize(catalog)},
			},
			StorageClassName: libraryStorageClass(catalog),
		},
	}
}

// The claim is provisioned once and never rewritten, the rule standClaim
// holds, because a claim's spec is immutable once it binds.
func (o *operator) standEnrichClaim(ctx context.Context, library *Library, catalog *NamespaceCatalog) error {
	return o.standClaim(ctx, buildEnrichClaim(library, catalog))
}

// The enricher Job. The name is the caller's, because a Library runs the
// standing enricher under the walk's name and a webhook's folder runs under
// the chain's. Sources that reach no Ready provider of the identity fact omit
// the identity container, which is how a Library with no Ready provider still
// runs the probe.
func buildEnrichJob(library *Library, providers providerSet, name, path string,
	scannerImage, corrosionImage, busAddress, topicBase string) *Job {
	backoff, ttl := int32(scanBackoffLimit), int32(scanJobTTL)
	return &Job{
		APIVersion: batchAPIVersion,
		Kind:       "Job",
		Metadata: ObjectMeta{
			Name:            name,
			Namespace:       library.Metadata.Namespace,
			Labels:          workerLabels(library.Metadata.Name, workerEnrich),
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: enrichPodTemplate(library, providers, path,
				scannerImage, corrosionImage, busAddress, topicBase),
		},
	}
}

// The pod the enricher Job runs. The facts that must run in order are init
// containers, and the enrich container is the one regular container: it
// writes the runs row last and waits for the echo.
func enrichPodTemplate(library *Library, providers providerSet, path string,
	scannerImage, corrosionImage, busAddress, topicBase string) PodTemplateSpec {
	grace := int64(scannerGracePeriod)
	// An enricher holds no Kubernetes credential. It reads its work through the
	// agent beside it and takes the provider key through a secretKeyRef, so
	// nothing in this pod reads the API server.
	noToken := false

	// The agent starts first, and the facts run in order behind it, because
	// the kubelet starts an init container only when the one before it is up.
	// The facts here edit the same sidecar file, so they must never run at
	// once.
	facts := []Container{
		probeContainer(library, path, scannerImage, busAddress, topicBase),
		// The arrival container runs on every Library, because the fact asks no
		// provider. It runs after the probe, because the probe container writes the
		// run's started mark.
		factsContainer(library, arrivalContainerName, []string{factArrival}, path, scannerImage, busAddress, topicBase),
	}
	if providers.serving(library.Metadata.Namespace, library.Spec.Sources, factIdentity) != nil {
		facts = append(facts, factsContainer(library, factIdentity, []string{factIdentity}, path,
			scannerImage, busAddress, topicBase))
	}
	// The nfo container: one phase that runs every fact of the nfo group in
	// order, each fact reading the .nfo and writing its own element group. It
	// names the facts the Library's own sources serve. It runs before the art
	// container because a plot costs one call and an image costs a download, so
	// the cheap facts land first.
	if served := servedNFOFacts(library, providers); len(served) > 0 {
		facts = append(facts, factsContainer(library, nfoContainerName, served, path,
			scannerImage, busAddress, topicBase))
	}
	// The art container. It runs where a Ready provider of the Library's sources
	// serves one of the art facts, and it takes a memory line of its own because
	// it holds an image while it writes it. It is an init container because the
	// enrich container must run last, and a regular container beside it would
	// let the run end before the art is written. Plan 57 makes it a regular
	// container once a second fan-out container exists.
	if served := servedArtFacts(library, providers); len(served) > 0 {
		images := factsContainer(library, artContainerName, served, path,
			scannerImage, busAddress, topicBase)
		images.Resources.Limits = map[string]string{"memory": artMemoryLimit}
		facts = append(facts, images)
	}
	// The contributors container, which fills the people the credits fact named.
	// It runs after the art container, and it is an init container for the same
	// reason the art container is: the enrich container must run last. Plan 57
	// makes both of them regular containers that run at once.
	if providers.servingContributors(library.Metadata.Namespace, library.Spec.Sources) != nil {
		facts = append(facts, factsContainer(library, contributorsContainerName, contributorFactNames,
			path, scannerImage, busAddress, topicBase))
	}
	// The trickplay container. It runs only where the Library turns the fact on,
	// because a first pass over a whole library is hours of CPU. It asks no
	// provider: the file alone answers it. It takes a memory line and a CPU
	// request of its own because it decodes a video where every other container
	// reads rows. It is an init container for the reason the art container is:
	// The enrich container must run last. Plan 57 makes it a regular container
	// once the fan-out exists.
	if library.Spec.Trickplay.Enabled {
		tiles := factsContainer(library, trickplayContainerName, []string{factTrickplay},
			path, scannerImage, busAddress, topicBase)
		tiles.Resources.Requests["cpu"] = trickplayCPURequest
		tiles.Resources.Limits = map[string]string{"memory": trickplayMemoryLimit}
		facts = append(facts, tiles)
	}
	// The same environment carries the source order, so a container asks its
	// providers in the order spec.sources names them.
	keys := providerEnv(library, providers)
	for index := range facts {
		facts[index].Env = append(facts[index].Env, keys...)
	}
	sequence := append([]Container{catalogSidecar(corrosionImage)}, facts...)

	return PodTemplateSpec{
		Metadata: ObjectMeta{
			Labels: withMemberLabel(workerLabels(library.Metadata.Name, workerEnrich)),
		},
		Spec: PodSpec{
			RestartPolicy:                 "Never",
			TerminationGracePeriodSeconds: &grace,
			AutomountServiceAccountToken:  &noToken,
			InitContainers:                sequence,
			Containers: []Container{
				enrichContainer(library, enrichMode, enrichMode, path, scannerImage, busAddress, topicBase),
			},
			Volumes: []Volume{
				{Name: catalogVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
					ClaimName: enrichCatalogClaimName(library.Metadata.Name),
				}},
				// The enricher mounts the volume read-write, where every scan Job mounts
				// it read-only, because the facts it fills in are files beside the media.
				// The volume is the claim a screen reads, so an enricher of a franchises
				// library writes beside the art and never into the checkout.
				{Name: libraryVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
					ClaimName: library.Spec.screenClaim(),
				}},
			},
		},
	}
}

// One phase's container. Its name is the phase and its command is the role,
// and it learns everything else from the environment, because it holds no
// credential to look a Library up with. The kind's own image is the scanner's
// alone: a scanner a person supplies is not an enricher.
func enrichContainer(library *Library, name, role, path, image, busAddress, topicBase string) Container {
	return Container{
		Name:    name,
		Image:   image,
		Command: []string{"/library-operator", role},
		Env: []EnvVar{
			{Name: libraryNamespaceVariable, Value: library.Metadata.Namespace},
			{Name: libraryNameVariable, Value: library.Metadata.Name},
			{Name: libraryKindVariable, Value: library.Spec.Kind},
			{Name: libraryRootVariable, Value: library.Spec.Storage.Root},
			{Name: busAddressVariable, Value: busAddress},
			{Name: topicBaseVariable, Value: topicBase},
			{Name: catalogAPIVariable, Value: defaultCatalogAPI},
			{Name: libraryIgnoreVariable, Value: ignoreValue(library)},
			{Name: libraryRefreshVariable, Value: refreshValue(library)},
			{Name: scanPathVariable, Value: path},
			{Name: echoTimeoutVariable, Value: defaultEchoTimeout.String()},
			{Name: syncTimeoutVariable, Value: defaultSyncTimeout.String()},
			{Name: jobNameVariable, ValueFrom: &EnvVarSource{
				FieldRef: &ObjectFieldSelector{FieldPath: jobNameFieldPath},
			}},
		},
		VolumeMounts: []VolumeMount{
			{Name: libraryVolumeName, MountPath: libraryMountPath},
		},
		Resources: ResourceRequirements{
			Requests: map[string]string{"cpu": scannerCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": scannerMemoryLimit},
		},
		SecurityContext: unprivileged(),
	}
}

// A container that runs facts. Its name is the phase, and LIBRARY_FACTS names
// the facts it runs in order, so the pod reads as the sequence and one
// container fills more than one gap.
func factsContainer(library *Library, name string, facts []string,
	path, image, busAddress, topicBase string) Container {
	container := enrichContainer(library, name, factsMode, path, image, busAddress, topicBase)
	container.Env = append(container.Env,
		EnvVar{Name: libraryFactsVariable, Value: strings.Join(facts, ",")})
	return container
}

// The refresh times travel as one JSON value, the way the ignore list
// does, so a fact of any name reaches the container whole. A Library
// that names none writes an empty value.
func refreshValue(library *Library) string {
	if len(library.Spec.Refresh) == 0 {
		return ""
	}
	refresh, _ := json.Marshal(library.Spec.Refresh)
	return string(refresh)
}

// ffprobe is a child process of the probe container, so its memory counts
// against the container's limit. It holds about 60 MB on its own while it
// reads one file, whatever the file's size, which is more than the scanner's
// limit allows.
const probeMemoryLimit = "256Mi"

// The probe container: the one fact that runs a child process, with a memory
// line of its own for it.
func probeContainer(library *Library, path, scannerImage, busAddress, topicBase string) Container {
	probe := factsContainer(library, factProbe, []string{factProbe}, path, scannerImage, busAddress, topicBase)
	probe.Resources.Limits = map[string]string{"memory": probeMemoryLimit}
	return probe
}
