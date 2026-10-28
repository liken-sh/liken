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
	"time"

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
	// Transition is how many power questions the peer answers with the
	// transition, ToOn or ToStandby, after a command changes its power.
	// A real TV takes seconds to wake and reports the transition while
	// it does.
	Transition int
	// Ignore is how many power commands the peer drops before it obeys
	// one, the way cec-follower's --ignore-view-on drops a command, so a
	// test shows what a program does when a command changes nothing.
	Ignore int
	// Lag is how many power questions the peer answers with its old
	// state after a command changes its power. A real TV answers its
	// old state for about 2 seconds after Image View On, so a program
	// that reads the power right after a command reads the past.
	Lag int
	// Grabs is how many times the peer takes the input for itself after
	// another device broadcasts Active Source, the way a streaming
	// player that wakes with the room claims Active Source. GrabAfter is
	// how long it waits before each claim, so a test shows what a
	// program does when the claim arrives after its own.
	Grabs     int
	GrabAfter time.Duration
	// Slow is how long the peer takes to answer each question. The
	// asking transmit waits that long, and the rest of the bus goes on
	// meanwhile, so a test can act while a question waits.
	Slow time.Duration
	// SystemAudio makes the peer an audio system that implements System
	// Audio Control (HDMI-CEC 1.3a, CEC 13.15.2). It answers Give System
	// Audio Mode Status with AudioMode, takes the volume keys, and
	// answers System Audio Mode Request the way AudioReply says, after
	// AudioAfter. AudioInput is the physical address of the last
	// request, which is the input the request switched the peer to. A
	// peer without SystemAudio answers every System Audio message with a
	// Feature Abort, as a device of HDMI 1.3 or earlier does (CEC
	// 13.15.3).
	SystemAudio bool
	AudioMode   cec.SystemAudioMode
	AudioInput  cec.PhysicalAddress
	AudioReply  AudioReply
	AudioAfter  time.Duration

	// towards is the state a transition ends in, and remaining is how
	// many power questions the transition still answers. old is the
	// state a lagging peer still reports, for lagging more questions.
	towards   cec.PowerStatus
	remaining int
	old       cec.PowerStatus
	lagging   int
}

// AudioReply is how an audio system peer answers System Audio Mode
// Request with an operand.
type AudioReply int

const (
	// AudioBroadcast turns the mode on and broadcasts Set System Audio
	// Mode [On], as the audio system does after a TV that takes the
	// feature.
	AudioBroadcast AudioReply = iota
	// AudioToTV turns the mode on and directs Set System Audio Mode [On]
	// to the TV alone, as the audio system's first [On] after a request
	// from a device other than the TV is. An adapter in Control does not
	// hear it.
	AudioToTV
	// AudioRefusedByTV directs Set System Audio Mode [On] to the TV, which
	// answers with a Feature Abort, so the audio system goes no further
	// and the mode stays as it was.
	AudioRefusedByTV
	// AudioSilent acknowledges each System Audio message and answers
	// none, as an audio system with its HDMI control off does.
	AudioSilent
	// AudioAbort answers the request with a Feature Abort.
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

// Peer answers the peer at a logical address as it stands now, so a
// test reads the power a command left it in.
func (b *Bus) Peer(address cec.LogicalAddress) (Peer, bool) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	peer, held := b.peers[address]
	return peer, held
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
	b.obey(message)
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
		reported := p.Power
		if p.lagging > 0 {
			reported = p.old
		}
		return cec.ReportPowerStatus(p.Logical, request.From, reported), false, true
	case cec.OpImageViewOn, cec.OpTextViewOn, cec.OpStandby:
		// A command has no answer, and a device that takes it sends no
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
			// The request's answer is the Set System Audio Mode that
			// requested schedules, and a volume key has none.
			return cec.Message{}, false, false
		}
	}
	return cec.Message{}, true, false
}

// systemAudio are the System Audio messages an audio system peer takes.
var systemAudio = map[cec.Opcode]bool{
	cec.OpGiveSystemAudioModeState: true,
	cec.OpSystemAudioModeRequest:   true,
	cec.OpUserControlPressed:       true,
	cec.OpUserControlReleased:      true,
}

// requested applies a System Audio Mode Request to an audio system
// peer and schedules its Set System Audio Mode. A request with a
// physical address wakes the peer, switches it to that input, and turns
// the mode on; one with no operand turns the mode off and broadcasts
// [Off], as CEC 13.15.2 asks. The statement goes out later and on its
// own goroutine, as a device's own message does. The caller holds the
// mutex.
func (b *Bus) requested(message cec.Message) {
	peer, held := b.peers[message.To]
	if !held || !peer.SystemAudio || message.IsBroadcast() || peer.AudioReply == AudioSilent || peer.AudioReply == AudioAbort {
		return
	}
	operands := message.Operands()
	statement := cec.SetSystemAudioMode(peer.Logical, cec.AddressBroadcast, cec.SystemAudioOff)
	switch {
	case len(operands) < 2:
		peer.AudioMode = cec.SystemAudioOff
	case peer.AudioReply == AudioRefusedByTV:
		peer.Power = cec.PowerOn
		peer.AudioInput = cec.PhysicalAddress(operands[0])<<8 | cec.PhysicalAddress(operands[1])
		statement = cec.SetSystemAudioMode(peer.Logical, cec.AddressTV, cec.SystemAudioOn)
	default:
		peer.Power, peer.AudioMode = cec.PowerOn, cec.SystemAudioOn
		peer.AudioInput = cec.PhysicalAddress(operands[0])<<8 | cec.PhysicalAddress(operands[1])
		statement = cec.SetSystemAudioMode(peer.Logical, cec.AddressBroadcast, cec.SystemAudioOn)
		if peer.AudioReply == AudioToTV {
			statement.To = cec.AddressTV
		}
	}
	b.peers[message.To] = peer
	time.AfterFunc(peer.AudioAfter, func() { b.Send(statement) })
}

// obey applies a power command to the peers it reaches. Image View On
// and Text View On wake the TV and no other device. Standby puts the
// peer it is sent to in standby, or every peer when it is a broadcast.
// The caller holds the mutex.
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

// grab schedules the Active Source of each peer that takes the input
// after another device's Active Source. The claim goes out later and
// on its own goroutine, as a device's own message does. The caller
// holds the mutex.
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

// command is the peer after one power command.
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

// asked is the peer after it answered one request. A power question
// counts down the lag first, then a transition, and the last one ends
// the transition.
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
