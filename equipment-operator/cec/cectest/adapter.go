package cectest

// One adapter on the bus, and the ioctls it answers.

import (
	"time"
	"unsafe"

	"github.com/liken-sh/equipment-operator/cec"
	"golang.org/x/sys/unix"
)

// Options describe one adapter. Physical is the address it has before
// anyone sets one: f.f.f.f for a USB adapter, or the address a video
// port's EDID gives. Capabilities default to what a Pulse-Eight
// reports. Monitor says whether the process may take a monitor mode,
// which the kernel allows only with CAP_NET_ADMIN.
type Options struct {
	Physical     cec.PhysicalAddress
	Capabilities cec.Capability
	Monitor      bool
}

// pulseEight is the capability set a Pulse-Eight adapter reports.
const pulseEight = cec.CapPhysAddr | cec.CapLogAddrs | cec.CapTransmit | cec.CapPassthrough | cec.CapRC | cec.CapMonitorAll

// Adapter is one adapter's kernel state. Its methods implement
// cec.Handle.
type Adapter struct {
	bus     *Bus
	options Options

	// Each field below is guarded by the bus mutex.
	physical cec.PhysicalAddress
	logical  []cec.LogicalAddress
	osdName  string
	// requested says that a claim is stored, and requestType is its
	// kind of logical address, which the kernel claims once the adapter
	// has a physical address.
	requested   bool
	requestType uint8
	// claims counts the claim requests the adapter received.
	claims int
	mode   uint32
	queue  []cec.Message
	events []cec.KernelEvent
	gone   bool
	notify chan struct{}
}

// Adapter puts a new adapter on the bus and opens it.
func (b *Bus) Adapter(options Options) (*Adapter, *cec.Device) {
	if options.Capabilities == 0 {
		options.Capabilities = pulseEight
	}
	adapter := &Adapter{
		bus:      b,
		options:  options,
		physical: options.Physical,
		notify:   make(chan struct{}, 1),
	}
	b.mutex.Lock()
	b.adapters = append(b.adapters, adapter)
	b.mutex.Unlock()
	return adapter, cec.New(adapter)
}

// Unplug makes every later call on the adapter fail with ENODEV, the
// way the kernel answers a handle whose adapter left.
func (a *Adapter) Unplug() {
	a.bus.mutex.Lock()
	a.gone = true
	a.bus.mutex.Unlock()
	a.wake()
}

// Disconnect drops the adapter's physical address and its logical
// addresses, the way the kernel drops them when an adapter on a video
// port loses its hotplug signal, and queues the state change. The
// kernel keeps the claim request, and Connect completes it again. A
// test uses this to show what a program does after the kernel took its
// addresses away.
func (a *Adapter) Disconnect() {
	a.bus.mutex.Lock()
	defer a.bus.mutex.Unlock()
	a.physical = cec.InvalidPhysicalAddress
	a.logical = nil
	a.stateChanged()
}

// Connect gives the adapter a physical address the way a video port's
// driver does when its hotplug signal returns with an EDID, and the
// kernel completes a stored claim.
func (a *Adapter) Connect(address cec.PhysicalAddress) {
	a.bus.mutex.Lock()
	defer a.bus.mutex.Unlock()
	a.physical = address
	a.complete()
	a.stateChanged()
}

// Logical answers the logical addresses the adapter holds.
func (a *Adapter) Logical() []cec.LogicalAddress {
	a.bus.mutex.Lock()
	defer a.bus.mutex.Unlock()
	return append([]cec.LogicalAddress(nil), a.logical...)
}

// Claims counts the claim requests a program made, so a test can tell
// an adapter that stayed joined from one that joined again.
func (a *Adapter) Claims() int {
	a.bus.mutex.Lock()
	defer a.bus.mutex.Unlock()
	return a.claims
}

// Follows answers whether a handle on the adapter is a follower or a
// monitor, which is what makes the kernel pass it messages.
func (a *Adapter) Follows() bool {
	a.bus.mutex.Lock()
	defer a.bus.mutex.Unlock()
	return a.follower() != 0
}

// Physical answers the address the adapter announces.
func (a *Adapter) Physical() cec.PhysicalAddress {
	a.bus.mutex.Lock()
	defer a.bus.mutex.Unlock()
	return a.physical
}

func (a *Adapter) wake() {
	select {
	case a.notify <- struct{}{}:
	default:
	}
}

// holds answers whether the adapter holds an address. The caller holds
// the bus mutex.
func (a *Adapter) holds(address cec.LogicalAddress) bool {
	for _, held := range a.logical {
		if held == address {
			return true
		}
	}
	return false
}

func (a *Adapter) follower() uint32  { return a.mode & 0xf0 }
func (a *Adapter) initiator() uint32 { return a.mode & 0x0f }

