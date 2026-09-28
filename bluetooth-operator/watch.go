package main

// A watch keeps this operator's view of a collection current without a
// timer. The API server sends each change to the collection as it
// happens, and a watch with no change to send costs nothing.
//
// The shared informer package runs each watch on client-go's reflector,
// and keeps the copy of the collection that a pass reads
// (objectcache.go). This operator watches four collections: the
// Adapters and the Peripherals (editwatch.go), the PairingRequests
// (requestwatch.go), and the bond Secrets of its radio (bondstore.go).
// The shared apiclient package sends every write, and every read that a
// watch's copy does not answer.

import "k8s.io/apimachinery/pkg/runtime/schema"

// The four collections this operator watches.
var (
	adapterResource        = schema.GroupVersionResource{Group: pairingGroup, Version: pairingVersion, Resource: "adapters"}
	peripheralResource     = schema.GroupVersionResource{Group: pairingGroup, Version: pairingVersion, Resource: "peripherals"}
	pairingRequestResource = schema.GroupVersionResource{Group: pairingGroup, Version: pairingVersion, Resource: "pairingrequests"}
	secretResource         = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
)
