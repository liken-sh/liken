package cec

// The directory is what one adapter knows about the other devices on
// its bus. It learns from two sources: the messages the adapter hears,
// and the answers to the questions the adapter asks when it joins the
// bus, when a device announces itself, and before a command. A logical
// address is the key, because it is the one address every message
// carries.

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
	// missed counts the power questions each peer acknowledged and did
	// not answer since its last Report Power Status.
	missed map[LogicalAddress]int
}

// NewDirectory starts an empty directory.
func NewDirectory() *Directory {
	return &Directory{own: AddressUnregistered, peers: map[LogicalAddress]*Peer{}, missed: map[LogicalAddress]int{}}
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
	d.missed = map[LogicalAddress]int{}
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
	_, _, changed := d.Hear(message)
	return changed
}

// Hear folds one message into the directory like Observe, and also
// answers the sender as the directory held it before the message and
// after it. A log line needs both in one step: the facts after the
// message name the sender, and the power before it shows whether a
// Report Power Status is news. A sender that is no peer comes back
// with every fact unknown.
func (d *Directory) Hear(message Message) (before, after Peer, changed bool) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if message.From == AddressUnregistered || message.From == d.own {
		stranger := *newPeer(message.From)
		return stranger, stranger, false
	}
	held, known := d.peers[message.From]
	if known {
		before = *held
	} else {
		before = *newPeer(message.From)
	}
	peer := d.peer(message.From)
	learn(peer, message)
	if opcode, _ := message.Opcode(); opcode == OpReportPowerStatus && !message.IsPoll() {
		delete(d.missed, message.From)
	}
	inferred := d.inferTV(message)
	return before, *peer, !known || *peer != before || inferred
}

// inferTV reads what a message states about the TV's power beyond what
// it states about its sender, and answers whether it changed the power
// the directory held. The TV's power then follows the bus without a
// question on a timer. A question on a timer is traffic, and a TV can
// answer traffic by switching its own input. In Control the kernel
// passes the adapter only broadcasts and the messages to its own
// address, and it drops a broadcast Report Power Status for a claim
// that states CEC 1.4, so these messages are most of what the adapter
// can hear when a person turns the TV on or off with its own remote:
//
//   - A Standby to the TV, or a broadcast Standby, puts the TV in
//     standby.
//   - Image View On and Text View On wake the TV. The adapter hears
//     them only in Listen, because they go to the TV.
//   - A Routing Change, a Set Stream Path, or a Request Active Source
//     from the TV comes from a TV that is on: a TV in standby switches
//     no input and asks for no source.
//   - An Active Source comes from a TV that shows its own tuner, or
//     from a source that follows One Touch Play, which wakes the TV
//     with Image View On first.
//
// The directory changes only a TV it holds, because a message about
// the TV does not show that a TV is on the bus. The caller holds the
// mutex.
func (d *Directory) inferTV(message Message) bool {
	tv, held := d.peers[AddressTV]
	if !held || message.IsPoll() {
		return false
	}
	opcode, _ := message.Opcode()
	toTV := message.To == AddressTV
	fromTV := message.From == AddressTV
	power := PowerUnknown
	switch {
	case opcode == OpStandby && (toTV || message.IsBroadcast()):
		power = PowerStandby
	case (opcode == OpImageViewOn || opcode == OpTextViewOn) && toTV:
		power = PowerOn
	case fromTV && (opcode == OpRoutingChange || opcode == OpSetStreamPath || opcode == OpRequestActiveSource):
		power = PowerOn
	case opcode == OpActiveSource && message.IsBroadcast():
		power = PowerOn
	}
	if power == PowerUnknown || tv.Power == power {
		return false
	}
	tv.Power = power
	delete(d.missed, AddressTV)
	return true
}

// Holds answers whether the directory holds a peer at an address.
func (d *Directory) Holds(address LogicalAddress) bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	_, held := d.peers[address]
	return held
}

// Lookup answers the peer at an address, with every fact unknown when
// the directory does not hold one there.
func (d *Directory) Lookup(address LogicalAddress) Peer {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if held, known := d.peers[address]; known {
		return *held
	}
	return *newPeer(address)
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
	delete(d.missed, address)
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

// missedReplies is how many power questions in a row a device may
// leave unanswered before its power is unknown. A TV that wakes can
// miss one question, and forgetting its power then would make its
// Television flap between reachable and not.
const missedReplies = 2

// Unanswered records a device that acknowledged a power question and
// did not answer it. The device is present. After missedReplies such
// questions in a row, its power is unknown.
func (d *Directory) Unanswered(address LogicalAddress) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if address == d.own || address == AddressUnregistered {
		return
	}
	peer := d.peer(address)
	d.missed[address]++
	if d.missed[address] >= missedReplies {
		peer.Power = PowerUnknown
	}
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
