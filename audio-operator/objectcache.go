package main

// The pass reads this machine's Sinks and Sources from the stores of
// the Sink and Source watches, so a pass that changes nothing sends no
// GET. Each watch selects status.node=<this machine>, the selector the
// pass lists with. kubernetes/informer decides when a store is used. A
// list or a single resource comes from a store only after the watch's
// first list has finished. After that, a resource is still read from
// the API server when the store has no copy of it, or when the
// copy's resourceVersion differs from the one the operator last wrote
// or read.
//
// A store has no copy of a resource that has no status.node yet, or
// whose status.node names another machine, such as a Bluetooth speaker
// that moved to this machine.
//
// The version check matters because the pass writes a status only when
// it differs from the copy it read. Suppose the operator writes a claim
// into a Sink's status while the store still has the copy from before.
// When the claim is released, the status the pass composes equals that
// old copy, so the pass skips the write and the API server keeps the
// released claim. The memo of each kind records the version of the
// operator's last write or read, so the pass reads from the API server
// when the store's copy is at another version.

import "github.com/liken-sh/liken/kubernetes/informer"

// kubernetes/informer reads an object's metadata through these
// methods. Sinks and Sources are cluster-scoped, so the namespace is
// empty.
func (m *EndpointMeta) GetName() string            { return m.Name }
func (m *EndpointMeta) GetNamespace() string       { return "" }
func (m *EndpointMeta) GetResourceVersion() string { return m.ResourceVersion }

func (s *Sink) GetObjectMeta() informer.Meta   { return &s.Metadata }
func (s *Source) GetObjectMeta() informer.Meta { return &s.Metadata }

// The zero objectCache has no stores, so every read goes to the API
// server.
type objectCache struct {
	sinks, sources informer.Held
}
