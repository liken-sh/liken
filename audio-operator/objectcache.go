package main

// The pass reads this machine's Sinks and Sources from the two watches'
// stores, not from the API server. Each store holds every resource
// whose status.node is this machine, the same selection the pass lists,
// so a settled pass sends the API server no read at all. The shared
// informer package holds the rules of that read: a resource a store
// does not hold, and a store's copy older than this operator's own last
// write, are read from the API server, and a list comes from a store
// only after the store holds the whole first read.
//
// A resource the store does not hold is one no machine has written
// status.node on yet, one whose status.node names another machine,
// such as a Bluetooth speaker that moved here, or any resource while
// the watch has not finished its first read.
//
// A copy older than this operator's own write matters here because the
// pass writes status only where it differs from the copy it read. A
// pass that read such a copy would skip a write the resource needs: a
// claim released after the write that recorded it matches the older
// copy, and the API server would keep the claim. The memo of each kind
// records the version of the newest copy this operator wrote or read.

import "github.com/liken-sh/liken/kubernetes/informer"

// The three getters are the metadata the shared cache reads from each
// of this operator's kinds. Both kinds are cluster-scoped, so an
// endpoint has no namespace.
func (m *EndpointMeta) GetName() string            { return m.Name }
func (m *EndpointMeta) GetNamespace() string       { return "" }
func (m *EndpointMeta) GetResourceVersion() string { return m.ResourceVersion }

func (s *Sink) GetObjectMeta() informer.Meta   { return &s.Metadata }
func (s *Source) GetObjectMeta() informer.Meta { return &s.Metadata }

// objectCache is the stores the pass reads, and the memos of the copies
// it wrote or read. The zero value holds nothing, and every read goes to
// the API server.
type objectCache struct {
	sinks, sources informer.Held
}
