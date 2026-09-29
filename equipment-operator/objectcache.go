package main

// A pass reads the collections it acts on from the stores of the
// informers that watch them, not from the API server. Each store holds
// the whole collection, apart from the node workload's Display store,
// which holds the Displays of its machine. So a settled pass sends the
// API server no read of the Receivers, the CECBuses, the Televisions,
// or the Displays.
//
// A pass reads the API server instead when a store has nothing to give
// it: before the informer's first read is done, while the API server
// forbids the watch, and after the watch stopped. So a pass never acts
// on an empty store as if the collection were empty. A kind whose
// definition is missing is the one empty store a pass reads, because
// the API server serves no such collection (watch.go). A copy in a
// store that does not convert is read from the API server as well.
//
// A copy in a store can be older than this operator's own last write,
// because the watch delivers the write a moment after the API server
// answers it, and later still while the watch is down. A pass that
// acted on such a copy would act again on a change it already made: a
// Receiver unit that took the settled state from the copy would send a
// receiver a setting again, and a status compared with the copy would
// be written again with a new lastTransitionTime. So the memo of each
// kind records the version of the newest copy this operator wrote or
// read, and the shared informer package reads an object from the API
// server when the store's copy has another version. A list also reads
// each object the operator created that the store does not hold yet,
// so a pass does not create it again, and leaves out each object the
// operator deleted that the store still holds.
//
// Every write here is a server-side apply, a create, or a delete. An
// apply states no resourceVersion, so a stale copy never makes the API
// server refuse it, and this operator needs no write that retries from
// a fresh copy. A 409 here means a create whose name exists, or an
// apply whose uid precondition names an object deleted since. The
// memos are the Client's (objectVersions), because every write goes
// through the Client.

import (
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// The three getters are the metadata the shared cache reads from each
// kind this operator reads from a store. Every such kind is
// cluster-scoped, so an object has no namespace.
func (m *ObjectMeta) GetName() string            { return m.Name }
func (m *ObjectMeta) GetNamespace() string       { return "" }
func (m *ObjectMeta) GetResourceVersion() string { return m.ResourceVersion }

func (r *Receiver) GetObjectMeta() informer.Meta   { return &r.Metadata }
func (b *CECBus) GetObjectMeta() informer.Meta     { return &b.Metadata }
func (t *Television) GetObjectMeta() informer.Meta { return &t.Metadata }
func (d *Display) GetObjectMeta() informer.Meta    { return &d.Metadata }

// objectVersions is the memo of each kind this process writes.
type objectVersions struct {
	receivers, cecBuses, televisions *memo.Versions
}

func newObjectVersions() objectVersions {
	return objectVersions{receivers: memo.New(), cecBuses: memo.New(), televisions: memo.New()}
}
