package cec

import (
	"slices"
	"sync"
	"time"
)

// A Peer is one other device on the bus. A fact that the device has not
// reported has its unknown value: InvalidPhysicalAddress, an empty
// name, VendorUnknown, VersionUnknown, PowerUnknown, or
// SystemAudioUnknown.
type Peer struct {
	Logical  LogicalAddress
	Physical PhysicalAddress
	// Type is the primary device type the device reported, or the type
	// its logical address implies until it reports one.
	Type    DeviceType
	OSDName string
	Vendor  VendorID
	// NoVendor is true when the device answered Give Device Vendor ID
	// with 000000, which names no vendor. Vendor stays VendorUnknown,
	// and the question counts as answered, so a scan does not ask it
	// again.
	NoVendor bool
	Version  Version
	Power    PowerStatus
	// SystemAudioMode is the mode from the device's last broadcast Set
	// System Audio Mode or System Audio Mode Status, and
	// SystemAudioModeAt is when the adapter received it. Only an audio
	// system sends these messages.
	SystemAudioMode   SystemAudioMode
	SystemAudioModeAt time.Time
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

// Directory records the other devices on one adapter's bus, from the
// messages the adapter receives and the replies to the questions it
// sends. It is keyed by logical address, because every message carries
// the sender's logical address. The receive loop and a scan can use it
// at the same time.
type Directory struct {
	mutex sync.Mutex
	// own is the adapter's own logical address, which is never a peer.
	// A monitor has none, and its own is AddressUnregistered.
	own   LogicalAddress
	peers map[LogicalAddress]*Peer
	// missed counts, for each peer, the Give Device Power Status
	// messages it acknowledged without answering since its last Report
	// Power Status.
	missed map[LogicalAddress]int
}

// NewDirectory returns an empty directory.
func NewDirectory() *Directory {
	return &Directory{own: AddressUnregistered, peers: map[LogicalAddress]*Peer{}, missed: map[LogicalAddress]int{}}
}

// SetOwn records the adapter's own logical address and removes any peer
// at that address.
func (d *Directory) SetOwn(address LogicalAddress) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.own = address
	delete(d.peers, address)
}

// Reset removes every peer. The caller resets the directory when the
// adapter changes mode or bus, because the peers it has may be devices
// on another bus.
func (d *Directory) Reset() {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.peers = map[LogicalAddress]*Peer{}
	d.missed = map[LogicalAddress]int{}
}

// peer returns the peer at an address and adds it when it is absent.
// The caller holds the mutex.
func (d *Directory) peer(address LogicalAddress) *Peer {
	held, found := d.peers[address]
	if !found {
		held = newPeer(address)
		d.peers[address] = held
	}
	return held
}

// Observe applies one message to the directory and returns whether
// anything changed. A device that sends a message is present, so its
// address becomes a peer. The unregistered address is not a device, and
// the adapter's own messages say nothing about a peer.
func (d *Directory) Observe(message Message) bool {
	_, _, changed := d.Hear(message)
	return changed
}

// Hear applies one message like Observe, and also returns the sender
// before and after the message, for the log line: after identifies the
// sender, and before shows whether a Report Power Status changed its
// power. For a sender that was not a peer, before has every fact
// unknown. For a message that Observe ignores, both have every fact
// unknown.
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
	learn(peer, message, time.Now())
	if opcode, _ := message.Opcode(); opcode == OpReportPowerStatus && !message.IsPoll() {
		delete(d.missed, message.From)
	}
	inferred := d.inferTV(message)
	return before, *peer, !known || *peer != before || inferred
}

