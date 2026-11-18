package main

// Tests of the node workload's handling of a Television. The adapter
// fixtures are in cecnode_test.go, and cecnode_power_test.go tests an
// application of spec.power that meets a spec change, a failure, or a
// restart.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// busWithTV returns the bus of fakeCECBus with tv in place of its TV.
func busWithTV(tv cectest.Peer) *cectest.Bus {
	wire := fakeCECBus()
	wire.Add(tv)
	return wire
}

// lounge is a declared Television on the den bus.
func lounge(power TelevisionPower) Television {
	return Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}, Power: power}}
}

// controlling runs the node workload for node-1 until the test ends, on
// a bus in Control mode with one Television.
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

// sentOf counts the messages with the opcode sent on the wire, not
// counting polls.
func sentOf(wire *cectest.Bus, opcode cec.Opcode) int {
	count := 0
	for _, message := range wire.Sent() {
		if sent, _ := message.Opcode(); sent == opcode && !message.IsPoll() {
			count++
		}
	}
	return count
}

// mustCommandTheTVAlone fails the test when a power command went to an
// address other than the TV's. A broadcast Standby would also put the
// receiver and every source in standby.
func mustCommandTheTVAlone(t *testing.T, wire *cectest.Bus) {
	t.Helper()
	for _, message := range wire.Sent() {
		opcode, _ := message.Opcode()
		if !message.IsPoll() && (opcode == cec.OpImageViewOn || opcode == cec.OpStandby) && message.To != cec.AddressTV {
			t.Errorf("a power command went to address %d, want only the TV at address %d: %v", message.To, cec.AddressTV, message)
		}
	}
}

// televisionTV returns the TV of fakeCECBus with the given power.
func televisionTV(power cec.PowerStatus) cectest.Peer {
	return cectest.Peer{Logical: 0, Physical: 0x0000, PrimaryType: 0, OSDName: "TV", Vendor: 0x00e091, Version: cec.Version14, Power: power}
}

// appliedAt waits until the node workload records a generation of the
// Television as applied. An application takes at most cecPowerSends
// windows.
func appliedAt(t *testing.T, api *cecAPI, name string, generation int64) Television {
	t.Helper()
	return api.waitForTelevisionWithin(t, name, time.Duration(cecPowerSends)*cecPowerWindow+testTimeout, func(television Television) bool {
		return television.Status.PowerGeneration == generation
	})
}

func TestTheAdapterAppliesThePowerAndConfirmsIt(t *testing.T) {
	slow := func(power cec.PowerStatus) cectest.Peer {
		tv := televisionTV(power)
		tv.Transition = 2
		return tv
	}
	ignoring := televisionTV(cec.PowerStandby)
	ignoring.Ignore = 1
	// A TV that still reports a transition after one window of reads is
	// changing state, so the adapter sends no second command.
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
			"The TV reported On after the adapter on node-1 sent Image View On once"},
		{"a standby", televisionTV(cec.PowerOn), TelevisionStandby, cec.OpStandby, 1,
			"The TV reported Standby after the adapter on node-1 sent Standby once"},
		{"a TV that reports the transition first", slow(cec.PowerStandby), TelevisionOn, cec.OpImageViewOn, 1,
			"The TV reported On after the adapter on node-1 sent Image View On once"},
		{"a TV whose transition outlasts a window", slower, TelevisionOn, cec.OpImageViewOn, 1,
			"The TV reported On after the adapter on node-1 sent Image View On once"},
		{"a TV that drops the first command", ignoring, TelevisionOn, cec.OpImageViewOn, 2,
			"The TV reported On after the adapter on node-1 sent Image View On 2 times"},
		{"a TV already in the state", televisionTV(cec.PowerOn), TelevisionOn, cec.OpImageViewOn, 0,
			"The TV already reported On, so the adapter on node-1 sent no command"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithTV(c.tv)

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
		})
	}
}

