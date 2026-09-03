package main

// enrichjob.go builds the enricher Job of one Library and stands the claim
// its catalog agent runs on. The order of the facts is the order of the
// containers in the pod, so a person reads it with kubectl get pod, and the
// operator holds no order of its own.

import (
	"context"
	"errors"
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

// The volume the enricher's agent runs on. It is separate from the scan Jobs'
// claim, so a folder enrich never waits on the ReadWriteOnce a scan holds,
// and it keeps the agent's actor id and rows between runs, so a run syncs a
// delta.
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
			StorageClassName: catalog.Spec.Storage.StorageClassName,
		},
	}
}

// The claim is provisioned once and never rewritten, the rule
// standCatalogClaim follows, because a claim's spec is immutable once it
// binds.
func (o *operator) standEnrichClaim(ctx context.Context, library *Library, catalog *NamespaceCatalog) error {
	namespace := library.Metadata.Namespace
	name := enrichCatalogClaimName(library.Metadata.Name)

	_, err := GetPersistentVolumeClaim(ctx, o.client, namespace, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	_, err = CreatePersistentVolumeClaim(ctx, o.client, buildEnrichClaim(library, catalog))
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
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
	// Both facts here edit the same sidecar file, so they must never run at
	// once.
	facts := []Container{
		factsContainer(library, factProbe, []string{factProbe}, path, scannerImage, busAddress, topicBase),
	}
	if providers.serving(library.Metadata.Namespace, library.Spec.Sources, factIdentity) != nil {
		facts = append(facts, factsContainer(library, factIdentity, []string{factIdentity}, path,
			scannerImage, busAddress, topicBase))
	}
	// Every facts container carries every key the sources reach, so a container
	// that asks a second provider needs no wiring of its own.
	keys := providerKeyEnv(library, providers)
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
				{Name: libraryVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
					ClaimName: library.Spec.Storage.Claim,
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
