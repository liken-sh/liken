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
func watchEdits(ctx context.Context, client *Client, nodeName string) *editWatch {
	w := &editWatch{
		wake:     make(chan struct{}, 1),
		nodeName: nodeName,
		moved:    make(chan struct{}, 1),
	}
	adapters := &editTracker[Adapter]{wake: w.signal, mark: w.adapterMark}
	var group sync.WaitGroup
	group.Go(func() {
		listThenWatch(ctx, client, adaptersPath(), "the Adapters", adapters.replace, adapters.apply)
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
// holds. When the radio's address changes, it stops the watch for the
// old address before it opens the watch for the new one, so the
// Peripherals of the old radio stop waking the loop.
func (w *editWatch) followPeripherals(ctx context.Context, client *Client) {
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
		peripherals := &editTracker[Peripheral]{wake: w.signal, mark: peripheralMark}
		go func() {
			defer close(done)
			listThenWatch(watchCtx, client, byAdapter(peripheralsPath(), key), "the Peripherals of "+key,
				peripherals.replace, peripherals.apply)
		}()
	}
}

// adapterMark reads the parts of an Adapter that an edit changes. An
// Adapter matters to this pod when it names this pod's radio, when its
// status names this node, and when no node has written its status yet.
func (w *editWatch) adapterMark(adapter Adapter) (string, editMark, bool) {
	node := adapter.Status.Node
	ours := adapter.Metadata.Name == w.followed() || node == w.nodeName || node == ""
	return adapter.Metadata.Name, markOf(adapter.Metadata), ours
}

// peripheralMark reads the parts of a Peripheral that an edit changes.
// Every Peripheral the watch delivers carries this radio's label, so
// every one matters.
func peripheralMark(peripheral Peripheral) (string, editMark, bool) {
	return peripheral.Metadata.Name, markOf(peripheral.Metadata), true
}

// editMark is what an edit changes on an object: the generation, which
// counts spec changes, and the deletion mark. The watcher compares the
// deletion mark as well, so a deletion request wakes the loop whether
// or not the API server raises the generation when it sets
// deletionTimestamp.
type editMark struct {
	generation int64
	deleting   bool
}

func markOf(meta ObjectMeta) editMark {
	return editMark{generation: meta.Generation, deleting: meta.deleting()}
}

// editTracker holds the last mark of each object that one watch
// delivered, and wakes the loop when an event changes a mark. The
// watch calls replace and apply from one goroutine, so the map needs no
// lock.
type editTracker[T any] struct {
	held map[string]editMark
	wake func()
	mark func(T) (name string, mark editMark, ours bool)
}

// replace takes the whole collection from a list, and wakes the loop.
// A list follows a start or a gap in the watch, and the watcher cannot
// tell which edits the gap held. At a start, the list can also be read
// after the pass read the same objects, so an edit between the two
// reads is in the list and in no event.
func (t *editTracker[T]) replace(items []T) {
	t.held = make(map[string]editMark, len(items))
	for _, item := range items {
		name, mark, _ := t.mark(item)
		t.held[name] = mark
	}
	t.wake()
}

// apply takes one event from the watch.
func (t *editTracker[T]) apply(event string, item T) {
	name, mark, ours := t.mark(item)
	before, known := t.held[name]
	if event == "DELETED" {
		delete(t.held, name)
	} else {
		t.held[name] = mark
	}
	if ours && (event == "DELETED" || !known || before != mark) {
		t.wake()
	}
}
