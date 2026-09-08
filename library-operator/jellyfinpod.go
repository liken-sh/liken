package main

// jellyfinpod.go stands the pod and the Service of the jellyfin role, one
// pair per namespace, beside the progress store. The role keeps a person's
// playback position the same in the progress store and in a Jellyfin server,
// in both directions.
//
// The pair stands only while the namespace's Catalog names a Jellyfin server
// in spec.jellyfin. A Catalog that drops the block loses the pod and the
// Service on the next pass.
//
// The pod holds no Kubernetes credential, like every pod this operator
// stands. It reads the server address and the API key from its environment,
// and everything else it needs crosses the bus.

import (
	"context"
	"errors"
	"maps"
	"slices"
)

// The container name a person reads in kubectl logs, and the name label that
// tells this pod from a catalog pod, a progress pod, and a screen pod. The
// pod carries neither gossip member label, so it reaches neither cluster's
// EndpointSlice.
const (
	jellyfinContainer  = "jellyfin"
	jellyfinLabelValue = "library-jellyfin"
)

// The port the role listens on for Jellyfin's webhook posts, and the name the
// Service reaches it by. The path is where the Webhook plugin posts, and the
// health path is what the kubelet reads.
const (
	jellyfinPort      = 8080
	jellyfinPortName  = "webhook"
	jellyfinProbePath = "/healthz"
)

// The pod and the Service take the Catalog's name with the suffix -jellyfin,
// so every pass names the same objects and the operator keeps no record of
// them.
func jellyfinName(catalog string) string {
	return catalog + "-jellyfin"
}

// The labels the pod carries and the Service selects on. They are one map, so
// the selector cannot drift from the pod.
func jellyfinLabels() map[string]string {
	return map[string]string{scannerLabelKey: jellyfinLabelValue}
}

// The pod the Catalog stands for its Jellyfin server. It is a function of the
// Catalog and the operator's own settings alone, so two passes over an
// unchanged Catalog build the same pod, which is what makes the template hash
// mean anything.
func buildJellyfinPod(catalog *NamespaceCatalog, operatorImage, busAddress, topicBase, mediaBase string) *Pod {
	grace := int64(scannerGracePeriod)
	// The role holds no Kubernetes credential. It reads the bus and the
	// Jellyfin server, and the operator alone reads the API.
	noToken := false
	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:            jellyfinName(catalog.Metadata.Name),
			Namespace:       catalog.Metadata.Namespace,
			Labels:          jellyfinLabels(),
			OwnerReferences: []OwnerReference{catalogObjectOwner(catalog)},
		},
		Spec: PodSpec{
			// The pod is a standing service and not a run to
			// completion, so the kubelet restarts a container
			// that exits rather than letting the pod end.
			RestartPolicy:                 "Always",
			TerminationGracePeriodSeconds: &grace,
			AutomountServiceAccountToken:  &noToken,
			Containers:                    []Container{jellyfinRole(catalog, operatorImage, busAddress, topicBase, mediaBase)},
		},
	}
}

