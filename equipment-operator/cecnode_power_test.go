package main

// Tests of how the node workload applies `spec.power` after a change, a
// failure, or a restart. The fixtures are in
// `cecnode_television_test.go`.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// stubbornTV is a TV in standby that drops every power command.
func stubbornTV() cectest.Peer {
	tv := televisionTV(cec.PowerStandby)
	tv.Ignore = 1000
	return tv
}

// The node workload applies each generation once. When a person then
// turns the TV off with its own remote, neither this node workload nor
// a new one sends the command again.
func TestAnAppliedGenerationIsNotSentAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
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

func TestANewGenerationIsApplied(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
		api := controlling(t, wire, lounge(TelevisionOn))
		appliedAt(t, api, "lounge", 1)

		api.putTelevision(lounge(TelevisionStandby))

		appliedAt(t, api, "lounge", 2)
		peer, _ := wire.Peer(0)
		mustMatch(t, peer.Power, cec.PowerStandby)
		mustCommandTheTVAlone(t, wire)
	})
}

// Every spec edit is a new generation, so a person can send the same
// power value again by clearing `spec.power` and setting it back.
func TestSettingTheSamePowerAgainAfterClearingItSendsTheCommandAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
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

// A `Television` that is deleted and created again under the same name
// has a new uid, so the node workload applies its `spec.power` even
// when the generation equals the old object's.
func TestARecreatedTelevisionIsApplied(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
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

// When the API server refuses the write of a result, the node workload
// writes it again and never sends the command again, neither on the
// next pass nor after a person turns the TV off with its own remote.
func TestARefusedResultWriteIsRetriedWithoutSendingTheCommandAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
		api := startCECAPI(t)
		api.refusePowerWrites(true)
		_, device := usbAdapter(wire)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		api.putTelevision(lounge(TelevisionOn))
		startNode(t, api, "node-1", device)
		// The application ends within a few reads after the entry shows
		// the TV on.
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

// A generation gets at most cecPowerSends commands, even when the API
// server refuses the result and the bus leaves Control and returns
// during the application.
func TestAGenerationGetsAtMostCecPowerSendsCommands(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(stubbornTV())
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
// application sends its command without reading the power first. A TV
// reports its old state for a while after a command, so a read then
// would find the TV in standby, send nothing, and leave it on.
func TestANewGenerationDuringAnApplicationSendsItsCommandWithoutReading(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		lagging := televisionTV(cec.PowerStandby)
		lagging.Lag = 30
		wire := busWithTV(lagging)
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

// A cancelled application sends nothing more. Here the bus leaves
// Control during the application.
func TestACancelledApplicationSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(stubbornTV())
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

// An adapter that is unplugged during an application ends the node
// workload, and the application writes nothing, so the next pod for the
// adapter applies the generation.
func TestAnUnplugDuringAnApplicationEndsTheNodeWorkload(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(stubbornTV())
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

// When a `Television` is deleted during an application, the next pass
// finds no `Television` for the bus and ends the application, so the TV
// gets no more commands.
func TestATelevisionDeletedDuringAnApplicationEndsIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(stubbornTV())
		api := controlling(t, wire, lounge(TelevisionOn))
		api.waitUntil(t, "the first Image View On", func() bool { return sentOf(wire, cec.OpImageViewOn) > 0 })

		api.removeTelevision("lounge")
		time.Sleep(2 * cecPowerWindow)
		ended := sentOf(wire, cec.OpImageViewOn)
		time.Sleep(time.Duration(cecPowerSends) * cecPowerWindow)

		mustMatch(t, sentOf(wire, cec.OpImageViewOn), ended)
		if ended >= cecPowerSends {
			t.Errorf("the application sent %d commands, want fewer than %d", ended, cecPowerSends)
		}
	})
}

// Of two `Television`s on one bus, the node workload applies only the
// one that is in charge. When that one is deleted, the other takes
// over, and the node workload applies its `spec.power` once, as for any
// new object.
func TestATelevisionThatIsNotInChargeIsNotApplied(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
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

// The `Deployment` writes `status.session`. A status write is not a
// spec edit, so it does not change the generation or send a power
// command, and a TV that a person turned off with its own remote stays
// off.
func TestASessionWriteSendsNoPowerCommand(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
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
