package main

// A person edits an Adapter or a Peripheral with kubectl, and neither
// the kernel nor bluetoothd reports that edit. So the operator watches
// both kinds, and wakes the loop when an event carries an edit that
// the pass must act on:
//
//   - a change to the spec, such as an Adapter's spec.alias or a
//     Peripheral's spec.trusted,
//   - a deletion request, which starts the refusal of an Adapter's
//     deletion or the unpair of a Peripheral,
//   - a new object, and an object that the API server removed. A
//     Peripheral that somebody removed by force before its unpair
//     finished leaves a bond in bluetoothd, and the next pass adopts
//     that bond again.
//
// The operator writes status on these objects on many passes: a
// battery level, a Connected condition, the node that holds the radio.
// A status write changes neither the spec nor the deletion mark, so
// the watch of this operator's own write does not wake the loop.
// metadata.generation is the API server's count of spec changes, and
// the status subresource does not change it, so the watcher compares
// the generation and the deletion mark and nothing else.
//
// Each watch also wakes the loop once, when its first read of the
// collection is done. At a start, the pass can read the objects before
// the watch does, and an edit made between the two reads is in the
// watch's read and in no event.
//
// Each watch is scoped to the objects this pod acts on:
//
//   - The Peripherals are listed and watched with the adapter's label,
//     the same selector the pass lists them with. Every pod writes the
//     status of its own Peripherals, and a watch of every Peripheral
//     in the cluster would receive each other node's battery writes.
//     The pass reports the address of the radio it holds, and the
//     watch opens with that address, or opens again when it changes.
//   - The Adapters are watched in the whole cluster, because the pass
//     also releases a deleting Adapter whose radio left this node, and
//     that Adapter carries another radio's label. A cluster has one
//     Adapter for each radio, and an Adapter's status changes only when
//     its radio moves or its power changes, so the watch receives few
//     events. The watcher ignores an Adapter that another node holds.

