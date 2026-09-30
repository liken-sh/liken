package main

// The node workload's application of spec.power when it meets a
// change, a failure, or a restart. The rule under test: the node
// workload applies each generation of each Television once, sends at
// most three commands for it, and records it in status.powerGeneration.
// cecnode_television_test.go holds the fixtures.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// stubbornTV is a TV in standby that drops every power command.
func stubbornTV() cectest.Peer {
	tv := televisionTV(cec.PowerStandby)
	tv.Ignore = 1000
	return tv
}

// The operator applies each generation once. A person who then turns
// the TV off with its own remote is not overruled, and a new node
// workload does not send the command again.
func TestThePowerIsNotAssertedAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(televisionTV(cec.PowerStandby))
		api := controlling(t, wire, lounge(TelevisionOn))
		appliedAt(t, api, "lounge", 1)

		wire.Add(televisionTV(cec.PowerStandby))
		_, second := usbAdapter(wire)
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-2", Display: "acm-0001-receiver"}, CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		startNode(t, api, "node-2", second)
		api.scanned(t, "node-2")
		api.nudge()
		time.Sleep(10 * cecPowerWindow)

		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
		peer, _ := wire.Peer(0)
		mustMatch(t, peer.Power, cec.PowerStandby)
	})
}

func TestANewChangeIsApplied(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(televisionTV(cec.PowerStandby))
		api := controlling(t, wire, lounge(TelevisionOn))
		appliedAt(t, api, "lounge", 1)

		api.putTelevision(lounge(TelevisionStandby))

		appliedAt(t, api, "lounge", 2)
		peer, _ := wire.Peer(0)
		mustMatch(t, peer.Power, cec.PowerStandby)
		mustCommandTheTVAlone(t, wire)
	})
}

// Every spec edit is a new generation, so a person asks for the same
// value again by removing the field and adding it back.
func TestAPersonRetriesTheSameValueWithAnEdit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(televisionTV(cec.PowerStandby))
		api := controlling(t, wire, lounge(TelevisionOn))
		appliedAt(t, api, "lounge", 1)
		wire.Add(televisionTV(cec.PowerStandby))

		api.putTelevision(lounge(""))
		api.putTelevision(lounge(TelevisionOn))

		appliedAt(t, api, "lounge", 3)
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 2)
		peer, _ := wire.Peer(0)
		mustMatch(t, peer.Power, cec.PowerOn)
	})
}

// A Television deleted and created again under the same name is a new
// object with a new uid, so its spec.power is applied at once, even at
// the same generation as the old object's.
func TestARecreatedTelevisionIsANewObject(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(televisionTV(cec.PowerStandby))
		api := controlling(t, wire, lounge(TelevisionOn))
		appliedAt(t, api, "lounge", 1)

		api.removeTelevision("lounge")
		api.putTelevision(lounge(TelevisionStandby))

		appliedAt(t, api, "lounge", 1)
		api.waitUntil(t, "the Standby", func() bool { return sentOf(wire, cec.OpStandby) == 1 })
		peer, _ := wire.Peer(0)
		mustMatch(t, peer.Power, cec.PowerStandby)
		mustCommandTheTVAlone(t, wire)
	})
}

// A result the API server refuses is written again, and the command is
// never sent again: not on the next pass, and not after a person turns
// the TV off with its own remote.
func TestARefusedWriteIsRetriedWithoutTheCommand(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(televisionTV(cec.PowerStandby))
		api := startCECAPI(t)
		api.refusePowerWrites(true)
		_, device := usbAdapter(wire)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		api.putTelevision(lounge(TelevisionOn))
		startNode(t, api, "node-1", device)
		// The entry shows On once a read confirmed it, and the application
		// reads every cecPowerReadEvery, so a few of those later it is done.
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool {
			return len(entry.Devices) > 0 && entry.Devices[0].LogicalAddress == 0 && entry.Devices[0].Power == "On"
		})
		time.Sleep(4 * cecPowerReadEvery)

		wire.Add(televisionTV(cec.PowerStandby))
		for range 5 {
			api.nudge()
			time.Sleep(2 * cecPowerWindow)
		}
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
		api.refusePowerWrites(false)
		api.nudge()

		appliedAt(t, api, "lounge", 1)
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
	})
}

// A generation gets at most three commands, whatever happens: a result
// the API server refuses, and a bus that leaves Control and comes back
// during the application.
func TestAGenerationGetsAtMostThreeCommands(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(stubbornTV())
		api := startCECAPI(t)
		api.refusePowerWrites(true)
		_, device := usbAdapter(wire)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		control := controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"})
		api.putBus(control)
		api.putTelevision(lounge(TelevisionOn))
		startNode(t, api, "node-1", device)
		api.waitUntil(t, "the first Image View On", func() bool { return sentOf(wire, cec.OpImageViewOn) == 1 })

		api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
		api.putBus(control)
		for range 10 {
			api.nudge()
			time.Sleep(2 * cecPowerWindow)
		}

		mustMatch(t, sentOf(wire, cec.OpImageViewOn), cecPowerSends)
	})
}

