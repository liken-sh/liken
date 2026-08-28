package main

// The control device as an event source.
//
// A card reports every change to one of its elements on the same
// descriptor its controls are read through, once a reader subscribes.
// The kernel queues a record for every write from any process, for a
// jack that changes state, for an ELD the graphics driver rewrote,
// and for a knob turned on a USB device, whose mixer keeps an
// interrupt endpoint open for exactly that. So a change a person made
// at the hardware reaches the operator the moment it happens, and the
// operator asks the card nothing on a timer. status.observed follows
// these events, and a declared value is written back on the pass one
// of them wakes.

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The size of one struct snd_ctl_event on a 64-bit kernel, and the
// offset of the element identifier inside it. The record is an int
// type, an unsigned mask, and the 64-byte identifier, and the kernel
// writes whole records only.
const (
	ctlEventSize     = 72
	ctlEventIDOffset = 8
)

// The request that turns a control descriptor into an event source.
// Its argument is one int: 1 subscribes, 0 unsubscribes and drops
// what was queued.
var ctlIoctlSubscribeEvents = iowr(0x16, 4)

// The mask bits a record carries. Value says the element's value
// moved, info says its description changed, such as an ELD whose
// count went to zero, add says the card created it, and TLV says its
// dB range changed. A removed element sets every bit, so that a
// reader that checks any one bit still notices it went away.
const (
	ctlEventMaskValue  = 1 << 0
	ctlEventMaskInfo   = 1 << 1
	ctlEventMaskAdd    = 1 << 2
	ctlEventMaskTLV    = 1 << 3
	ctlEventMaskRemove = ^uint32(0)
)

// ctlEvent mirrors struct snd_ctl_event. Nothing reads through it: the
// reader parses the record by offset, the way jacks.go parses an input
// event. It is here so a test can assert the layout the offsets
// assume.
type ctlEvent struct {
	Type int32
	Mask uint32
	ID   ctlElemID
}

// controlEvent is one change one card reported on one element.
//
// The whole identity comes along, not the numid alone, because the
// reconciler matches a change to an endpoint by the name and the
// index, which is what the attach rule sorts on.
type controlEvent struct {
	Card      int
	NumID     uint32
	Interface int32
	Device    uint32
	Index     uint32
	Name      string
	Mask      uint32
}

// valueChanged reports that the element's value moved. The removed
// test comes first because a removed element sets every bit, and the
// value bit alone would read it as a change.
func (e controlEvent) valueChanged() bool {
	return !e.removed() && e.Mask&ctlEventMaskValue != 0
}

// removed reports that the card took the element away.
func (e controlEvent) removed() bool { return e.Mask == ctlEventMaskRemove }

// String names the change for a log line.
func (e controlEvent) String() string {
	return fmt.Sprintf("card %d: %s (numid %d, index %d) changed, mask %#x",
		e.Card, e.Name, e.NumID, e.Index, e.Mask)
}

// watchControls subscribes to one card's control events and carries
// them onto a channel. The channel closes when the context ends.
func watchControls(ctx context.Context, card int) (<-chan controlEvent, error) {
	path := fmt.Sprintf("%s/controlC%d", sndDir, card)
	device, err := subscribeControlEvents(path)
	if err != nil {
		return nil, fmt.Errorf("subscribing to the control events of card %d: %w", card, err)
	}
	return readControlEvents(ctx, card, device), nil
}

// subscribeControlEvents opens a card's control device as an event
// source.
//
// The ioctl runs on the raw descriptor and not through the open
// file, because asking an os.File for its descriptor puts the file
// back into blocking mode and takes it out of the runtime's poller. A
// read on it would then hold a thread that a Close cannot interrupt.
// The descriptor is non-blocking from the open, so the runtime polls
// it and a Close ends a waiting read, the way jacks.go opens an input
// node.
func subscribeControlEvents(path string) (*os.File, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	subscribe := int32(1)
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(descriptor),
		ctlIoctlSubscribeEvents, uintptr(unsafe.Pointer(&subscribe)))
	if errno != 0 {
		unix.Close(descriptor)
		return nil, errno
	}
	return os.NewFile(uintptr(descriptor), path), nil
}