func TestATVThatNeverReportsTheStateIsUnconfirmed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		stubborn := televisionTV(cec.PowerStandby)
		stubborn.Ignore = 10
		wire := busWithTV(stubborn)

		api := controlling(t, wire, lounge(TelevisionOn))

		television := appliedAt(t, api, "lounge", 1)
		applied := conditionOf(television.Status.Conditions, conditionPowerApplied)
		mustMatch(t, applied.Status, ConditionFalse)
		mustMatch(t, applied.Reason, reasonUnconfirmed)
		mustMatch(t, applied.Message, "The adapter on node-1 sent Image View On 3 times, and the TV last reported Standby")
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 3)
	})
}

// A TV that does not acknowledge the command is unconfirmed, and the
// message includes the kernel's status for the last transmit.
func TestATVThatIsGoneBeforeTheCommandIsUnconfirmed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := fakeCECBus()
		wire.Remove(0)

		api := controlling(t, wire, lounge(TelevisionOn))

		television := appliedAt(t, api, "lounge", 1)
		mustMatch(t, conditionOf(television.Status.Conditions, conditionPowerApplied).Message,
			"The adapter on node-1 sent Image View On 3 times, and the TV did not answer Give Device Power Status. The TV did not acknowledge the last command: NACK MAX_RETRIES")
	})
}

// Only the first adapter in spec.adapters that holds a logical address
// sends the bus's commands, so the TV gets one command and not one from
// each adapter.
func TestOnlyOneAdapterSendsTheBusCommands(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
		api := startCECAPI(t)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putDisplay("bnq-0002-monitor", "node-2", "1.4.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}, CECBusAdapter{Machine: "node-2", Display: "bnq-0002-monitor"}))
		api.putTelevision(lounge(""))
		for _, machine := range []string{"node-1", "node-2"} {
			_, device := usbAdapter(wire)
			startNode(t, api, machine, device)
			api.scanned(t, machine)
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
	})
}

func TestABusInListenSendsNoPowerCommand(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := busWithTV(televisionTV(cec.PowerStandby))
		_, device := usbAdapter(wire)
		api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
		api.putTelevision(lounge(TelevisionOn))

		startNode(t, api, "node-1", device)
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
		time.Sleep(10 * cecPowerWindow)

		if sent := wire.Sent(); len(sent) != 0 {
			t.Errorf("a listening adapter sent %v, want no messages", sent)
		}
		television, _ := api.television("lounge")
		mustMatch(t, television.Status.PowerGeneration, int64(0))
	})
}

// No timer reads the TV's power. When a person turns the TV on or off
// with its own remote, the entry changes because of what the TV sends
// on the bus: a broadcast Standby, or a Routing Change from a TV that is
// on.
func TestTheTVsPowerFollowsItsBroadcasts(t *testing.T) {
	cases := []struct {
		name    string
		held    cec.PowerStatus
		message cec.Message
		power   string
	}{
		{"a TV turned off with its own remote", cec.PowerOn, cec.Standby(0, 15), "Standby"},
		{"a TV turned on with its own remote", cec.PowerStandby, cec.NewMessage(0, 15, cec.OpRoutingChange, 0x12, 0x00, 0x10, 0x00), "On"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithTV(televisionTV(c.held))
				api := controlling(t, wire, lounge(""))
				api.scanned(t, "node-1")
				sent := len(wire.Sent())

				wire.Send(c.message)

				api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool {
					return len(entry.Devices) > 0 && entry.Devices[0].LogicalAddress == 0 && entry.Devices[0].Power == c.power
				})
				mustMatch(t, len(wire.Sent()), sent)
			})
		})
	}
}

// A cluster without the Television CustomResourceDefinition still runs
// the bus, because the node workload treats the missing collection as
// empty.
func TestTheNodeWorkloadRunsWithoutTheTelevisionDefinition(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.noTelevisionDefinition = true
		_, device := usbAdapter(fakeCECBus())
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))

		startNode(t, api, "node-1", device)

		api.scanned(t, "node-1")
	})
}