// hear takes one message off the wire, and answers the reply the
// kernel sends for it when the message is an identity request to the
// adapter's own address, which the kernel answers by itself. A
// monitor-all handle gets every message; a monitor or a follower gets
// the broadcasts and the messages to its own addresses, except the
// requests the kernel answered. The caller holds the bus mutex.
func (a *Adapter) hear(message cec.Message) (cec.Message, bool) {
	if a.gone {
		return cec.Message{}, false
	}
	mine := a.holds(message.To)
	reply, answered := cec.Message{}, false
	if mine {
		reply, answered = a.coreAnswer(message)
	}
	switch {
	case a.follower() == 0xf0:
	case answered:
		return reply, true
	case (a.follower() == 0xe0 || a.follower() == 0x10) && (mine || message.IsBroadcast()):
	default:
		return reply, answered
	}
	a.queue = append(a.queue, message)
	a.wake()
	return reply, answered
}

// coreAnswer is the answer the kernel's CEC core gives for its own
// adapter, and false for a message the core leaves to a follower. The
// core answers Give Device Vendor ID only for an adapter that states a
// vendor, and the adapters here state none. The caller holds the bus
// mutex.
func (a *Adapter) coreAnswer(message cec.Message) (cec.Message, bool) {
	opcode, _ := message.Opcode()
	own := message.To
	switch opcode {
	case cec.OpGivePhysicalAddress:
		return cec.ReportPhysicalAddress(own, a.physical, 4), true
	case cec.OpGiveOSDName:
		return cec.SetOSDName(own, message.From, a.osdName), true
	case cec.OpGetCECVersion:
		return cec.CECVersionReport(own, message.From, cec.Version14), true
	}
	return cec.Message{}, false
}

// Ioctl answers one request.
func (a *Adapter) Ioctl(request uintptr, argument unsafe.Pointer) error {
	a.bus.mutex.Lock()
	defer a.bus.mutex.Unlock()
	if a.gone {
		return unix.ENODEV
	}
	switch request {
	case cec.RequestGetCaps:
		caps := (*cec.KernelCaps)(argument)
		copy(caps.Driver[:], "cectest")
		copy(caps.Name[:], "cectest-adapter")
		caps.AvailableLogAddrs = 1
		caps.Capabilities = uint32(a.options.Capabilities)
	case cec.RequestGetPhysAddr:
		*(*uint16)(argument) = uint16(a.physical)
	case cec.RequestSetPhysAddr:
		return a.setPhysical(cec.PhysicalAddress(*(*uint16)(argument)))
	case cec.RequestSetMode:
		return a.setMode(*(*uint32)(argument))
	case cec.RequestSetLogAddrs:
		return a.claim((*cec.KernelLogAddrs)(argument))
	case cec.RequestGetLogAddrs:
		held := (*cec.KernelLogAddrs)(argument)
		*held = cec.KernelLogAddrs{NumLogAddrs: uint8(len(a.logical))}
		for index, address := range a.logical {
			held.LogAddr[index] = uint8(address)
		}
		copy(held.OSDName[:], a.osdName)
	case cec.RequestTransmit:
		return a.transmit((*cec.KernelMsg)(argument))
	case cec.RequestReceive:
		return a.receive((*cec.KernelMsg)(argument))
	case cec.RequestDequeueEvent:
		if len(a.events) == 0 {
			return unix.EAGAIN
		}
		*(*cec.KernelEvent)(argument) = a.events[0]
		a.events = a.events[1:]
	default:
		return unix.ENOTTY
	}
	return nil
}

// setPhysical sets the address, as a USB adapter allows and an adapter
// on a video port refuses. The caller holds the bus mutex.
func (a *Adapter) setPhysical(address cec.PhysicalAddress) error {
	if !a.options.Capabilities.Has(cec.CapPhysAddr) {
		return unix.ENOTTY
	}
	if a.initiator() == 0 {
		return unix.EBUSY
	}
	a.physical = address
	if address == cec.InvalidPhysicalAddress {
		a.logical = nil
	}
	a.complete()
	a.stateChanged()
	return nil
}

// setMode applies the kernel's rules for CEC_S_MODE. The caller holds
// the bus mutex.
func (a *Adapter) setMode(mode uint32) error {
	follower := mode & 0xf0
	monitor := follower == 0xe0 || follower == 0xf0
	switch {
	case monitor && !a.options.Monitor:
		return unix.EPERM
	case monitor && mode&0x0f != 0:
		return unix.EINVAL
	case follower == 0xf0 && !a.options.Capabilities.Has(cec.CapMonitorAll):
		return unix.EINVAL
	}
	a.mode = mode
	return nil
}

// claim applies CEC_ADAP_S_LOG_ADDRS: an empty request clears the
// addresses and forgets the stored request. Any other request is
// stored, and the adapter claims the first address of its type that
// nothing on the bus holds, at once when it has a physical address and
// otherwise when it gets one: 0 for a TV, 4, 8, or 11 for a playback
// device, and 5 for an audio system. The caller holds the bus mutex.
func (a *Adapter) claim(request *cec.KernelLogAddrs) error {
	if a.initiator() == 0 {
		return unix.EBUSY
	}
	if request.NumLogAddrs == 0 {
		a.logical = nil
		a.osdName = ""
		a.requested = false
		a.stateChanged()
		return nil
	}
	if len(a.logical) > 0 {
		return unix.EBUSY
	}
	a.osdName = string(trimNul(request.OSDName[:]))
	a.requested, a.requestType = true, request.LogAddrType[0]
	a.claims++
	if a.complete() {
		a.stateChanged()
	}
	return nil
}

