package main

// This file holds what the api role adds to the watch of one named
// object (informer.WatchOne): the kinds it follows by name, such as a
// CA ConfigMap or the serving Secret, and the wait for their first
// reads. The api role hands every version of each object to its owner,
// so a rotated certificate reaches the next TLS handshake when the API
// server writes it.

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// The two kinds the api role follows by name.
var (
	configMapResource = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	secretResource    = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
)

// awaitSynced waits until every channel has closed or the wait runs
// out, and answers whether every one closed. The api role reads its
// certificates before it listens, and a read the API server does not
// answer in time must not hold the listener back for good.
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
