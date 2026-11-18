package main

// The operator must act when someone edits an Adapter or a Peripheral
// with kubectl, so it watches both kinds. The watches wake the main
// loop for a spec change, a deletion request, a new object, and a
// removed object. When someone force-deletes a Peripheral before its
// unpair finishes, the bond stays in bluetoothd, and the next pass
// adopts it again.
//
// The operator writes status on these objects often, such as a
// Peripheral's battery level. Those writes must not wake the loop, or
// each pass would cause another. A status write does not change
// metadata.generation, so the handlers compare only the generation,
// the UID, and the deletion timestamp.
//
// Each watch also wakes the loop when its first list completes. At
// startup a pass can read an object before the watch lists it, and an
// edit between the two reads shows up in the list but in no event.
//
// The Peripheral watch selects the Peripherals of this pod's radio by
// the adapter label. Each pod writes the status of its own
// Peripherals, so a watch with no selector would receive the battery
// writes of every other node.
//
// The Adapter watch covers the whole cluster, because the pass also
// releases a deleting Adapter whose radio left this node, and the
// label of that Adapter names another radio. The operator writes an
// Adapter's status only when it changes, so this watch receives few
// events. The handlers ignore the Adapter of another node.

import (
	"context"
	"sync"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// editWatch runs the Adapter and Peripheral watches and sends on one
// channel to wake the main loop.
type editWatch struct {
	wake     chan struct{}
	nodeName string

	// adapterKey is the name of the Adapter for this pod's radio, which
	// is also the value of the adapter label on each of its
	// Peripherals. It is empty until a pass reads the radio.
	mu         sync.Mutex
	adapterKey string

	// follow sends on adapterKeyChanged when adapterKey changes. The
	// buffer holds one send, and a second send before the Peripheral
	// watch reads it is dropped. Nothing is lost, because the watch
	// then reads the latest adapterKey.
	adapterKeyChanged chan struct{}

	// adapters and peripherals are the stores of the two watches. The
	// pass reads them instead of calling the API server (see
	// objectcache.go).
	adapters    informer.View
	peripherals followedView
}

// watchEdits starts the Adapter watch immediately and the Peripheral
// watch at the first call to follow. The wake channel closes after the
// context ends and both watches have stopped.
func watchEdits(ctx context.Context, client dynamic.Interface, nodeName string) *editWatch {
	w := &editWatch{
		wake:              make(chan struct{}, 1),
		nodeName:          nodeName,
		adapterKeyChanged: make(chan struct{}, 1),
	}
	handler := editHandler[Adapter]{what: "the Adapters", wake: w.signal, mark: w.adapterMark}
	adapters := informer.Start(ctx, client, informer.Source{Resource: adapterResource}, informer.Options{Handler: handler.handler(), Synced: w.signal})
	w.adapters = adapters.View()
	var group sync.WaitGroup
	group.Go(func() { <-adapters.Done() })
	group.Go(func() { w.followPeripherals(ctx, client) })
	go func() {
		group.Wait()
		close(w.wake)
	}()
	return w
}

func (w *editWatch) wakes() <-chan struct{} { return w.wake }

// cache returns the stores the pass reads: the Adapter and Peripheral
// stores of this watch, and the PairingRequest store, which another
// watch fills.
func (w *editWatch) cache(requests informer.View) objectCache {
	return objectCache{
		adapters:           informer.Held{View: w.adapters, Versions: memo.New()},
		requests:           informer.Held{View: requests, Versions: memo.New()},
		peripherals:        &w.peripherals,
		peripheralVersions: memo.New(),
	}
}

// follow records the Adapter name of this pod's radio. The pass calls
// it on every pass that reads the radio, and the Peripheral watch
// restarts only when the name differs from the last call.
func (w *editWatch) follow(adapterKey string) {
	w.mu.Lock()
	changed := w.adapterKey != adapterKey
	w.adapterKey = adapterKey
	w.mu.Unlock()
	if !changed {
		return
	}
	select {
	case w.adapterKeyChanged <- struct{}{}:
	default:
	}
}

func (w *editWatch) followed() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.adapterKey
}

