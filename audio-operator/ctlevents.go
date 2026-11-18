package main

// The kernel queues an event on a sound card's control device,
// /dev/snd/controlC<N>, for each change to one of the card's control
// elements, and a process that subscribes reads them. A change can be a
// write from any process, a jack that changes state, an ELD that the
// graphics driver rewrote, or a knob turned on a USB device, which the
// device reports on an interrupt endpoint. So the operator reads a change
// made at the hardware when it happens, and reads nothing from the card
// on a timer. The pass that an event starts updates status.observed and
// writes a declared value back.
//
// The operator does not watch the input node of a jack. The HDA and USB
// audio drivers create a control element for each jack they sense, and
// the pass reads the jack's state from that element. An ASoC machine
// driver can create a jack with no pins, which has an input node and no
// element. The pass cannot read the state of such a jack, so an event on
// its input node would start a pass that finds nothing new.

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// struct snd_ctl_event is 72 bytes on a 64-bit kernel: an int type, an
// unsigned mask, and the 64-byte element ID at offset 8. The kernel
// writes whole records only.
const (
	ctlEventSize     = 72
	ctlEventIDOffset = 8
)

// ctlIoctlSubscribeEvents is SNDRV_CTL_IOCTL_SUBSCRIBE_EVENTS. Its
// argument is an int: 1 subscribes, and 0 unsubscribes and drops the
// queued events.
var ctlIoctlSubscribeEvents = iowr(0x16, 4)

// The bits of a record's mask. Value means the element's value changed.
// Info means its description changed, for example an ELD whose count
// went to zero. Add means the card created the element, and TLV means
// its dB range changed. A removed element sets every bit, so a reader
// that tests any single bit still sees the removal.
const (
	ctlEventMaskValue  = 1 << 0
	ctlEventMaskInfo   = 1 << 1
	ctlEventMaskAdd    = 1 << 2
	ctlEventMaskTLV    = 1 << 3
	ctlEventMaskRemove = ^uint32(0)
)

// ctlEvent mirrors struct snd_ctl_event so that a test can check the
// layout that parseControlEvent's offsets assume. The reader itself
// parses by offset and does not use this type.
type ctlEvent struct {
	Type int32
	Mask uint32
	ID   ctlElemID
}

// controlEvent is one change that a card reported on one element. It
// keeps the whole element ID and not only the numid, because
// countELDChange finds an ELD change by the element's Name and its
// Device, the PCM number.
type controlEvent struct {
	Card      int
	NumID     uint32
	Interface int32
	Device    uint32
	Index     uint32
	Name      string
	Mask      uint32
}

// valueChanged reports that the element's value changed. A removed
// element has the value bit set along with all the others, so the
// method excludes it.
func (e controlEvent) valueChanged() bool {
	return !e.removed() && e.Mask&ctlEventMaskValue != 0
}

// removed reports that the card deleted the element.
func (e controlEvent) removed() bool { return e.Mask == ctlEventMaskRemove }

func (e controlEvent) String() string {
	return fmt.Sprintf("card %d: %s (numid %d, index %d) changed, mask %#x",
		e.Card, e.Name, e.NumID, e.Index, e.Mask)
}

// watchControls subscribes to one card's control events and returns
// them on a channel, which closes when the context ends.
func watchControls(ctx context.Context, card int) (<-chan controlEvent, error) {
	path := fmt.Sprintf("%s/controlC%d", sndDir, card)
	device, err := subscribeControlEvents(path)
	if err != nil {
		return nil, fmt.Errorf("subscribing to the control events of card %d: %w", card, err)
	}
	return readControlEvents(ctx, card, device), nil
}

// subscribeControlEvents opens a card's control device and subscribes
// to its events.
//
// The ioctl runs on the raw descriptor, before the os.File exists,
// because File.Fd switches the descriptor to blocking mode and removes
// it from the runtime's poller. A read on it would then block a thread
// that Close cannot interrupt. Opened with O_NONBLOCK, the descriptor
// stays in the poller, so Close ends a read that is waiting.
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

