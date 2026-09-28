package main

// The pass reads the Adapters, the Peripherals of its radio, the
// PairingRequests, and the bond Secrets from the watches' stores, not
// from the API server. Each store holds the same selection that the
// pass would list, so a settled pass sends the API server no read of
// these four kinds. The shared informer package holds the rules of that
// read: an object a store does not hold, and a store's copy older than
// this operator's own last write, are read from the API server, and a
// list comes from a store only after the store holds the whole first
// read.
//
// The Peripheral watch follows the radio the pass reads, so on the
// first pass for a radio its store is not ready, and the pass lists the
// Peripherals from the API server.
//
// A copy older than this operator's own write matters here because a
// pass that acted on it would act again on a change it already made,
// such as opening the pairing window for a request it already paired.
// The memo of each kind records the version of the newest copy this
// operator wrote or read.

import (
	"sync"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// The three getters are the metadata the shared cache reads from each
// of this operator's kinds.
func (m *ObjectMeta) GetName() string            { return m.Name }
func (m *ObjectMeta) GetNamespace() string       { return m.Namespace }
func (m *ObjectMeta) GetResourceVersion() string { return m.ResourceVersion }

func (a *Adapter) GetObjectMeta() informer.Meta        { return &a.Metadata }
func (p *Peripheral) GetObjectMeta() informer.Meta     { return &p.Metadata }
func (r *PairingRequest) GetObjectMeta() informer.Meta { return &r.Metadata }

// followedView is the store of a watch whose selector follows one
// radio, and the radio the store is for. A reader asks for the store
// of the radio it holds, and gets nothing while the watch for that
// radio has not started.
type followedView struct {
	mu   sync.Mutex
	key  string
	view informer.View
}

func (f *followedView) set(key string, view informer.View) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key, f.view = key, view
}

// of answers the store for one radio, or the zero view when the watch
// holds another radio's objects.
func (f *followedView) of(key string) informer.View {
	if f == nil {
		return informer.View{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.key != key {
		return informer.View{}
	}
	return f.view
}

// objectCache is the stores the inventory pass reads, and the memos of
// the copies it wrote or read. The zero value holds nothing, and every
// read goes to the API server.
type objectCache struct {
	adapters    informer.Held
	requests    informer.Held
	peripherals *followedView

	// peripheralVersions is the memo for the Peripherals, which
	// outlives the watch of any one radio.
	peripheralVersions *memo.Versions
}

// peripheralsOf answers the Peripheral store for one radio, with its
// memo.
func (c objectCache) peripheralsOf(adapterKey string) informer.Held {
	return informer.Held{View: c.peripherals.of(adapterKey), Versions: c.peripheralVersions}
}
