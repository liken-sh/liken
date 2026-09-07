package main

// The catalog pods are what a Catalog becomes at run time: one durable
// copy per replica the Catalog asks for, owned by the Catalog, each with
// the namespace's catalog on a claim of its own. The first copy reports
// what it holds over the bus. They are the standing members of the gossip
// cluster, and every worker Job joins that cluster for the length of its
// run. They answer on no port: the agent's API is loopback only, and the
// reporter reads it from inside its own pod.

import (
	"context"
	"errors"
)

// The name every durable copy of the namespace's catalog is numbered
// from. It derives from the Catalog, so every pass names the same pods
// and the operator keeps no record of them.
func catalogStoreName(catalog string) string {
	return catalog + "-catalog"
}

// The label pair the catalog pod carries: the name label that
// tells it from a Job's pod and a screen pod, and the member label the
// namespace's EndpointSlice is written over.
//
// The store label is the third. It tells a durable copy of the catalog
// from every other pod that holds a catalog agent.
func catalogPodLabels() map[string]string {
	return withMemberLabel(map[string]string{
		scannerLabelKey: catalogLabelValue,
		storeLabelKey:   catalogStoreLabelValue,
	})
}

// The pod the Catalog stands. It is a function of the Catalog
// and the operator's own settings alone, so two passes over an
// unchanged Catalog build the same pod, which is what makes the
// template hash mean anything.
//
// buildCatalogPod builds one copy of the catalog. The index names which
// copy this is.
func buildCatalogPod(catalog *NamespaceCatalog, index int, scannerImage, corrosionImage, busAddress, topicBase string) *Pod {
	store := catalogStoreOf(catalog)
	grace := int64(scannerGracePeriod)
	// The reporter holds no Kubernetes credential; it publishes over
	// the bus, and the operator alone writes a status.
	noToken := false
	sidecars, containers := catalogPodContainers(catalog, index, scannerImage, corrosionImage, busAddress, topicBase)
	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:            store.replicaName(index),
			Namespace:       catalog.Metadata.Namespace,
			Labels:          catalogPodLabels(),
			OwnerReferences: []OwnerReference{catalogObjectOwner(catalog)},
		},
		Spec: PodSpec{
			// The catalog pod is a standing service and not a run to
			// completion, so the kubelet restarts a container that
			// exits rather than letting the pod end.
			RestartPolicy:                 "Always",
			TerminationGracePeriodSeconds: &grace,
			AutomountServiceAccountToken:  &noToken,
			Affinity:                      store.antiAffinity(),
			InitContainers:                sidecars,
			Containers:                    containers,
			Volumes: []Volume{
				{Name: catalogVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
					ClaimName: catalogReplicaClaim(catalog, index),
				}},
			},
		},
	}
}

// The containers one copy of the catalog runs, as the native sidecars and
// the containers beside them. The first copy carries the reporter over
// its agent. Every copy after it is the agent alone, because one namespace
// publishes one report.
func catalogPodContainers(catalog *NamespaceCatalog, index int, scannerImage, corrosionImage, busAddress, topicBase string) ([]Container, []Container) {
	agent := catalogSidecar(corrosionImage)
	if index > 0 {
		return nil, []Container{replicaAgent(agent)}
	}
	return []Container{agent}, []Container{reporterSidecar(catalog, scannerImage, busAddress, topicBase)}
}

// The container that reads the loopback catalog API and
// publishes each library's report over the bus. It runs this operator's
// own image in its report role, and it learns the namespace it reports
// on from its environment alone, because it holds no API credential.
func reporterSidecar(catalog *NamespaceCatalog, image, busAddress, topicBase string) Container {
	return Container{
		Name:    reporterContainer,
		Image:   image,
		Command: []string{"/library-operator", reportMode},
		Env: []EnvVar{
			{Name: libraryNamespaceVariable, Value: catalog.Metadata.Namespace},
			{Name: busAddressVariable, Value: busAddress},
			{Name: topicBaseVariable, Value: topicBase},
			{Name: catalogAPIVariable, Value: defaultCatalogAPI},
		},
		Resources: ResourceRequirements{
			Requests: map[string]string{"cpu": scannerCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": scannerMemoryLimit},
		},
		SecurityContext: unprivileged(),
	}
}

// Stand every durable copy of the catalog this Catalog asks for, in index
// order, and take down the copies above that count. A failure on one copy
// ends the stand, and the pass reports it with the copies that already
// stood.
func (o *operator) standCatalogPods(ctx context.Context, catalog *NamespaceCatalog) ([]*Pod, error) {
	wanted := catalogReplicaCount(catalog)
	pods := make([]*Pod, wanted)
	for index := range wanted {
		pod, err := o.standCatalogPod(ctx, catalog, index)
		if err != nil {
			return pods, err
		}
		pods[index] = pod
	}
	return pods, o.sweepStoreReplicas(ctx, catalog, catalogStoreOf(catalog), wanted)
}

// The pod that stands for one Catalog after this pass, on the
// same terms as every other pod this operator stands: the live pod when
// it matches the template, the created pod when there was none, and nil
// when this pass deleted a stale one.
func (o *operator) standCatalogPod(ctx context.Context, catalog *NamespaceCatalog, index int) (*Pod, error) {
	if err := o.standCatalogPodClaim(ctx, catalog, index); err != nil {
		return nil, err
	}
	desired := buildCatalogPod(catalog, index, o.scannerImage, o.corrosionImage, o.busAddress, o.topicBase)
	return o.standPod(ctx, desired)
}

// The copy that reports the namespace's catalog, out of the pods the pass
// listed, or nil when it does not stand yet. It is copy zero, because that
// copy alone carries the reporter.
func catalogPodOf(catalog *NamespaceCatalog, pods []Pod) *Pod {
	if catalog == nil {
		return nil
	}
	name := catalogStoreOf(catalog).replicaName(0)
	for index := range pods {
		pod := &pods[index]
		if pod.Metadata.Namespace == catalog.Metadata.Namespace && pod.Metadata.Name == name {
			return pod
		}
	}
	return nil
}

// The reason and message the Catalog's Ready condition carries
// while its pod is not up, so a person reads one object to find what
// the namespace's catalog waits on.
func catalogPodBlocker(pod *Pod) (string, string) {
	switch {
	case pod == nil:
		return catalogReasonPodPending, "there is no catalog pod yet"
	case pod.Status.Phase == podFailed:
		return catalogReasonPodFailed, podFailureMessage(pod)
	case pod.Status.Phase != podRunning || !everyContainerReady(pod):
		return catalogReasonPodPending, podPendingMessage(pod)
	}
	return "", ""
}

// An absent claim is created and an existing one is left alone,
// the rule standCatalogClaim follows, because a claim's spec is
// immutable once it binds. A Catalog that names a claim of its own
// creates none: the claim is the person's, and the operator mounts it.
//
// The claim a Catalog names is the first copy's alone. Every copy after
// it takes a claim the operator provisions.
func (o *operator) standCatalogPodClaim(ctx context.Context, catalog *NamespaceCatalog, index int) error {
	if index == 0 && catalog.Spec.Storage.ClaimName != "" {
		return nil
	}
	namespace, name := catalog.Metadata.Namespace, catalogStoreOf(catalog).replicaName(index)

	_, err := GetPersistentVolumeClaim(ctx, o.client, namespace, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	_, err = CreatePersistentVolumeClaim(ctx, o.client, buildCatalogPodClaim(catalog, index))
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}
