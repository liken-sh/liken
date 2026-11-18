// Package cectest is a CEC bus in memory, for tests. It implements the
// kernel's CEC ioctls for the calls this repository makes, so a test
// can drive a real cec.Device through the same ioctl boundary that a
// /dev/cecN node serves. The bus holds scripted peers, such as a TV
// and a receiver, and any number of adapters.
//
// It copies only some of the kernel's rules: logical address claims,
// the modes, polls, replies, the kernel's own replies on behalf of its
// adapter, and ENODEV after an unplug. The integration tests against
// the kernel's vivid driver check the same behavior on the real CEC
// core.
package cectest

import (
	"sync"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// A Peer is one scripted device on the bus. It acknowledges polls to
// its logical address and replies to identity and power requests from
// its fields. With Mute set it still acknowledges polls, but it sends
// no reply and ignores power commands, like a TV in deep standby.
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
	// arbitration, as a disturbed wire does. A test uses it to check
	// how a program handles a failure that is not a NACK.
	Garbled bool
	// Transition is the number of power status requests that the peer
	// answers with the transition state, ToOn or ToStandby, after a
	// command changes its power. A real TV takes seconds to wake and
	// reports the transition while it does.
	Transition int
	// Ignore is the number of power commands that the peer drops before
	// it obeys one, like cec-follower's --ignore-view-on. A test uses
	// it to check how a program handles a command that changes nothing.
	Ignore int
	// Lag is the number of power status requests that the peer answers
	// with its old state after a command changes its power. A real TV
	// reports its old state for about 2 seconds after Image View On, so
	// a program that reads the power right after a command reads the
	// state from before it.
	Lag int
	// Grabs is the number of times that the peer broadcasts its own
	// Active Source, GrabAfter later, when another device broadcasts
	// Active Source. Some streaming players take the TV's input this
	// way when the TV turns on. A test uses it to check how a program
	// handles a claim that arrives after its own.
	Grabs     int
	GrabAfter time.Duration
	// Slow is how long the peer takes to reply to each request. The
	// transmit that sent the request waits that long while the rest of
	// the bus keeps running, so a test can act while a request is
	// outstanding.
	Slow time.Duration
	// SystemAudio makes the peer an audio system that implements System
	// Audio Control (HDMI-CEC 1.3a, CEC 13.15.2). It replies to Give
	// System Audio Mode Status with AudioMode, accepts the volume keys,
	// and handles a System Audio Mode Request as AudioReply sets.
	// AudioInput is the physical address in the last request, which is
	// the input that the request switched the peer to. A peer without
	// SystemAudio replies to every System Audio message with a Feature
	// Abort, as a device of HDMI 1.3 or earlier does (CEC 13.15.3).
	SystemAudio bool
	AudioMode   cec.SystemAudioMode
	AudioInput  cec.PhysicalAddress
	AudioReply  AudioReply
	AudioAfter  time.Duration

	// After a power command, the next lagging power status requests
	// report old, and the next remaining requests report the transition
	// state. Then Power becomes towards.
	towards   cec.PowerStatus
	remaining int
	old       cec.PowerStatus
	lagging   int
}

// AudioReply is how an audio system peer handles a System Audio Mode
// Request. The peer sends its Set System Audio Mode AudioAfter after
// the request.
type AudioReply int

const (
	// AudioBroadcast turns the mode on and broadcasts Set System Audio
	// Mode [On], as an audio system does when the TV supports the
	// feature.
	AudioBroadcast AudioReply = iota
	// AudioToTV turns the mode on and sends Set System Audio Mode [On]
	// to the TV only, as an audio system does the first time after a
	// request from a device other than the TV. Only an adapter in
	// monitor-all mode receives it.
	AudioToTV
	// AudioRefusedByTV sends Set System Audio Mode [On] to the TV and
	// leaves the mode off, as an audio system does when the TV refuses
	// the mode with a Feature Abort. The bus sends no Feature Abort for
	// the TV.
	AudioRefusedByTV
	// AudioSilent acknowledges each System Audio message and sends no
	// reply, as an audio system with HDMI control turned off does.
	AudioSilent
	// AudioAbort replies to the request with a Feature Abort.
	AudioAbort
)

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