// readControlEvents reads records from an open control device and sends
// them on the returned channel until the context ends. A separate
// goroutine closes the device when the context ends, because that is
// what wakes a read that is waiting in the poller.
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
				// The kernel writes whole records, so a short read has
				// no complete element ID to parse.
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

// cardWatchers runs one control-event reader for each card whose control
// node the claim gives the pod, and merges their events onto one channel.
//
// Each pass starts the readers before it reads a card, so a change made
// during the read arrives as an event and is not lost. Each pass also
// starts a reader for a control node that appeared since the last pass,
// because a USB card can be plugged in between two passes, and only its
// own control device reports the knob on it. When a card is unplugged,
// its device node goes away, and its reader logs one error and stops. A
// reader that stops on its own calls wake, so the next pass starts a new
// reader before it reads the card.
type cardWatchers struct {
	ctx    context.Context
	events chan controlEvent
	wake   func()

	mutex    sync.Mutex
	watching map[int]*cardReader
	// subscribeFailed holds the cards whose last subscription failed, so
	// the error is logged once for each card and not on every pass.
	subscribeFailed map[int]bool
}

// cardReader is one card's running reader. relay compares pointers, so
// a relay that ends removes only its own reader and never a newer one
// that a later pass started for the same card number.
type cardReader struct{ stop context.CancelFunc }

func watchCards(ctx context.Context, wake func()) *cardWatchers {
	return &cardWatchers{
		ctx:             ctx,
		events:          make(chan controlEvent, 16),
		wake:            wake,
		watching:        map[int]*cardReader{},
		subscribeFailed: map[int]bool{},
	}
}

// controlCards lists the cards whose control nodes the claim gives the
// pod.
// It reads the device nodes in sndDir and not the result of reading the
// cards, because a pass subscribes to events before it reads any card.
func controlCards() []int {
	entries, err := os.ReadDir(sndDir)
	if err != nil {
		return nil
	}
	var cards []int
	for _, entry := range entries {
		match := controlNodePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		card, _ := strconv.Atoi(match[1])
		cards = append(cards, card)
	}
	slices.Sort(cards)
	return cards
}

var controlNodePattern = regexp.MustCompile(`^controlC(\d+)$`)

// Events returns the channel that carries the events of every card. It
// returns nil on a nil cardWatchers, and a receive from nil never
// proceeds.
func (w *cardWatchers) Events() <-chan controlEvent {
	if w == nil {
		return nil
	}
	return w.events
}

// follow starts a reader for each listed card that has none and stops
// the readers of cards that are no longer listed. It does nothing on a
// nil cardWatchers, so a test with no watchers needs no check.
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
	for card, reader := range w.watching {
		if !wanted[card] {
			reader.stop()
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

// start subscribes to one card's control events and starts a reader for
// them. When the subscription fails, start logs the error once for the
// card, and the next pass tries again. Until a subscription succeeds,
// a pass reads a knob turned on that card only on the backstop tick.
func (w *cardWatchers) start(card int) {
	ctx, stop := context.WithCancel(w.ctx)
	events, err := watchControls(ctx, card)
	if err != nil {
		stop()
		if !w.subscribeFailed[card] {
			w.subscribeFailed[card] = true
			fmt.Fprintf(os.Stderr, "%v\n", err)
		}
		return
	}
	delete(w.subscribeFailed, card)
	reader := &cardReader{stop: stop}
	w.watching[card] = reader
	go w.relay(card, reader, events)
}

// relay copies one card's events onto the shared channel. When the
// reader ends, relay removes the card from the watchers so that a later
// pass starts a new reader.
func (w *cardWatchers) relay(card int, reader *cardReader, events <-chan controlEvent) {
	defer func() {
		w.mutex.Lock()
		endedOnItsOwn := w.watching[card] == reader
		if endedOnItsOwn {
			reader.stop()
			delete(w.watching, card)
		}
		w.mutex.Unlock()
		if endedOnItsOwn && w.ctx.Err() == nil {
			w.wake()
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

// parseControlEvent decodes one record. It ignores the type field,
// because every event on a control device has the element type. The
// element ID has the same layout as an entry of the element list.
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
