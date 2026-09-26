// Package cectest is a CEC bus in memory, for tests. It answers the
// kernel's CEC ioctls the way the kernel's CEC core does for the calls
// this repository makes, so a test drives a real cec.Device through
// the same ioctl boundary a /dev/cecN node serves. The bus carries
// scripted peers, such as a TV and a receiver, and any number of
// adapters.
//
// It follows the kernel's documented rules, not every rule: the
// claim, the modes, the polls, the replies, the answers the kernel
// gives for its own adapter, and ENODEV after an unplug. The
// integration tests against the kernel's vivid driver check the same
// behavior on the real CEC core.
package cectest

import (
	"sync"

	"github.com/liken-sh/equipment-operator/cec"
)

// A Peer is one scripted device on the bus. It acknowledges polls to
// its logical address and answers the identity and power questions
// from its fields. Mute makes it acknowledge polls and answer nothing,
// the way a TV in deep standby behaves.
type Peer struct {
	Logical     cec.LogicalAddress
	Physical    cec.PhysicalAddress
	PrimaryType byte
	OSDName     string
	Vendor      cec.VendorID
	Version     cec.Version
	Power       cec.PowerStatus
	Mute        bool
	// Garbled makes every transmission to the peer fail with a lost
	// arbitration, the way a disturbed wire fails, so a test shows what
	// a program does with a failure that is not a NACK.
	Garbled bool
}

// Bus is one CEC wire.
type Bus struct {
	mutex    sync.Mutex
	peers    map[cec.LogicalAddress]Peer
	adapters []*Adapter
	sent     []cec.Message
}

// NewBus starts a bus with no device on it.
func NewBus() *Bus {
	return &Bus{peers: map[cec.LogicalAddress]Peer{}}
}

// Add puts a scripted peer on the bus, or replaces the one at its
// logical address.
func (b *Bus) Add(peer Peer) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.peers[peer.Logical] = peer
}

// Remove takes the peer at a logical address off the bus.
func (b *Bus) Remove(address cec.LogicalAddress) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	delete(b.peers, address)
}

// Send puts a message from a peer on the wire. Every adapter whose mode
// passes the message to it receives it.
func (b *Bus) Send(message cec.Message) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	for _, reply := range b.carry(message, nil) {
		b.carry(reply, nil)
	}
}

// Sent answers every message the adapters transmitted, in order.
func (b *Bus) Sent() []cec.Message {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return append([]cec.Message(nil), b.sent...)
}

// carry delivers one message to every adapter but its sender, and
// answers the replies the adapters' kernels sent to it. The caller
// holds the mutex.
func (b *Bus) carry(message cec.Message, sender *Adapter) []cec.Message {
	var replies []cec.Message
	for _, adapter := range b.adapters {
		if adapter == sender {
			continue
		}
		if reply, answered := adapter.hear(message); answered {
			replies = append(replies, reply)
		}
	}
	return replies
}

// occupied answers whether a peer or an adapter holds a logical
// address. The caller holds the mutex.
func (b *Bus) occupied(address cec.LogicalAddress, asker *Adapter) bool {
	if _, held := b.peers[address]; held {
		return true
	}
	for _, adapter := range b.adapters {
		if adapter != asker && adapter.holds(address) {
			return true
		}
	}
	return false
}

// answer is what a peer replies to one request, and false when it
// replies nothing or aborts. aborted says that it sends a Feature
// Abort.
func (p Peer) answer(request cec.Message) (reply cec.Message, aborted bool, ok bool) {
	if p.Mute {
		return cec.Message{}, false, false
	}
	opcode, _ := request.Opcode()
	switch opcode {
	case cec.OpGivePhysicalAddress:
		return cec.ReportPhysicalAddress(p.Logical, p.Physical, p.PrimaryType), false, true
	case cec.OpGiveOSDName:
		return cec.SetOSDName(p.Logical, request.From, p.OSDName), false, true
	case cec.OpGiveDeviceVendorID:
		return cec.DeviceVendorID(p.Logical, p.Vendor), false, true
	case cec.OpGetCECVersion:
		return cec.CECVersionReport(p.Logical, request.From, p.Version), false, true
	case cec.OpGiveDevicePowerStatus:
		return cec.ReportPowerStatus(p.Logical, request.From, p.Power), false, true
	}
	return cec.Message{}, true, false
}
