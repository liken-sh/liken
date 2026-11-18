package main

import (
	"sync"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// tvPowerReadCall holds the read of the TV's power in progress. call is
// nil when no read runs.
type tvPowerReadCall struct {
	mutex sync.Mutex
	call  *tvPowerCall
}

// tvPowerCall is one read of the TV's power. power and err are valid
// after done is closed.
type tvPowerCall struct {
	done  chan struct{}
	power cec.PowerStatus
	err   error
}

// readTVPower reads the TV's power, or waits for the read in progress and
// returns its result. When two reads run at once, each waits for a
// Report Power Status, and the kernel delivers the reply to only one of
// them. The other times out with no power, and a powerAsk that acts on
// that turns the room on when it should turn it off. So every read
// of the TV's power goes through readTVPower.
func (n *cecNode) readTVPower(own cec.LogicalAddress) (cec.PowerStatus, error) {
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

// tvPowerReader returns a cec.PowerReader for cec.Scan and
// cec.Introduce that reads through readTVPower. It returns an error only
// when the adapter has gone.
func (n *cecNode) tvPowerReader(own cec.LogicalAddress) cec.PowerReader {
	return func() error {
		_, err := n.readTVPower(own)
		return err
	}
}

// queueTVPowerRead queues a read of the TV's power when the TV broadcasts
// Report Physical Address or Device Vendor ID and the directory does
// not know its power. A TV in deep standby can miss the join scan and
// announce itself when it wakes, and without this read its power stays
// unknown until a powerAsk reads it. It queues one read for each
// physical address the TV announces until the power is known. Each read
// is bus traffic, and a TV can switch its input in response, so a TV
// that never answers Give Device Power Status is not read again on
// every announcement. The caller holds n.mutex.
func (n *cecNode) queueTVPowerRead(message cec.Message, opcode cec.Opcode, after cec.Peer) {
	if message.From != cec.AddressTV || n.tvPowerReadQueue == nil || after.Power != cec.PowerUnknown {
		return
	}
	if opcode != cec.OpReportPhysicalAddr && opcode != cec.OpDeviceVendorID {
		return
	}
	if n.tvPowerQueuedAddress != nil && *n.tvPowerQueuedAddress == after.Physical {
		return
	}
	physical := after.Physical
	n.tvPowerQueuedAddress = &physical
	sendWake(n.tvPowerReadQueue)
}

// runQueuedTVPowerRead runs a read that queueTVPowerRead queued, and returns false
// when the adapter has gone. A scan or an introduction can have read
// the power while the read waited in the queue, so it reads nothing
// when the directory already has the power.
func (n *cecNode) runQueuedTVPowerRead(own cec.LogicalAddress) bool {
	if n.directory.Lookup(cec.AddressTV).Power != cec.PowerUnknown {
		return true
	}
	_, err := n.readTVPower(own)
	return err == nil
}

// noteTVPower clears tvPowerQueuedAddress once the directory has the TV's
// power, so queueTVPowerRead reads again when the power later becomes unknown.
// The reply to the adapter's own read does not pass through heard, so
// readTVPower and introduce call this as well as heard.
func (n *cecNode) noteTVPower() {
	if n.directory.Lookup(cec.AddressTV).Power == cec.PowerUnknown {
		return
	}
	n.mutex.Lock()
	n.tvPowerQueuedAddress = nil
	n.mutex.Unlock()
}
