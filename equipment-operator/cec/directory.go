package cec

// The directory is what one adapter knows about the other devices on
// its bus. It learns from two sources: the messages the adapter hears,
// and the answers to the questions a scan asks. A logical address is
// the key, because it is the one address every message carries.

import (
	"slices"
	"sync"
)

// A Peer is one other device on the bus, as far as the adapter knows
// it. A fact the device has not stated yet holds its unknown value:
// InvalidPhysicalAddress, an empty name, VendorUnknown,
// VersionUnknown, or PowerUnknown.
type Peer struct {
	Logical  LogicalAddress
	Physical PhysicalAddress
	// Type is the primary device type the device reported, or the type
	// its logical address implies until it reports one.
	Type    DeviceType
	OSDName string
	Vendor  VendorID
	Version Version
	Power   PowerStatus
}

func newPeer(address LogicalAddress) *Peer {
	return &Peer{
		Logical:  address,
		Physical: InvalidPhysicalAddress,
		Type:     address.Type(),
		Vendor:   VendorUnknown,
		Version:  VersionUnknown,
		Power:    PowerUnknown,
	}
}

// Directory holds the peers of one adapter. It is safe for the
// receive loop and a scan to use at once.
type Directory struct {
	mutex sync.Mutex
	// own is the adapter's own logical address, which is never a peer.
	// A monitor holds none, so AddressUnregistered stands for it.
	own   LogicalAddress
	peers map[LogicalAddress]*Peer
}

// NewDirectory starts an empty directory.
func NewDirectory() *Directory {
	return &Directory{own: AddressUnregistered, peers: map[LogicalAddress]*Peer{}}
}

// SetOwn records the adapter's own logical address, and forgets any
// peer the directory held at that address.
func (d *Directory) SetOwn(address LogicalAddress) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.own = address
	delete(d.peers, address)
}

// Reset forgets every peer, which a change of mode or of bus needs,
// because what the adapter heard before may be another wire.
func (d *Directory) Reset() {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.peers = map[LogicalAddress]*Peer{}
}

// peer finds or adds the peer at an address. The caller holds the
// mutex.
func (d *Directory) peer(address LogicalAddress) *Peer {
	held, found := d.peers[address]
	if !found {
		held = newPeer(address)
		d.peers[address] = held
	}
	return held
}

// Observe folds one message into the directory and answers whether it
// changed anything. A device that sends a message is present, so its
// address becomes a peer. The unregistered address is no device, and
// the adapter's own messages say nothing about a peer.
func (d *Directory) Observe(message Message) bool {
	if message.From == AddressUnregistered {
		return false
	}
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if message.From == d.own {
		return false
	}
	_, known := d.peers[message.From]
	peer := d.peer(message.From)
	before := *peer
	learn(peer, message)
	return !known || *peer != before
}

// learn reads the one fact a message states about its sender.
func learn(peer *Peer, message Message) {
	opcode, _ := message.Opcode()
	operands := message.Operands()
	switch {
	case opcode == OpReportPhysicalAddr && len(operands) >= 3:
		peer.Physical = PhysicalAddress(operands[0])<<8 | PhysicalAddress(operands[1])
		if kind, found := primaryTypes[operands[2]]; found {
			peer.Type = kind
		}
	case opcode == OpActiveSource && len(operands) >= 2:
		// Active Source states the sender's own physical address.
		peer.Physical = PhysicalAddress(operands[0])<<8 | PhysicalAddress(operands[1])
	case opcode == OpSetOSDName && len(operands) > 0:
		peer.OSDName = string(operands)
	case opcode == OpDeviceVendorID && len(operands) >= 3:
		peer.Vendor = VendorID(operands[0])<<16 | VendorID(operands[1])<<8 | VendorID(operands[2])
	case opcode == OpCECVersion && len(operands) >= 1:
		peer.Version = Version(operands[0])
	case opcode == OpReportPowerStatus && len(operands) >= 1:
		peer.Power = PowerStatus(operands[0])
	}
}

// Forget removes the peer at an address, which a scan does when a poll
// finds no device there.
func (d *Directory) Forget(address LogicalAddress) bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	_, held := d.peers[address]
	delete(d.peers, address)
	return held
}

// Present records a device that acknowledged a poll, and answers
// whether the directory did not hold it yet.
func (d *Directory) Present(address LogicalAddress) bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if address == d.own || address == AddressUnregistered {
		return false
	}
	_, held := d.peers[address]
	d.peer(address)
	return !held
}

// Peers copies every peer, in the order of their logical addresses.
func (d *Directory) Peers() []Peer {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	peers := make([]Peer, 0, len(d.peers))
	for _, peer := range d.peers {
		peers = append(peers, *peer)
	}
	slices.SortFunc(peers, func(a, b Peer) int { return int(a.Logical) - int(b.Logical) })
	return peers
}
