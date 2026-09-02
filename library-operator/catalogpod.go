package main

// The catalog pod is what a Catalog becomes at run time: one
// standing pod per namespace, owned by the Catalog, holding the
// namespace's durable catalog on a claim and reporting what it holds
// over the bus. It is the standing member of the gossip cluster, and
// every worker Job joins that cluster for the length of its run. It
// answers on no port: the agent's API is loopback only, and the
// reporter reads it from inside the pod.

import (
	"context"
	"errors"
)

// The pod one Catalog becomes, named from the Catalog, so every
// pass names the same pod and the operator keeps no record of it.
func catalogPodName(catalog string) string {
	return catalog + "-catalog"
}

// The label pair the catalog pod carries: the name label that
// tells it from a Job's pod and a screen pod, and the member label the
// namespace's EndpointSlice is written over.
func catalogPodLabels() map[string]string {
	return withMemberLabel(map[string]string{scannerLabelKey: catalogLabelValue})
}

// The pod the Catalog stands. It is a function of the Catalog
// and the operator's own settings alone, so two passes over an
// unchanged Catalog build the same pod, which is what makes the
// template hash mean anything.
func buildCatalogPod(catalog *NamespaceCatalog, scannerImage, corrosionImage, busAddress, topicBase string) *Pod {
	grace := int64(scannerGracePeriod)
	// The reporter holds no Kubernetes credential; it publishes over
	// the bus, and the operator alone writes a status.
	noToken := false
	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:            catalogPodName(catalog.Metadata.Name),
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
			InitContainers: []Container{
				catalogSidecar(corrosionImage),
			},
			Containers: []Container{
				reporterSidecar(catalog, scannerImage, busAddress, topicBase),
			},
			Volumes: []Volume{
				{Name: catalogVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
					ClaimName: catalogClaimFor(catalog),
				}},
			},
		},
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

// The pod that stands for one Catalog after this pass, on the
// same terms as every other pod this operator stands: the live pod when
// it matches the template, the created pod when there was none, and nil
// when this pass deleted a stale one.
func (o *operator) standCatalogPod(ctx context.Context, catalog *NamespaceCatalog) (*Pod, error) {
	if err := o.standCatalogPodClaim(ctx, catalog); err != nil {
		return nil, err
	}
	desired := buildCatalogPod(catalog, o.scannerImage, o.corrosionImage, o.busAddress, o.topicBase)
	return o.standPod(ctx, desired)
}

// The pod that holds the namespace's catalog, out of the pods
// the pass listed, or nil when it does not stand yet.
func catalogPodOf(catalog *NamespaceCatalog, pods []Pod) *Pod {
	if catalog == nil {
		return nil
	}
	name := catalogPodName(catalog.Metadata.Name)
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
func (o *operator) standCatalogPodClaim(ctx context.Context, catalog *NamespaceCatalog) error {
	if catalog.Spec.Storage.ClaimName != "" {
		return nil
	}
	namespace, name := catalog.Metadata.Namespace, catalogPodClaimName(catalog.Metadata.Name)

	_, err := GetPersistentVolumeClaim(ctx, o.client, namespace, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	_, err = CreatePersistentVolumeClaim(ctx, o.client, buildCatalogPodClaim(catalog))
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}
