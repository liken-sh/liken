package main

// The api role watches single objects by name with informer.WatchOne,
// such as a ConfigMap of CA certificates or the Secret with the serving
// certificate, so a rotated certificate is used from the next TLS
// handshake.

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	configMapResource = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	secretResource    = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
)

// awaitSynced waits until every channel is closed or ctx ends, and
// returns whether every channel closed. The api role waits for its
// certificates before it listens, and ctx bounds that wait, so an API
// server that does not respond does not keep the listener from starting.
func awaitSynced(ctx context.Context, channels ...<-chan struct{}) bool {
	for _, channel := range channels {
		select {
		case <-channel:
		case <-ctx.Done():
			return false
		}
	}
	return true
}
