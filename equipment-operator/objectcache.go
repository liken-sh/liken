package main

// A reconcile pass reads Receivers, CECBuses, Televisions, and Displays
// from the stores of the informers that watch them, so a pass that
// changes nothing sends the API server no list. Each store holds the
// whole collection, except the node workload's Display store, which
// holds only the Displays of its machine. The informer package determines
// when a store can be read, and reads from the API server when it
// cannot (kubernetes/informer/cache.go).
//
// A stored copy can be older than this operator's own last write,
// because the watch delivers the write some time after the API server
// accepts it. A pass that acted on that copy would repeat a change: a
// Receiver unit would send the receiver the same setting again, and a
// status compared with the old copy would be written again with a new
// lastTransitionTime. So each apply and create records the object's new
// resourceVersion in the Client's memo for its kind (objectVersions),
// and the informer package reads an object from the API server when the
// store's copy has a different version.
//
// Every write is a server-side apply, a create, or a delete. An apply
// sends no resourceVersion, so a stale copy never makes the API server
// reject it, and no write retries from a fresh copy. A 409 means a
// create whose name already exists, or an apply whose metadata.uid
// names an object that was deleted since.

import (
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// The informer package reads an object's name, namespace, and version
// through these methods. Every kind that uses ObjectMeta is
// cluster-scoped, so the namespace is empty.
func (m *ObjectMeta) GetName() string            { return m.Name }
func (m *ObjectMeta) GetNamespace() string       { return "" }
func (m *ObjectMeta) GetResourceVersion() string { return m.ResourceVersion }

func (r *Receiver) GetObjectMeta() informer.Meta   { return &r.Metadata }
func (b *CECBus) GetObjectMeta() informer.Meta     { return &b.Metadata }
func (t *Television) GetObjectMeta() informer.Meta { return &t.Metadata }
func (d *Display) GetObjectMeta() informer.Meta    { return &d.Metadata }

// objectVersions holds one memo for each kind this process writes.
type objectVersions struct {
	receivers, cecBuses, televisions *memo.Versions
}

func newObjectVersions() objectVersions {
	return objectVersions{receivers: memo.New(), cecBuses: memo.New(), televisions: memo.New()}
}