// A new generation during an application cancels it, and the new
// application sends its command without a read first. Right after a
// command a TV answers its old state for a while, so a read then would
// find the TV already in standby, send nothing, and leave it on.
func TestANewGenerationDuringAnApplicationSendsItsCommand(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		lagging := televisionTV(cec.PowerStandby)
		lagging.Lag = 30
		wire := roomWithTV(lagging)
		api := controlling(t, wire, lounge(TelevisionOn))
		api.waitUntil(t, "the Image View On", func() bool { return sentOf(wire, cec.OpImageViewOn) == 1 })

		api.putTelevision(lounge(TelevisionStandby))

		appliedAt(t, api, "lounge", 2)
		api.waitUntil(t, "the TV in standby", func() bool {
			peer, _ := wire.Peer(0)
			return peer.Power == cec.PowerStandby
		})
		if sentOf(wire, cec.OpStandby) == 0 {
			t.Error("the new generation sent no Standby")
		}
		mustCommandTheTVAlone(t, wire)
	})
}

// An application the node workload cancels sends nothing more. Here
// the bus leaves Control during it.
func TestACancelledApplicationSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(stubbornTV())
		api := controlling(t, wire, lounge(TelevisionOn))
		api.waitUntil(t, "the first Image View On", func() bool { return sentOf(wire, cec.OpImageViewOn) == 1 })

		api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
		time.Sleep(time.Duration(cecPowerSends+2) * cecPowerWindow)

		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
		television, _ := api.television("lounge")
		mustMatch(t, television.Status.PowerGeneration, int64(0))
	})
}

// An adapter unplugged during an application ends the node workload,
// and the application writes nothing, so the adapter's next pod
// applies the generation.
func TestAnUnplugDuringAnApplicationEndsTheNodeWorkload(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(stubbornTV())
		api := startCECAPI(t)
		adapter, device := usbAdapter(wire)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		api.putTelevision(lounge(TelevisionOn))
		done := startNode(t, api, "node-1", device)
		api.waitUntil(t, "the first Image View On", func() bool { return sentOf(wire, cec.OpImageViewOn) > 0 })

		adapter.Unplug()

		select {
		case err := <-done:
			if !cec.IsGone(err) {
				t.Errorf("ended with %v, want ENODEV", err)
			}
		case <-time.After(testTimeout):
			t.Fatal("the node workload did not end")
		}
		television, _ := api.television("lounge")
		mustMatch(t, television.Status.PowerGeneration, int64(0))
	})
}

// A Television deleted during an application ends the application:
// the pass finds no Television for the bus, and the TV gets no more
// commands.
func TestATelevisionDeletedDuringAnApplicationEndsIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(stubbornTV())
		api := controlling(t, wire, lounge(TelevisionOn))
		api.waitUntil(t, "the first Image View On", func() bool { return sentOf(wire, cec.OpImageViewOn) > 0 })

		api.removeTelevision("lounge")
		time.Sleep(2 * cecPowerWindow)
		ended := sentOf(wire, cec.OpImageViewOn)
		time.Sleep(time.Duration(cecPowerSends) * cecPowerWindow)

		mustMatch(t, sentOf(wire, cec.OpImageViewOn), ended)
		if ended >= cecPowerSends {
			t.Errorf("the application ran to its bound: %d commands", ended)
		}
	})
}

// Of two Televisions on one bus, the node workload applies only the
// one in charge. When that one is deleted, the other takes over, and
// its spec.power is applied once, as a new object's is.
func TestTheTelevisionNotInChargeWaits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(televisionTV(cec.PowerStandby))
		discovered := discoveredTV("den")
		discovered.Spec.Power = TelevisionOn
		api := controlling(t, wire, lounge(""))
		api.putTelevision(discovered)
		api.scanned(t, "node-1")
		api.nudge()
		time.Sleep(4 * cecPowerWindow)
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)

		api.removeTelevision("lounge")

		appliedAt(t, api, "den", 1)
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
	})
}

// The Deployment writes status.session, and a status write is no spec
// edit, so it asks nothing of the TV's power: a TV a person turned off
// with its own remote stays off.
func TestASessionWriteAppliesNoPower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(televisionTV(cec.PowerStandby))
		api := controlling(t, wire, lounge(TelevisionOn))
		appliedAt(t, api, "lounge", 1)
		wire.Add(televisionTV(cec.PowerStandby))

		asleep := wokeNow()
		asleep.Awake = false
		mustSucceed(t, ApplyTelevisionSession(api.client, "lounge", asleep))
		time.Sleep(4 * cecPowerWindow)

		television, _ := api.television("lounge")
		mustMatch(t, television.Metadata.Generation, int64(1))
		mustDeepEqual(t, television.Status.Session, asleep)
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
	})
}