// Peer returns the peer at a logical address in its current state, so
// a test can read the power state that a command left it in.
func (b *Bus) Peer(address cec.LogicalAddress) (Peer, bool) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	peer, found := b.peers[address]
	return peer, found
}

// Remove deletes the peer at a logical address from the bus.
func (b *Bus) Remove(address cec.LogicalAddress) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	delete(b.peers, address)
}

// Send puts a message from a peer on the wire. Every adapter whose
// mode passes the message to its handle receives it.
func (b *Bus) Send(message cec.Message) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.obey(message)
	for _, reply := range b.carry(message, nil) {
		b.carry(reply, nil)
	}
}

// Sent returns every message that the adapters transmitted, in order.
func (b *Bus) Sent() []cec.Message {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return append([]cec.Message(nil), b.sent...)
}

// carry delivers a message to every adapter except its sender, and
// returns the replies that the kernel sent on behalf of a receiving
// adapter. The caller holds the mutex.
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

// occupied reports whether a peer or an adapter other than asker holds
// a logical address. The caller holds the mutex.
func (b *Bus) occupied(address cec.LogicalAddress, asker *Adapter) bool {
	if _, found := b.peers[address]; found {
		return true
	}
	for _, adapter := range b.adapters {
		if adapter != asker && adapter.holds(address) {
			return true
		}
	}
	return false
}

// answer returns the reply that the peer sends to a request. ok is
// false when the peer sends no reply. aborted is true when it sends a
// Feature Abort instead.
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
		reported := p.Power
		if p.lagging > 0 {
			reported = p.old
		}
		return cec.ReportPowerStatus(p.Logical, request.From, reported), false, true
	case cec.OpImageViewOn, cec.OpTextViewOn, cec.OpStandby:
		// A command has no reply, and a device that supports it sends no
		// Feature Abort.
		return cec.Message{}, false, false
	}
	if p.SystemAudio {
		switch {
		case !systemAudio[opcode]:
		case p.AudioReply == AudioSilent:
			return cec.Message{}, false, false
		case opcode == cec.OpGiveSystemAudioModeState:
			return cec.SystemAudioModeStatus(p.Logical, request.From, p.AudioMode), false, true
		case opcode == cec.OpSystemAudioModeRequest && p.AudioReply == AudioAbort:
			return cec.Message{}, true, false
		default:
			// The reply to a System Audio Mode Request is the Set System
			// Audio Mode that requested schedules. A volume key has no
			// reply.
			return cec.Message{}, false, false
		}
	}
	return cec.Message{}, true, false
}

// systemAudio is the set of opcodes that an audio system peer accepts.
var systemAudio = map[cec.Opcode]bool{
	cec.OpGiveSystemAudioModeState: true,
	cec.OpSystemAudioModeRequest:   true,
	cec.OpUserControlPressed:       true,
	cec.OpUserControlReleased:      true,
}

