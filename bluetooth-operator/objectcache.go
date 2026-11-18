package main

// The inventory pass reads Adapters, Peripherals, and PairingRequests
// from the stores of the operator's watches, so a pass that changes
// nothing sends the API server no read for them.
//
// A store's copy can be older than the operator's last write, and a
// pass that acted on it would repeat a change, such as opening the
// pairing window again for a request it already paired. So each kind
// has a memo of the resourceVersion the operator last wrote or read, and
// the informer package reads the object from the API server when the
// store's copy has a different version. kubernetes/informer/cache.go
// gives the other cases that read from the API server.
//
// The Peripheral watch selects the Peripherals of one radio and starts
// only after a pass has read the radio. Until then the pass lists
// Peripherals from the API server.

import (
	"sync"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// The informer package reads an object's metadata through these methods.
func (m *ObjectMeta) GetName() string            { return m.Name }
func (m *ObjectMeta) GetNamespace() string       { return m.Namespace }
func (m *ObjectMeta) GetResourceVersion() string { return m.ResourceVersion }

func (a *Adapter) GetObjectMeta() informer.Meta        { return &a.Metadata }
func (p *Peripheral) GetObjectMeta() informer.Meta     { return &p.Metadata }
func (r *PairingRequest) GetObjectMeta() informer.Meta { return &r.Metadata }

// followedView holds the store of the Peripheral watch and the Adapter
// key that the watch selects on. The watch restarts with a new selector
// when the radio changes, so a reader passes the key it expects and gets
// the zero View while the watch is for another radio or has not started.
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

// of also returns the zero View on a nil followedView, which the zero
// objectCache has.
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

// objectCache is the stores that the inventory pass reads and the memos
// of the versions it wrote or read. The zero value has no stores, so
// every read goes to the API server.
type objectCache struct {
	adapters    informer.Held
	requests    informer.Held
	peripherals *followedView

	// peripheralVersions is the memo for the Peripherals. It is not
	// part of followedView, so it stays when the Peripheral watch moves
	// to another radio.
	peripheralVersions *memo.Versions
}

func (c objectCache) peripheralsOf(adapterKey string) informer.Held {
	return informer.Held{View: c.peripherals.of(adapterKey), Versions: c.peripheralVersions}
}