// The container that reads the bus and the Jellyfin server. It runs this
// operator's own image in its jellyfin role, and it learns the namespace,
// both topic trees, the broker, the server, and the port it listens on from
// its environment alone. The API key arrives through a secretKeyRef, the way
// a provider key reaches an enricher, so the key never passes through the
// operator.
func jellyfinRole(catalog *NamespaceCatalog, image, busAddress, topicBase, mediaBase string) Container {
	jellyfin := catalog.Spec.Jellyfin
	return Container{
		Name:    jellyfinContainer,
		Image:   image,
		Command: []string{"/library-operator", jellyfinMode},
		Env: []EnvVar{
			{Name: libraryNamespaceVariable, Value: catalog.Metadata.Namespace},
			{Name: busAddressVariable, Value: busAddress},
			{Name: topicBaseVariable, Value: topicBase},
			{Name: mediaTopicBaseVariable, Value: mediaBase},
			{Name: jellyfinURLVariable, Value: jellyfin.URL},
			{Name: jellyfinAPIKeyVariable, ValueFrom: &EnvVarSource{
				SecretKeyRef: &SecretKeySelector{
					Name: jellyfin.SecretRef.Name,
					Key:  jellyfin.SecretRef.secretKey(),
				},
			}},
			{Name: jellyfinListenVariable, Value: defaultJellyfinListen},
		},
		Ports: []ContainerPort{
			{Name: jellyfinPortName, ContainerPort: jellyfinPort},
		},
		// The readinessProbe gates the pod's place in the endpoints
		// of the Service, so a post from Jellyfin reaches the role
		// only once the role listens.
		ReadinessProbe: &Probe{
			HTTPGet: &HTTPGetAction{Path: jellyfinProbePath, Port: jellyfinPort},
		},
		Resources: ResourceRequirements{
			Requests: map[string]string{"cpu": scannerCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": scannerMemoryLimit},
		},
		SecurityContext: unprivileged(),
	}
}

// The Service Jellyfin's Webhook plugin posts to. It states a selector, so
// the API server writes its endpoints from the pod's readiness, and it is not
// headless: the plugin dials one address and the proxy carries the post to
// the pod.
func buildJellyfinService(catalog *NamespaceCatalog) *Service {
	return &Service{
		APIVersion: serviceAPIVersion,
		Kind:       "Service",
		Metadata: ObjectMeta{
			Name:            jellyfinName(catalog.Metadata.Name),
			Namespace:       catalog.Metadata.Namespace,
			Labels:          jellyfinLabels(),
			OwnerReferences: []OwnerReference{catalogObjectOwner(catalog)},
		},
		Spec: ServiceSpec{
			Selector: jellyfinLabels(),
			Ports: []ServicePort{{
				Name:       jellyfinPortName,
				Protocol:   "TCP",
				Port:       jellyfinPort,
				TargetPort: jellyfinPortName,
			}},
		},
	}
}

// standJellyfin brings the namespace into line with what the Catalog states.
// A Catalog that names a Jellyfin server stands the pod and the Service, and
// a Catalog that names none takes both down.
func (o *operator) standJellyfin(ctx context.Context, catalog *NamespaceCatalog) error {
	if catalog.Spec.Jellyfin == nil {
		return o.retireJellyfin(ctx, catalog)
	}
	if _, err := o.standJellyfinPod(ctx, catalog); err != nil {
		return err
	}
	return o.standJellyfinService(ctx, catalog)
}

// The pod goes through standPod, so a missing pod is created and a pod built
// from another template is deleted and stood again on the next pass, the rule
// every pod this operator stands follows.
func (o *operator) standJellyfinPod(ctx context.Context, catalog *NamespaceCatalog) (*Pod, error) {
	desired := buildJellyfinPod(catalog, o.scannerImage, o.busAddress, o.topicBase, o.mediaTopicBase)
	return o.standPod(ctx, desired)
}

// The Service is written on divergence alone, the rule standGossipService
// holds. The update writes the live object with the operator's own fields set
// on it, because the API server assigned the address and both address fields
// are immutable.
func (o *operator) standJellyfinService(ctx context.Context, catalog *NamespaceCatalog) error {
	desired := buildJellyfinService(catalog)
	namespace, name := desired.Metadata.Namespace, desired.Metadata.Name

	live, err := GetService(ctx, o.client, namespace, name)
	if errors.Is(err, ErrNotFound) {
		_, err := CreateService(ctx, o.client, desired)
		if errors.Is(err, ErrConflict) {
			return nil
		}
		return err
	}
	if err != nil {
		return err
	}
	if sameJellyfinService(live, desired) {
		return nil
	}
	live.Metadata.Labels = desired.Metadata.Labels
	live.Metadata.OwnerReferences = desired.Metadata.OwnerReferences
	live.Spec.Selector = desired.Spec.Selector
	live.Spec.Ports = desired.Spec.Ports
	_, err = UpdateService(ctx, o.client, live)
	return err
}

// The comparison reads what the operator states and nothing else. The address
// the API server assigned is not compared, because a Service with an address
// is what a create answers and a comparison of it would rewrite the object on
// every pass.
func sameJellyfinService(live, desired *Service) bool {
	if !slices.Equal(live.Metadata.OwnerReferences, desired.Metadata.OwnerReferences) {
		return false
	}
	if !maps.Equal(live.Metadata.Labels, desired.Metadata.Labels) {
		return false
	}
	if !maps.Equal(live.Spec.Selector, desired.Spec.Selector) {
		return false
	}
	return slices.Equal(live.Spec.Ports, desired.Spec.Ports)
}

// retireJellyfin takes down the pair a Catalog no longer asks for. The name
// label guards both deletes, so an object another writer gave this name is
// left where it is, the rule retireStoreReplica follows.
func (o *operator) retireJellyfin(ctx context.Context, catalog *NamespaceCatalog) error {
	namespace, name := catalog.Metadata.Namespace, jellyfinName(catalog.Metadata.Name)

	pod, err := GetPod(ctx, o.client, namespace, name)
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return err
	case pod.Metadata.Labels[scannerLabelKey] == jellyfinLabelValue:
		if err := DeletePod(ctx, o.client, namespace, name); err != nil {
			return err
		}
	}

	service, err := GetService(ctx, o.client, namespace, name)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil
	case err != nil:
		return err
	case service.Metadata.Labels[scannerLabelKey] != jellyfinLabelValue:
		return nil
	}
	return DeleteService(ctx, o.client, namespace, name)
}
