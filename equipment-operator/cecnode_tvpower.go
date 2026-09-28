package main

// The node workload's questions to the TV about its power. Every
// question is traffic on the wire, and a TV can answer traffic by
// switching its input, so each one has a hard bound: a read shares any
// read already in flight, and an announcing TV is asked only while its
// power is unknown, once for each physical address it announces.

import (
	"sync"

	"github.com/liken-sh/equipment-operator/cec"
)

// tvPowerFlight holds the one read of the TV's power in flight.
type tvPowerFlight struct {
	mutex sync.Mutex
	call  *tvPowerCall
}

// tvPowerCall is one read and its answer, which done releases.
type tvPowerCall struct {
	done  chan struct{}
	power cec.PowerStatus
	err   error
}

// askPower reads the TV's power, or shares the read in flight. Two
// questions at once would each wait for a Report Power Status, and the
// kernel hands the first answer to one of the two waiters, so the other
// would time out and report no power. A power press that decides from
// that would turn a room on instead of off. So every reader of the
// TV's power, the press, spec.power, the wake, and the announcement,
// comes through here, as do the join scan and an introduction through
// tvPowerReader, and a reader that arrives while a read is in flight
// waits for that read and takes its answer.
func (n *cecNode) askPower(own cec.LogicalAddress) (cec.PowerStatus, error) {
	flight := &n.tvPowerRead
	flight.mutex.Lock()
	if call := flight.call; call != nil {
		flight.mutex.Unlock()
		<-call.done
		return call.power, call.err
	}
	call := &tvPowerCall{done: make(chan struct{})}
	flight.call = call
	flight.mutex.Unlock()

	call.power, call.err = n.readPower(own)
	n.noteTVPower()

	flight.mutex.Lock()
	flight.call = nil
	flight.mutex.Unlock()
	close(call.done)
	return call.power, call.err
}

// tvPowerReader is the reader the join scan and an introduction ask the
// TV its power through, so their question is the same shared read as
// a press's, and a press while either waits on the TV takes their
// answer. askPower returns an error only when the adapter left.
func (n *cecNode) tvPowerReader(own cec.LogicalAddress) cec.PowerReader {
	return func() error {
		_, err := n.askPower(own)
		return err
	}
}

// askTVPower asks the TV for its power, and nothing else, when it
// announces itself with Report Physical Address or Device Vendor ID
// while the directory does not know its power. A TV in a deep standby
// can answer no power at the scan and announce itself when it wakes,
// and its power would otherwise stay unknown until a power press asks.
// The bound: only the TV, only its power, one queued question for a
// burst of announcements, and no second question at the same physical
// address while the power stays unknown, such as for a TV that refuses
// Give Device Power Status. noteTVPower clears the mark when the
// directory learns the power by any path, so a TV that later loses its
// power again is asked once more. The question goes out only after the
// TV's own broadcast, never on a timer. The caller holds the mutex.
func (n *cecNode) askTVPower(message cec.Message, opcode cec.Opcode, after cec.Peer) {
	if message.From != cec.AddressTV || n.tvPowerAsk == nil || after.Power != cec.PowerUnknown {
		return
	}
	if opcode != cec.OpReportPhysicalAddr && opcode != cec.OpDeviceVendorID {
		return
	}
	if n.tvPowerAskedAt != nil && *n.tvPowerAskedAt == after.Physical {
		return
	}
	physical := after.Physical
	n.tvPowerAskedAt = &physical
	poke(n.tvPowerAsk)
}

// answerTVPowerAsk runs one queued question from askTVPower, and
// answers false when the adapter left. The question waited in a queue,
// and the scan or an introduction that ran first can have read the
// power since, so a power the directory knows now sends nothing.
func (n *cecNode) answerTVPowerAsk(own cec.LogicalAddress) bool {
	if n.directory.Lookup(cec.AddressTV).Power != cec.PowerUnknown {
		return true
	}
	_, err := n.askPower(own)
	return err == nil
}

// noteTVPower clears askTVPower's mark once the directory knows the
// TV's power. The directory learns it from a message the adapter
// hears, from the answer to one of the adapter's own questions, which
// never reaches the loop that hears the bus, and from a message another
// device sends, such as a receiver's Standby. So each of those paths
// calls this.
func (n *cecNode) noteTVPower() {
	if n.directory.Lookup(cec.AddressTV).Power == cec.PowerUnknown {
		return
	}
	n.mutex.Lock()
	n.tvPowerAskedAt = nil
	n.mutex.Unlock()
}