// complete claims the stored request's address when the adapter has a
// physical address and holds none, and answers whether it claimed one.
// The caller holds the bus mutex.
func (a *Adapter) complete() bool {
	if !a.requested || a.physical == cec.InvalidPhysicalAddress || len(a.logical) > 0 {
		return false
	}
	candidates := map[uint8][]cec.LogicalAddress{0: {0}, 3: {4, 8, 11}, 4: {5}}[a.requestType]
	for _, address := range candidates {
		if !a.bus.occupied(address, a) {
			a.logical = []cec.LogicalAddress{address}
			return true
		}
	}
	return false
}

func trimNul(raw []byte) []byte {
	for index, value := range raw {
		if value == 0 {
			return raw[:index]
		}
	}
	return raw
}

// stateChanged queues the event the kernel sends when the adapter's
// addresses change. The caller holds the bus mutex.
func (a *Adapter) stateChanged() {
	var mask uint32
	for _, address := range a.logical {
		mask |= 1 << address
	}
	event := cec.KernelEvent{Event: cec.EventStateChange}
	event.Raw[0] = uint32(a.physical) | mask<<16
	a.events = append(a.events, event)
	a.wake()
}

// transmit puts a message on the wire and fills in what happened. The
// caller holds the bus mutex.
func (a *Adapter) transmit(raw *cec.KernelMsg) error {
	if a.initiator() == 0 {
		return unix.EBUSY
	}
	if len(a.logical) == 0 {
		return unix.EPERM
	}
	message := cec.Message{From: cec.LogicalAddress(raw.Msg[0] >> 4), To: cec.LogicalAddress(raw.Msg[0] & 0xf)}
	if raw.Len > 1 {
		message.Body = append([]byte(nil), raw.Msg[1:raw.Len]...)
	}
	a.bus.sent = append(a.bus.sent, message)
	if peer, held := a.bus.peers[message.To]; held && peer.Garbled && !message.IsBroadcast() {
		raw.TxStatus = cec.TxStatusArbLost | cec.TxStatusMaxRetries
		return nil
	}
	if !message.IsBroadcast() && !a.bus.occupied(message.To, a) {
		raw.TxStatus = cec.TxStatusNack | cec.TxStatusMaxRetries
		return nil
	}
	raw.TxStatus = cec.TxStatusOK
	if message.IsPoll() {
		return nil
	}
	replies := a.bus.carry(message, a)
	if peer, isPeer := a.bus.peers[message.To]; isPeer {
		reply, aborted, ok := peer.answer(message)
		if aborted {
			raw.RxStatus = cec.RxStatusFeatureAbort | cec.RxStatusOK
			return nil
		}
		if ok {
			replies = append(replies, reply)
		}
	}
	for _, reply := range replies {
		// The kernel hands the awaited reply to the transmit that asked
		// for it, and every other adapter on the wire hears it too.
		a.bus.carry(reply, a)
		if raw.Reply != 0 && reply.From == message.To && reply.Body[0] == raw.Reply {
			raw.RxStatus = cec.RxStatusOK
			raw.Msg = [16]byte{}
			raw.Msg[0] = byte(reply.From)<<4 | byte(reply.To)
			copy(raw.Msg[1:], reply.Body)
			raw.Len = uint32(len(reply.Body) + 1)
		}
	}
	if raw.Reply != 0 && raw.RxStatus == 0 {
		raw.RxStatus = cec.RxStatusTimeout
	}
	return nil
}

// receive dequeues the oldest message. The caller holds the bus mutex.
func (a *Adapter) receive(raw *cec.KernelMsg) error {
	if len(a.queue) == 0 {
		return unix.ETIMEDOUT
	}
	message := a.queue[0]
	a.queue = a.queue[1:]
	*raw = cec.KernelMsg{RxStatus: cec.RxStatusOK}
	raw.Msg[0] = byte(message.From)<<4 | byte(message.To)
	copy(raw.Msg[1:], message.Body)
	raw.Len = uint32(len(message.Body) + 1)
	return nil
}

// Wait blocks until a message or an event is queued, or the timeout
// ends.
func (a *Adapter) Wait(timeout time.Duration) (cec.Readiness, error) {
	deadline := time.After(timeout)
	for {
		a.bus.mutex.Lock()
		gone := a.gone
		ready := cec.Readiness{Message: len(a.queue) > 0, Event: len(a.events) > 0}
		a.bus.mutex.Unlock()
		if gone {
			return cec.Readiness{}, unix.ENODEV
		}
		if ready.Message || ready.Event {
			return ready, nil
		}
		select {
		case <-a.notify:
		case <-deadline:
			return cec.Readiness{}, nil
		}
	}
}

// Close has nothing to release.
func (a *Adapter) Close() error {
	return nil
}
