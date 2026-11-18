package main

// The shared informer package runs each watch on client-go's reflector,
// and keeps the cache that a pass reads (objectcache.go). The operator
// watches Adapters and Peripherals (editwatch.go), PairingRequests
// (requestwatch.go), and the bond Secrets of its radio (bondstore.go).
// The shared apiclient package sends every write, and every read that
// the cache cannot serve.

import "k8s.io/apimachinery/pkg/runtime/schema"

// The collections this operator watches.
var (
	adapterResource        = schema.GroupVersionResource{Group: pairingGroup, Version: pairingVersion, Resource: "adapters"}
	peripheralResource     = schema.GroupVersionResource{Group: pairingGroup, Version: pairingVersion, Resource: "peripherals"}
	pairingRequestResource = schema.GroupVersionResource{Group: pairingGroup, Version: pairingVersion, Resource: "pairingrequests"}
	secretResource         = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
)
