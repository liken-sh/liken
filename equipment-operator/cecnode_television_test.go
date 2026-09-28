package main

// The node workload and a Television: the power that follows the bus,
// the command that applies each generation of spec.power once, the
// readback that confirms it, and which adapter of a bus sends it.
// cecnode_test.go holds the adapter fixtures, and cecnode_power_test.go
// holds the cases where an application meets a change, a failure, or a
// restart.

import (
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// fastPower shortens the confirmation and the settle time, so a test
// that waits on either finishes in milliseconds.
func fastPower(t *testing.T) {
	t.Helper()
	every, window, settle := cecPowerReadEvery, cecPowerWindow, cecPowerSettle
	cecPowerReadEvery, cecPowerWindow, cecPowerSettle = 5*time.Millisecond, 50*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() {
		cecPowerReadEvery, cecPowerWindow, cecPowerSettle = every, window, settle
	})
}

// roomWithTV is the room's wire with the TV replaced by one in the
// given state.
func roomWithTV(tv cectest.Peer) *cectest.Bus {
	wire := cecRoom()
	wire.Add(tv)
	return wire
}

// lounge is a person's Television on the den bus.
func lounge(power TelevisionPower) Television {
	return Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}, Power: power}}
}

// controlling runs the node workload for node-1 on a bus in Control
// with one Television, until the test ends.
func controlling(t *testing.T, wire *cectest.Bus, television Television) *cecAPI {
	t.Helper()
	api := startCECAPI(t)
	_, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	api.putTelevision(television)
	startNode(t, api, "node-1", device)
	return api
}

// sentOf counts the messages on the wire with one opcode.
func sentOf(wire *cectest.Bus, opcode cec.Opcode) int {
	count := 0
	for _, message := range wire.Sent() {
		if sent, _ := message.Opcode(); sent == opcode && !message.IsPoll() {
			count++
		}
	}
	return count
}

// mustCommandTheTVAlone fails the test when a power command went to
// any address but the TV's. A broadcast Standby would put the receiver
// and every source in standby too.
func mustCommandTheTVAlone(t *testing.T, wire *cectest.Bus) {
	t.Helper()
	for _, message := range wire.Sent() {
		opcode, _ := message.Opcode()
		if !message.IsPoll() && (opcode == cec.OpImageViewOn || opcode == cec.OpStandby) && message.To != cec.AddressTV {
			t.Errorf("a power command went to address %d: %v", message.To, message)
		}
	}
}

// televisionTV is the room's TV at a power.
func televisionTV(power cec.PowerStatus) cectest.Peer {
	return cectest.Peer{Logical: 0, Physical: 0x0000, PrimaryType: 0, OSDName: "TV", Vendor: 0x00e091, Version: cec.Version14, Power: power}
}

// appliedAt waits until the node workload recorded a generation of a
// Television as applied.
func appliedAt(t *testing.T, api *cecAPI, name string, generation int64) Television {
	t.Helper()
	return api.waitForTelevision(t, name, func(television Television) bool { return television.Status.PowerGeneration == generation })
}

func TestTheAdapterAppliesThePowerAndConfirmsIt(t *testing.T) {
	slow := func(power cec.PowerStatus) cectest.Peer {
		tv := televisionTV(power)
		tv.Transition = 2
		return tv
	}
	ignoring := televisionTV(cec.PowerStandby)
	ignoring.Ignore = 1
	// A TV whose transition lasts longer than one window of reads is on
	// its way, and it gets no second command.
	slower := televisionTV(cec.PowerStandby)
	slower.Transition = 15
	cases := []struct {
		name    string
		tv      cectest.Peer
		power   TelevisionPower
		command cec.Opcode
		sends   int
		message string
	}{
		{"a wake", televisionTV(cec.PowerStandby), TelevisionOn, cec.OpImageViewOn, 1,
			"the TV reported On after the adapter on node-1 sent Image View On once"},
		{"a standby", televisionTV(cec.PowerOn), TelevisionStandby, cec.OpStandby, 1,
			"the TV reported Standby after the adapter on node-1 sent Standby once"},
		{"a TV that reports the transition first", slow(cec.PowerStandby), TelevisionOn, cec.OpImageViewOn, 1,
			"the TV reported On after the adapter on node-1 sent Image View On once"},
		{"a TV whose transition outlasts a window", slower, TelevisionOn, cec.OpImageViewOn, 1,
			"the TV reported On after the adapter on node-1 sent Image View On once"},
		{"a TV that drops the first command", ignoring, TelevisionOn, cec.OpImageViewOn, 2,
			"the TV reported On after the adapter on node-1 sent Image View On 2 times"},
		{"a TV already in the state", televisionTV(cec.PowerOn), TelevisionOn, cec.OpImageViewOn, 0,
			"the TV already reported On, so the adapter on node-1 sent no command"},
	}
	// The subtests run side by side, so the timing is set once for
	// all of them.
	fastPower(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wire := roomWithTV(c.tv)

			api := controlling(t, wire, lounge(c.power))

			television := appliedAt(t, api, "lounge", 1)
			applied := conditionOf(television.Status.Conditions, conditionPowerApplied)
			mustMatch(t, applied.Status, ConditionTrue)
			mustMatch(t, applied.Reason, reasonConfirmed)
			mustMatch(t, applied.Message, c.message)
			mustMatch(t, applied.ObservedGeneration, int64(1))
			mustMatch(t, sentOf(wire, c.command), c.sends)
			mustCommandTheTVAlone(t, wire)
		})
	}
}

