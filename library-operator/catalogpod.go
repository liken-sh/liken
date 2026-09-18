package main

// The catalog pods are what a Catalog becomes at run time: one durable
// copy per replica the Catalog asks for, owned by the Catalog, every one
// of them on the store's one claim. The first copy reports
// what it holds over the bus. They are the long-running members of the gossip
// cluster, and every worker Job joins that cluster for the length of its
// run. The agent's write API is loopback only, and the reporter reads it
// from inside its own pod. The agent also answers on the pod network,
// with the Prometheus metrics port Corrosion's own configuration opens
// (corrosion/config.toml), which the catalog PodMonitor scrapes.

import (
	"context"
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

// The pod that the Catalog creates. It is a function of the Catalog
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
			// The catalog pod is a long-running service and not a run to
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
					ClaimName: catalogClaimFor(catalog),
				}},
			},
		},
	}
}

// The containers one copy of the catalog runs, as the native
// sidecars and the containers beside them. Every copy carries the confirmer
// over its agent, because every copy is a copy a Job can hand off to. The
// first copy carries the reporter as well, because one namespace publishes
// one report.
func catalogPodContainers(catalog *NamespaceCatalog, index int, scannerImage, corrosionImage, busAddress, topicBase string) ([]Container, []Container) {
	agent := catalogSidecar(corrosionImage)
	confirm := confirmerSidecar(scannerImage)
	if index > 0 {
		return []Container{agent}, []Container{confirm}
	}
	return []Container{agent}, []Container{reporterSidecar(catalog, scannerImage, busAddress, topicBase), confirm}
}

// The container that confirms a Job's run against this copy. It runs
// this operator's own image in its confirm role, it reads the agent beside
// it over loopback, and it learns the pod it speaks for from the downward
// API, because the confirmations row is keyed by that name.
func confirmerSidecar(image string) Container {
	return Container{
		Name:    confirmerContainer,
		Image:   image,
		Command: []string{"/library-operator", confirmMode},
		Env: []EnvVar{
			{Name: catalogAPIVariable, Value: defaultCatalogAPI},
			{Name: podNameVariable, ValueFrom: &EnvVarSource{
				FieldRef: &ObjectFieldSelector{FieldPath: podNameFieldPath},
			}},
		},
		Resources: ResourceRequirements{
			Requests: map[string]string{"cpu": scannerCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": scannerMemoryLimit},
		},
		SecurityContext: unprivileged(),
	}
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

// Reconcile each requested catalog replica in index order, then remove
// replicas above the requested count. On failure, return the error with
// the pod results from the replicas already processed.
//
// Create the claim before the pods, because every replica mounts it.
// With a class other than per-node, run only one replica because the
// claim binds to one node.
func (o *operator) standCatalogPods(ctx context.Context, catalog *NamespaceCatalog) ([]*Pod, error) {
	store := catalogStoreOf(catalog)
	wanted, err := o.storeCopies(ctx, store, catalogReplicaCount(catalog))
	if err != nil {
		return nil, err
	}
	if err := o.standCatalogPodClaim(ctx, catalog); err != nil {
		return nil, err
	}
	pods := make([]*Pod, wanted)
	for index := range wanted {
		pod, err := o.standCatalogPod(ctx, catalog, index)
		if err != nil {
			return pods, err
		}
		pods[index] = pod
	}
	return pods, o.sweepStoreReplicas(ctx, catalog, store, wanted)
}

// Return the existing pod if it matches the template, or create and
// return a pod if none exists. If this pass deletes an outdated pod,
// return nil, as the operator does for its other pods.
func (o *operator) standCatalogPod(ctx context.Context, catalog *NamespaceCatalog, index int) (*Pod, error) {
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

// An absent claim is created and an existing one is left alone, the
// rule standClaim holds, because a claim's spec is immutable once it
// binds. A Catalog that names a claim of its own creates none: the claim
// is the person's, and the operator mounts it. A person's own claim gets
// no volume either, because the operator writes volumes for its own
// claims alone and removes only those.
func (o *operator) standCatalogPodClaim(ctx context.Context, catalog *NamespaceCatalog) error {
	if catalog.Spec.Storage.ClaimName != "" {
		return nil
	}
	return o.standClaim(ctx, buildCatalogPodClaim(catalog))
}