// requested applies a directed System Audio Mode Request to an audio
// system peer. A request with a physical address turns the peer on,
// switches it to that input, and turns the mode on, except with
// AudioRefusedByTV. A request with no operand turns the mode off and
// broadcasts Set System Audio Mode [Off], as CEC 13.15.2 requires. The
// peer sends its Set System Audio Mode AudioAfter later from its own
// goroutine, as a real device sends a message that it originates. A
// peer with AudioSilent or AudioAbort sends nothing from here. The
// caller holds the mutex.
func (b *Bus) requested(message cec.Message) {
	peer, found := b.peers[message.To]
	if !found || !peer.SystemAudio || message.IsBroadcast() || peer.AudioReply == AudioSilent || peer.AudioReply == AudioAbort {
		return
	}
	operands := message.Operands()
	setMode := cec.SetSystemAudioMode(peer.Logical, cec.AddressBroadcast, cec.SystemAudioOff)
	switch {
	case len(operands) < 2:
		peer.AudioMode = cec.SystemAudioOff
	case peer.AudioReply == AudioRefusedByTV:
		peer.Power = cec.PowerOn
		peer.AudioInput = cec.PhysicalAddress(operands[0])<<8 | cec.PhysicalAddress(operands[1])
		setMode = cec.SetSystemAudioMode(peer.Logical, cec.AddressTV, cec.SystemAudioOn)
	default:
		peer.Power, peer.AudioMode = cec.PowerOn, cec.SystemAudioOn
		peer.AudioInput = cec.PhysicalAddress(operands[0])<<8 | cec.PhysicalAddress(operands[1])
		setMode = cec.SetSystemAudioMode(peer.Logical, cec.AddressBroadcast, cec.SystemAudioOn)
		if peer.AudioReply == AudioToTV {
			setMode.To = cec.AddressTV
		}
	}
	b.peers[message.To] = peer
	time.AfterFunc(peer.AudioAfter, func() { b.Send(setMode) })
}

// obey changes the peers' state for a message on the wire. Image View
// On and Text View On turn on the TV and no other device. Standby puts
// the addressed peer in standby, or every peer when it is broadcast. A
// Mute peer ignores both. The caller holds the mutex.
func (b *Bus) obey(message cec.Message) {
	opcode, _ := message.Opcode()
	var want cec.PowerStatus
	switch {
	case message.IsPoll():
		return
	case opcode == cec.OpActiveSource:
		b.grab(message)
		return
	case opcode == cec.OpSystemAudioModeRequest:
		b.requested(message)
		return
	case opcode == cec.OpImageViewOn || opcode == cec.OpTextViewOn:
		want = cec.PowerOn
	case opcode == cec.OpStandby:
		want = cec.PowerStandby
	default:
		return
	}
	for address, peer := range b.peers {
		reached := address == message.To || message.IsBroadcast()
		if !reached || peer.Mute || (want == cec.PowerOn && address != cec.AddressTV) {
			continue
		}
		b.peers[address] = peer.command(want)
	}
}

// grab schedules an Active Source broadcast, GrabAfter later, from each
// peer other than the sender that has Grabs left. The caller holds the
// mutex.
func (b *Bus) grab(message cec.Message) {
	for address, peer := range b.peers {
		if peer.Grabs == 0 || address == message.From {
			continue
		}
		peer.Grabs--
		b.peers[address] = peer
		claim := cec.ActiveSource(peer.Logical, peer.Physical)
		time.AfterFunc(peer.GrabAfter, func() { b.Send(claim) })
	}
}

// command returns the peer after it receives a power command.
func (p Peer) command(want cec.PowerStatus) Peer {
	if p.Ignore == 0 && p.Power != want && p.Lag > 0 {
		p.old, p.lagging = p.Power, p.Lag
	}
	switch {
	case p.Ignore > 0:
		p.Ignore--
	case p.Power == want:
	case p.Transition == 0:
		p.Power = want
	default:
		p.towards, p.remaining = want, p.Transition
		p.Power = cec.PowerToOn
		if want == cec.PowerStandby {
			p.Power = cec.PowerToStandby
		}
	}
	return p
}

// asked returns the peer after it replies to a request. Each power
// status request counts down lagging first and then remaining, and the
// request that ends the transition sets Power to towards.
func (p Peer) asked(request cec.Message) Peer {
	opcode, _ := request.Opcode()
	if opcode != cec.OpGiveDevicePowerStatus {
		return p
	}
	if p.lagging > 0 {
		p.lagging--
		return p
	}
	if p.remaining == 0 {
		return p
	}
	p.remaining--
	if p.remaining == 0 {
		p.Power = p.towards
	}
	return p
}