func (w *editWatch) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// followPeripherals runs one Peripheral informer for this pod's radio.
// An informer's label selector cannot change, so when the radio's
// address changes, this cancels the old informer, waits for it to
// stop, and then starts one with the new selector, so that the
// Peripherals of the old radio no longer wake the loop.
func (w *editWatch) followPeripherals(ctx context.Context, client dynamic.Interface) {
	watching := ""
	stop := func() {}
	for {
		select {
		case <-ctx.Done():
			stop()
			return
		case <-w.adapterKeyChanged:
		}
		key := w.followed()
		if key == watching {
			continue
		}
		// Clear the store first, so that until the new informer starts,
		// the pass reads no Peripherals instead of the old radio's.
		w.peripherals.set("", informer.View{})
		stop()
		watching = key
		watchCtx, cancel := context.WithCancel(ctx)
		handler := editHandler[Peripheral]{what: "the Peripherals of " + key, wake: w.signal, mark: peripheralMark}
		source := informer.Source{Resource: peripheralResource, LabelSelector: adapterSelector(key)}
		peripherals := informer.Start(watchCtx, client, source, informer.Options{Handler: handler.handler(), Synced: w.signal})
		stop = func() {
			cancel()
			<-peripherals.Done()
		}
		w.peripherals.set(key, peripherals.View())
	}
}

// adapterMark returns the editMark of an Adapter, and whether this pod
// acts on it. That is true when the Adapter is this pod's radio, when
// its status.node is this node, or when no node has written its
// status yet.
func (w *editWatch) adapterMark(adapter Adapter) (editMark, bool) {
	node := adapter.Status.Node
	ours := adapter.Metadata.Name == w.followed() || node == w.nodeName || node == ""
	return markOf(adapter.Metadata), ours
}

// peripheralMark returns the editMark of a Peripheral. The selector
// admits only this radio's Peripherals, so this pod acts on all of
// them.
func peripheralMark(peripheral Peripheral) (editMark, bool) {
	return markOf(peripheral.Metadata), true
}

// editMark holds the fields of an object that change on an edit. The
// deleting flag makes a deletion request wake the loop even if the API
// server does not increment the generation when it sets
// deletionTimestamp. The UID is there because a watch that reconnects
// after a gap can report an object that was deleted and created again
// with the same name as an update, at the same generation.
type editMark struct {
	uid        string
	generation int64
	deleting   bool
}

func markOf(meta ObjectMeta) editMark {
	return editMark{uid: meta.UID, generation: meta.Generation, deleting: meta.deleting()}
}

// editHandler wakes the loop for the events of one watch that are
// edits. The informer passes an update both the cached copy and the new
// copy, so the handler keeps no state. After a gap in the watch, the
// informer lists the collection again and reports each difference from
// its cache as an add, an update, or a delete, so an edit made during
// the gap reaches the handler too.
type editHandler[T any] struct {
	what string
	wake func()
	mark func(T) (mark editMark, ours bool)
}

func (h editHandler[T]) handler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    h.added,
		UpdateFunc: h.updated,
		DeleteFunc: h.removed,
	}
}

// added wakes the loop when the new object is one this pod acts on.
func (h editHandler[T]) added(object any) {
	item, err := informer.Convert[T](object)
	if err != nil {
		informer.Report(h.what, err)
		return
	}
	if _, ours := h.mark(item); ours {
		h.wake()
	}
}

// removed wakes the loop when the removed object is one this pod acts
// on. A tombstone can arrive with no copy of the object, and then the
// handler wakes the loop anyway, because an extra pass costs less than
// a missed unpair.
func (h editHandler[T]) removed(object any) {
	item, err := informer.Convert[T](object)
	if err != nil {
		informer.Report(h.what, err)
		h.wake()
		return
	}
	if _, ours := h.mark(item); ours {
		h.wake()
	}
}

// updated wakes the loop when the mark of an object this pod acts on
// differs between the cached copy and the new one.
func (h editHandler[T]) updated(before, after any) {
	item, err := informer.Convert[T](after)
	if err != nil {
		informer.Report(h.what, err)
		return
	}
	mark, ours := h.mark(item)
	// A cached copy that fails to convert was logged when it arrived.
	// Its mark is unknown, so treat the update as an edit.
	old, err := informer.Convert[T](before)
	was, _ := h.mark(old)
	if ours && (err != nil || was != mark) {
		h.wake()
	}
}