// readControlEvents carries whole records off one open control device.
//
// The close runs in a goroutine of its own because the read waits in
// the poller, and closing the descriptor is what ends it when the
// context does.
func readControlEvents(ctx context.Context, card int, device *os.File) <-chan controlEvent {
	events := make(chan controlEvent, 16)
	go func() {
		<-ctx.Done()
		_ = device.Close()
	}()
	go func() {
		defer close(events)
		record := make([]byte, ctlEventSize)
		for {
			read, err := device.Read(record)
			if err != nil {
				if ctx.Err() == nil {
					fmt.Fprintf(os.Stderr, "reading the control events of card %d: %v\n", card, err)
				}
				return
			}
			if read < ctlEventSize {
				// The kernel writes whole records, so a short read is
				// a truncated one and carries no element.
				continue
			}
			select {
			case events <- parseControlEvent(card, record):
			case <-ctx.Done():
				return
			}
		}
	}()
	return events
}

// cardWatchers keeps one event reader per card the inventory holds,
// and carries what they all report onto one channel.
//
// The set follows the inventory because a USB card that a person
// plugs in appears between two passes, and its own control device is
// the only thing that reports the knob on its front. A card that
// leaves takes its reader with it: a reader of a device node that is
// gone reports one error and stops, and the next pass that lists the
// card again opens it again.
type cardWatchers struct {
	ctx    context.Context
	events chan controlEvent

	mutex    sync.Mutex
	watching map[int]context.CancelFunc
	refused  map[int]bool
}

func watchCards(ctx context.Context) *cardWatchers {
	return &cardWatchers{
		ctx:      ctx,
		events:   make(chan controlEvent, 16),
		watching: map[int]context.CancelFunc{},
		refused:  map[int]bool{},
	}
}

// Events is where every card's changes arrive.
func (w *cardWatchers) Events() <-chan controlEvent {
	if w == nil {
		return nil
	}
	return w.events
}

// follow starts a reader for each card that has none and stops the
// readers of the cards that are gone. A nil set of watchers takes the
// call and does nothing, so a caller that has none needs no branch.
func (w *cardWatchers) follow(cards []int) {
	if w == nil {
		return
	}
	w.mutex.Lock()
	defer w.mutex.Unlock()
	wanted := make(map[int]bool, len(cards))
	for _, card := range cards {
		wanted[card] = true
	}
	for card, stop := range w.watching {
		if !wanted[card] {
			stop()
			delete(w.watching, card)
		}
	}
	for _, card := range cards {
		if _, watching := w.watching[card]; watching {
			continue
		}
		w.start(card)
	}
}

// start opens one card's control device as an event source. A card
// that refuses reports once, and the next pass tries it again. While
// it refuses, a knob turned on it reaches the resource on the
// backstop tick alone.
func (w *cardWatchers) start(card int) {
	ctx, stop := context.WithCancel(w.ctx)
	events, err := watchControls(ctx, card)
	if err != nil {
		stop()
		if !w.refused[card] {
			w.refused[card] = true
			fmt.Fprintf(os.Stderr, "%v\n", err)
		}
		return
	}
	delete(w.refused, card)
	w.watching[card] = stop
	go w.relay(card, events)
}

// relay carries one card's events onto the shared channel, and forgets
// the card when its reader ends, so that a later pass opens it again.
func (w *cardWatchers) relay(card int, events <-chan controlEvent) {
	defer func() {
		w.mutex.Lock()
		defer w.mutex.Unlock()
		if stop, watching := w.watching[card]; watching {
			stop()
			delete(w.watching, card)
		}
	}()
	for event := range events {
		select {
		case w.events <- event:
		case <-w.ctx.Done():
			return
		}
	}
}

// parseControlEvent reads one record: a type that is always the
// element type, a mask, and the element identifier, which holds the
// same five fields the element list carries.
func parseControlEvent(card int, record []byte) controlEvent {
	id := record[ctlEventIDOffset : ctlEventIDOffset+ctlElemIDSize]
	return controlEvent{
		Card:      card,
		Mask:      binary.NativeEndian.Uint32(record[4:8]),
		NumID:     binary.NativeEndian.Uint32(id[0:4]),
		Interface: int32(binary.NativeEndian.Uint32(id[4:8])),
		Device:    binary.NativeEndian.Uint32(id[8:12]),
		Name:      nullTerminated(id[16:60]),
		Index:     binary.NativeEndian.Uint32(id[60:64]),
	}
}