// inferTV sets the TV's power from messages that imply it, and returns
// whether the power changed. This keeps the TV's power current without
// a Give Device Power Status on a timer, because some TVs respond to
// that traffic by switching their own input. In Control mode the kernel
// passes the adapter only broadcasts and messages to its own address.
// The adapter claims its address as a CEC 1.4 device, and for such a
// claim the kernel drops a broadcast Report Power Status. So when
// someone turns the TV on or off with its own remote, these messages
// are most of what the adapter receives:
//
//   - A Standby to the TV, or a broadcast Standby, means the TV is in
//     standby.
//   - Image View On and Text View On mean the TV is on. The adapter
//     receives them only in Listen mode, because they are addressed to
//     the TV.
//   - A Routing Change, Set Stream Path, or Request Active Source from
//     the TV means the TV is on, because a TV in standby does not
//     switch inputs or request a source.
//   - A broadcast Active Source means the TV is on. It comes from the
//     TV when it shows its own tuner, or from a source that follows One
//     Touch Play and sent Image View On first.
//
// The power changes only for a TV already in the directory, because a
// message about the TV does not show that a TV is on the bus. The
// caller holds the mutex.
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

// Holds returns whether the directory has a peer at an address.
func (d *Directory) Holds(address LogicalAddress) bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	_, held := d.peers[address]
	return held
}

// Lookup returns the peer at an address, with every fact unknown when
// the directory has none there.
func (d *Directory) Lookup(address LogicalAddress) Peer {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if held, known := d.peers[address]; known {
		return *held
	}
	return *newPeer(address)
}

// learn applies what a message states about its sender, received at
// now.
//
// A directed Set System Audio Mode does not set the mode. After a
// System Audio Mode Request, the audio system sends its first [On] to
// the TV, and stops there when the TV answers with a Feature Abort
// (HDMI-CEC 1.3a, CEC 13.15.2). So a directed [On] that a monitor
// receives can be for a mode that never started. A broadcast Set System
// Audio Mode sets the mode, and so does a System Audio Mode Status,
// whichever device asked for it.
func learn(peer *Peer, message Message, now time.Time) {
	opcode, _ := message.Opcode()
	operands := message.Operands()
	switch {
	case opcode == OpReportPhysicalAddr && len(operands) >= 3:
		peer.Physical = PhysicalAddress(operands[0])<<8 | PhysicalAddress(operands[1])
		if kind, found := primaryTypes[operands[2]]; found {
			peer.Type = kind
		}
	case opcode == OpActiveSource && len(operands) >= 2:
		peer.Physical = PhysicalAddress(operands[0])<<8 | PhysicalAddress(operands[1])
	case opcode == OpSetOSDName && len(operands) > 0:
		peer.OSDName = string(operands)
	case opcode == OpDeviceVendorID && len(operands) >= 3:
		// A vendor of 000000 names no vendor, and libCEC reads it as
		// unknown. A TV in standby behind an A/V receiver has sent it
		// between reports of its real vendor, so a peer with a vendor
		// keeps it.
		vendor := VendorID(operands[0])<<16 | VendorID(operands[1])<<8 | VendorID(operands[2])
		switch {
		case vendor != vendorZero:
			peer.Vendor, peer.NoVendor = vendor, false
		case peer.Vendor == VendorUnknown:
			peer.NoVendor = true
		}
	case opcode == OpCECVersion && len(operands) >= 1:
		peer.Version = Version(operands[0])
	case opcode == OpReportPowerStatus && len(operands) >= 1:
		peer.Power = PowerStatus(operands[0])
	case (opcode == OpSetSystemAudioMode && message.IsBroadcast() || opcode == OpSystemAudioModeStatus) && len(operands) >= 1:
		if mode := audioModeOf(operands[0]); mode != SystemAudioUnknown {
			peer.SystemAudioMode, peer.SystemAudioModeAt = mode, now
		}
	}
}

// Forget removes the peer at an address and returns whether there was
// one. A scan calls it when a poll finds no device there.
func (d *Directory) Forget(address LogicalAddress) bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	_, held := d.peers[address]
	delete(d.peers, address)
	delete(d.missed, address)
	return held
}

// Present records a device that acknowledged a poll, and returns true
// when the directory did not have it yet.
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

// missedReplies is how many Give Device Power Status messages in a row
// a device can leave unanswered before its power becomes unknown. A TV
// that is waking can miss one, and forgetting its power then would make
// its Television flap between reachable and not.
const missedReplies = 2

// Unanswered records a device that acknowledged a Give Device Power
// Status without answering it. The device is present, and after
// missedReplies in a row its power is unknown.
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

// Peers returns a copy of every peer, sorted by logical address.
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
