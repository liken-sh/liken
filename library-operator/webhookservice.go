package main

// The scanner accepts a webhook from Radarr, Sonarr, and Jellyfin, and
// rescans the one title an import names. This is how that endpoint is
// reached: one Service per Library, over that Library's scanner pod, so the
// address holds when the pod is replaced.
//
// It is the same Service type the catalog stands in service.go, and it
// states a selector where that one states none. The two labels the scanner
// pod carries select exactly one pod, so the API server keeps the endpoints
// and this operator writes no EndpointSlice.

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
)

// The port the scanner's webhook listens on, and the name the Service port
// and the container port in pod.go both carry. scan.go builds the address it
// listens on from this same number.
const (
	webhookPortName     = "webhook"
	webhookPortProtocol = "TCP"
	webhookPort         = 8090
)

// The scheme and the DNS suffix the reported address is built from. A name
// of this form resolves from any pod in the cluster, which is where the
// media servers that send these webhooks run.
const (
	webhookScheme    = "http://"
	clusterDNSSuffix = ".svc"
)

// buildWebhookService is a function of the Library alone, the way
// buildScannerPod is, so two passes build the same object.
//
// It publishes no not-ready address. A scanner that is still starting cannot
// answer a webhook, and a hook it drops is a hook the sender does not repeat,
// so the address holds no endpoint until the pod is ready. This is the
// opposite of the catalog Service, where a starting agent is still a peer
// worth gossiping to.
func buildWebhookService(library *Library) *Service {
	return &Service{
		APIVersion: serviceAPIVersion,
		Kind:       "Service",
		Metadata: ObjectMeta{
			Name:            scannerPodName(library.Metadata.Name),
			Namespace:       library.Metadata.Namespace,
			Labels:          scannerLabels(library.Metadata.Name),
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: ServiceSpec{
			PublishNotReadyAddresses: false,
			Selector:                 scannerLabels(library.Metadata.Name),
			Ports: []ServicePort{
				{Name: webhookPortName, Protocol: webhookPortProtocol, Port: webhookPort},
			},
		},
	}
}

// webhookURL is the address a person gives to Radarr, Sonarr, or Jellyfin.
// It names the Service and its namespace, and never a pod, so it is the same
// address after every replacement of the pod.
func webhookURL(library *Library) string {
	return webhookScheme + scannerPodName(library.Metadata.Name) + "." +
		library.Metadata.Namespace + clusterDNSSuffix + ":" +
		strconv.Itoa(webhookPort) + "/"
}

// standWebhookService brings the live Service into line with the one this
// pass built, and writes only where the two differ, the rule
// standCatalogService follows. A conflict on the create means another writer
// created it first, which is the result this pass wanted.
//
// The update writes the live object with this operator's own fields set on
// it, and never the object the pass built. clusterIP and clusterIPs are the
// API server's, and both are immutable, so a write that omits the address it
// assigned is refused.
func (o *operator) standWebhookService(ctx context.Context, library *Library) error {
	desired := buildWebhookService(library)
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

	if sameWebhookService(live, desired) {
		return nil
	}
	live.Metadata.Labels = desired.Metadata.Labels
	live.Metadata.OwnerReferences = desired.Metadata.OwnerReferences
	live.Spec.PublishNotReadyAddresses = desired.Spec.PublishNotReadyAddresses
	live.Spec.Selector = desired.Spec.Selector
	live.Spec.Ports = desired.Spec.Ports
	_, err = UpdateService(ctx, o.client, live)
	return err
}

// sameWebhookService compares only the fields this operator states.
// Everything else on a live Service belongs to the API server, and a
// comparison of those would rewrite the object on every pass.
//
// It is a second function beside sameService, because the two Services state
// different fields. This one states a selector and takes whatever address
// the API server assigns. The catalog Service states no selector and asks to
// be headless, so sameService compares the address and this one must not.
func sameWebhookService(live, desired *Service) bool {
	if !maps.Equal(live.Metadata.Labels, desired.Metadata.Labels) {
		return false
	}
	if !slices.Equal(live.Metadata.OwnerReferences, desired.Metadata.OwnerReferences) {
		return false
	}
	if live.Spec.PublishNotReadyAddresses != desired.Spec.PublishNotReadyAddresses {
		return false
	}
	if !maps.Equal(live.Spec.Selector, desired.Spec.Selector) {
		return false
	}
	return slices.Equal(live.Spec.Ports, desired.Spec.Ports)
}