func TestATVThatNeverReportsTheStateIsUnconfirmed(t *testing.T) {
	fastPower(t)
	stubborn := televisionTV(cec.PowerStandby)
	stubborn.Ignore = 10
	wire := roomWithTV(stubborn)

	api := controlling(t, wire, lounge(TelevisionOn))

	television := appliedAt(t, api, "lounge", 1)
	applied := conditionOf(television.Status.Conditions, conditionPowerApplied)
	mustMatch(t, applied.Status, ConditionFalse)
	mustMatch(t, applied.Reason, reasonUnconfirmed)
	mustMatch(t, applied.Message, "the adapter on node-1 sent Image View On 3 times, and the TV last reported Standby")
	mustMatch(t, sentOf(wire, cec.OpImageViewOn), 3)
}

// A TV that does not acknowledge the command is unconfirmed, and the
// message gives the kernel's words for the last transmit.
func TestATVThatIsGoneBeforeTheCommandIsUnconfirmed(t *testing.T) {
	fastPower(t)
	wire := cecRoom()
	wire.Remove(0)

	api := controlling(t, wire, lounge(TelevisionOn))

	television := appliedAt(t, api, "lounge", 1)
	mustMatch(t, conditionOf(television.Status.Conditions, conditionPowerApplied).Message,
		"the adapter on node-1 sent Image View On 3 times, and the TV did not answer Give Device Power Status; the TV did not acknowledge the last command: NACK MAX_RETRIES")
}

// Only the first adapter in spec.adapters that holds a logical address
// sends the bus's commands, so the TV gets one command and not one from
// each adapter.
func TestOneAdapterSendsTheBussCommands(t *testing.T) {
	fastPower(t)
	wire := roomWithTV(televisionTV(cec.PowerStandby))
	api := startCECAPI(t)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putDisplay("bnq-0002-monitor", "node-2", "1.4.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}, CECBusAdapter{Machine: "node-2", Display: "bnq-0002-monitor"}))
	api.putTelevision(lounge(""))
	for _, machine := range []string{"node-1", "node-2"} {
		_, device := usbAdapter(wire)
		startNode(t, api, machine, device)
		api.waitForEntry(t, "den", machine, func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	}
	first, _ := api.entry("den", "node-1")

	api.putTelevision(lounge(TelevisionOn))

	appliedAt(t, api, "lounge", 2)
	var senders []cec.LogicalAddress
	for _, message := range wire.Sent() {
		if opcode, _ := message.Opcode(); opcode == cec.OpImageViewOn && !message.IsPoll() {
			senders = append(senders, message.From)
		}
	}
	mustDeepEqual(t, senders, []cec.LogicalAddress{cec.LogicalAddress(*first.LogicalAddress)})
}

func TestABusInListenSendsNoPowerCommand(t *testing.T) {
	fastPower(t)
	api := startCECAPI(t)
	wire := roomWithTV(televisionTV(cec.PowerStandby))
	_, device := usbAdapter(wire)
	api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
	api.putTelevision(lounge(TelevisionOn))

	startNode(t, api, "node-1", device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
	time.Sleep(10 * cecPowerWindow)

	if sent := wire.Sent(); len(sent) != 0 {
		t.Errorf("a listening adapter sent %v", sent)
	}
	television, _ := api.television("lounge")
	mustMatch(t, television.Status.PowerGeneration, int64(0))
}

// No timer asks the TV for its power. A TV that a person turns on or
// off with its own remote shows in the entry through what the TV sends
// the bus: a broadcast Standby, or a Routing Change of a TV that is on.
func TestTheTVsPowerFollowsItsBroadcasts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		held    cec.PowerStatus
		message cec.Message
		power   string
	}{
		{"a TV turned off with its own remote", cec.PowerOn, cec.Standby(0, 15), "Standby"},
		{"a TV turned on with its own remote", cec.PowerStandby, cec.NewMessage(0, 15, cec.OpRoutingChange, 0x12, 0x00, 0x10, 0x00), "On"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wire := roomWithTV(televisionTV(c.held))
			api := controlling(t, wire, lounge(""))
			api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
			sent := len(wire.Sent())

			wire.Send(c.message)

			api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool {
				return len(entry.Devices) > 0 && entry.Devices[0].LogicalAddress == 0 && entry.Devices[0].Power == c.power
			})
			mustMatch(t, len(wire.Sent()), sent)
		})
	}
}

// A cluster without the Television definition still runs the bus: the
// node workload reads the missing collection as empty.
func TestTheNodeWorkloadRunsWithoutTheTelevisionDefinition(t *testing.T) {
	t.Parallel()
	api := startCECAPI(t)
	api.noTelevisionDefinition = true
	_, device := usbAdapter(cecRoom())
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))

	startNode(t, api, "node-1", device)

	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
}