import (
	"context"
	"sync"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

// editWatch holds the two watches and the channel that wakes the loop.
type editWatch struct {
	wake     chan struct{}
	nodeName string

	// adapterKey is the name of the Adapter for the radio this pod
	// holds, which is also the value of every Peripheral's adapter
	// label. It is empty until the first pass that reads the radio.
	mu         sync.Mutex
	adapterKey string

	// moved has one slot, and follow fills it when adapterKey changes.
	// The Peripheral watch reads adapterKey again when the slot is
	// full, so a second change before the read is not lost: the read
	// returns the latest key.
	moved chan struct{}
}

// watchEdits starts the Adapter watch at once and the Peripheral watch
// at the first call to follow. The wake channel closes when the context
// ends and both watches have stopped.
func watchEdits(ctx context.Context, client dynamic.Interface, nodeName string) *editWatch {
	w := &editWatch{
		wake:     make(chan struct{}, 1),
		nodeName: nodeName,
		moved:    make(chan struct{}, 1),
	}
	adapters := editHandler[Adapter]{what: "the Adapters", wake: w.signal, mark: w.adapterMark}
	var group sync.WaitGroup
	group.Go(func() {
		watchCollection(ctx, client, adapterResource, "", adapters.handler(), w.signal)
	})
	group.Go(func() { w.followPeripherals(ctx, client) })
	go func() {
		group.Wait()
		close(w.wake)
	}()
	return w
}

// wakes is the channel the loop reads.
func (w *editWatch) wakes() <-chan struct{} { return w.wake }

// follow names the radio this pod holds. The pass calls it on every
// pass that reads the radio, and only a change of address moves the
// Peripheral watch.
func (w *editWatch) follow(adapterKey string) {
	w.mu.Lock()
	changed := w.adapterKey != adapterKey
	w.adapterKey = adapterKey
	w.mu.Unlock()
	if !changed {
		return
	}
	select {
	case w.moved <- struct{}{}:
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

// followPeripherals runs one Peripheral watch for the radio this pod
// holds. A label selector is fixed for the life of an informer, so when
// the radio's address changes, this stops the informer for the old
// address and waits until it has returned, and then starts a new one
// for the new address. The Peripherals of the old radio stop waking
// the loop, and two informers never run at once.
func (w *editWatch) followPeripherals(ctx context.Context, client dynamic.Interface) {
	watching := ""
	stop := func() {}
	for {
		select {
		case <-ctx.Done():
			stop()
			return
		case <-w.moved:
		}
		key := w.followed()
		if key == watching {
			continue
		}
		stop()
		watching = key
		watchCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		stop = func() {
			cancel()
			<-done
		}
		peripherals := editHandler[Peripheral]{what: "the Peripherals of " + key, wake: w.signal, mark: peripheralMark}
		go func() {
			defer close(done)
			watchCollection(watchCtx, client, peripheralResource, adapterSelector(key), peripherals.handler(), w.signal)
		}()
	}
}

// adapterMark reads the parts of an Adapter that an edit changes. An
// Adapter matters to this pod when it names this pod's radio, when its
// status names this node, and when no node has written its status yet.
func (w *editWatch) adapterMark(adapter Adapter) (editMark, bool) {
	node := adapter.Status.Node
	ours := adapter.Metadata.Name == w.followed() || node == w.nodeName || node == ""
	return markOf(adapter.Metadata), ours
}

// peripheralMark reads the parts of a Peripheral that an edit changes.
// Every Peripheral the watch delivers carries this radio's label, so
// every one matters.
func peripheralMark(peripheral Peripheral) (editMark, bool) {
	return markOf(peripheral.Metadata), true
}

// editMark is what an edit changes on an object: the generation, which
// counts spec changes, and the deletion mark. The watcher compares the
// deletion mark as well, so a deletion request wakes the loop whether
// or not the API server raises the generation when it sets
// deletionTimestamp. It compares the UID too. After a gap in the
// watch, an object that somebody deleted and created again with the
// same name reaches the handler as an update, and the new object can
// have the same generation as the old one.
type editMark struct {
	uid        string
	generation int64
	deleting   bool
}

func markOf(meta ObjectMeta) editMark {
	return editMark{uid: meta.UID, generation: meta.Generation, deleting: meta.deleting()}
}

// editHandler wakes the loop when a change from one watch carries an
// edit. The informer hands an update both the copy it held and the new
// copy, so the handler compares their marks and keeps no copy of its
// own. After a gap in the watch, the informer reads the collection
// again and reports each difference from what it held as an addition,
// an update, or a deletion, so an edit made during the gap reaches the
// handler as well.
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

// added takes a new object, and wakes the loop when it is this pod's.
func (h editHandler[T]) added(object any) {
	item, err := convert[T](object)
	if err != nil {
		reportUnconverted(h.what, err)
		return
	}
	if _, ours := h.mark(item); ours {
		h.wake()
	}
}

// removed takes an object the API server removed, and wakes the loop
// when it is this pod's. A tombstone can hold no copy of the object,
// and then nothing says whose it was, so the removal wakes the loop.
// One extra pass costs less than a missed unpair.
func (h editHandler[T]) removed(object any) {
	item, err := convert[T](object)
	if err != nil {
		reportUnconverted(h.what, err)
		h.wake()
		return
	}
	if _, ours := h.mark(item); ours {
		h.wake()
	}
}

// updated takes a change to an object the informer held. Only a change
// to the mark wakes the loop, so this operator's own status write does
// not.
func (h editHandler[T]) updated(before, after any) {
	item, err := convert[T](after)
	if err != nil {
		reportUnconverted(h.what, err)
		return
	}
	mark, ours := h.mark(item)
	// A held copy that does not convert was logged when it arrived.
	// Nothing says what it held, so the change counts as an edit.
	old, err := convert[T](before)
	was, _ := h.mark(old)
	if ours && (err != nil || was != mark) {
		h.wake()
	}
}
