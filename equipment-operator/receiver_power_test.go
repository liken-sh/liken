package main

// spec.power against a restart, an upgrade, and a live change. The
// operator adopts the spec.power it finds in its first pass and sends
// nothing, and records the value in status.settledPower. A spec.power
// that differs from status.settledPower at a restart, and a change of
// spec.power it sees while it runs, go out once, and only when the
// receiver reports another power. A generation that changes another
// field, such as a session flag, sends nothing.

import (
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// switchedOn is a fake receiver that reports itself on from its first
// answer, the way a room a person is using does.
func switchedOn(t *testing.T) *fakeDenon {
	t.Helper()
	fake := startFakeDenon(t)
	fake.mutex.Lock()
	fake.power = denon.PowerOnCommand
	fake.mutex.Unlock()
	return fake
}

// asking is a Receiver that asks for a power, with the power the
// operator last settled for it.
func asking(fake *fakeDenon, power, settled equipment.Power) Receiver {
	receiver := testReceiver("theater", fake.address())
	receiver.Spec.Power = power
	receiver.Status.SettledPower = settled
	return receiver
}

// A restart at the settled power sends nothing, even when a person has
// turned the receiver on since the operator put it in standby.
func TestARestartAtTheSettledPowerSendsNoPower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := switchedOn(t)
		_, operator, log := reachedController(t, asking(fake, equipment.PowerStandby, equipment.PowerStandby), "127.0.0.1:1")

		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, "PWSTANDBY", quietPeriod)
		mustDeepEqual(t, linesWith(log, "asks power"), []string{
			"Receiver theater: generation 4 asks power Standby; the operator found it when it started, so it sent nothing",
		})
	})
}

// An upgrade meets Receivers with status.powerGeneration and no
// status.settledPower. The operator adopts the spec.power it finds when
// it starts, even one a toggle wrote before the receiver was turned on
// again: it records the value, sends nothing, and says so in one line.
// Its status apply no longer states powerGeneration, so the API server
// removes it.
//
// The old operator wrote the lowercase form, and the API server keeps
// an unchanged stored value under the new enum, so the operator reads
// it as its PascalCase value.
func TestAnUpgradeAdoptsTheSpecPower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := switchedOn(t)
		stored := asking(fake, "", "")
		stored.Status.PowerGeneration = 3
		stored.Spec.Power = decodedPower(t, `"standby"`)
		api, operator, log := reachedController(t, stored, "127.0.0.1:1")

		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, "PWSTANDBY", quietPeriod)
		mustDeepEqual(t, linesWith(log, "asks power"), []string{
			"Receiver theater: generation 4 asks power Standby; the operator found it when it started, so it sent nothing",
		})
		api.waitForStatus(t, func(status ReceiverStatus) bool { return status.SettledPower == equipment.PowerStandby })
	})
}

// A spec.power that differs from status.settledPower at a restart is an
// edit no operator settled, so it goes out once.
func TestASpecPowerEditedWhileTheOperatorWasDownSendsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		api, operator, log := reachedController(t, asking(fake, equipment.PowerOn, equipment.PowerStandby), "127.0.0.1:1")

		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, denon.PowerOnCommand)
		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, denon.PowerOnCommand, quietPeriod)
		mustDeepEqual(t, waitForLines(t, log, "asks power", 1), []string{
			"Receiver theater: generation 4 asks power On; sent power On; the receiver reported power On after <time>",
		})
		api.waitForStatus(t, func(status ReceiverStatus) bool { return status.SettledPower == equipment.PowerOn })
	})
}

// The same edit sends nothing when the receiver already reports it.
func TestASpecPowerEditedWhileTheOperatorWasDownSendsNothingTheReceiverReports(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := switchedOn(t)
		api, operator, log := reachedController(t, asking(fake, equipment.PowerOn, equipment.PowerStandby), "127.0.0.1:1")

		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, denon.PowerOnCommand, quietPeriod)
		mustDeepEqual(t, waitForLines(t, log, "asks power", 1), []string{
			"Receiver theater: generation 4 asks power On; sent nothing, because the receiver reports power On",
		})
		api.waitForStatus(t, func(status ReceiverStatus) bool { return status.SettledPower == equipment.PowerOn })
	})
}

// A spec.power that changes while the operator runs goes out once.
func TestALiveSpecPowerChangeSendsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		api, operator, _ := reachedController(t, asking(fake, "", ""), "127.0.0.1:1")

		changed := asking(fake, equipment.PowerOn, "")
		changed.Metadata.Generation = 5
		api.setReceivers(changed)
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, denon.PowerOnCommand)
		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, denon.PowerOnCommand, quietPeriod)
		api.waitForStatus(t, func(status ReceiverStatus) bool { return status.SettledPower == equipment.PowerOn })
	})
}

// A live change compares spec.power with the power the receiver
// reports, and sends nothing when they agree.
func TestALiveChangeSendsNoPowerTheReceiverReports(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := switchedOn(t)
		api, operator, log := reachedController(t, asking(fake, "", ""), "127.0.0.1:1")

		changed := asking(fake, equipment.PowerOn, "")
		changed.Metadata.Generation = 5
		api.setReceivers(changed)
		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, denon.PowerOnCommand, quietPeriod)
		mustDeepEqual(t, linesWith(log, "asks power"), []string{
			"Receiver theater: generation 5 asks power On; sent nothing, because the receiver reports power On",
		})
	})
}

// A Receiver created while the operator runs is a live change, so its
// spec.power goes out once, and the line says what the receiver
// reported.
func TestAReceiverCreatedWhileRunningSendsItsPowerOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		api := startFakeAPI(t)
		operator, log := loggedController(t, api, "127.0.0.1:1")
		mustSucceed(t, operator.pass(t.Context()))

		api.setReceivers(asking(fake, equipment.PowerOn, ""))
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForSurvey(t, operator)
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, denon.PowerOnCommand)
		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, denon.PowerOnCommand, quietPeriod)
		mustDeepEqual(t, waitForLines(t, log, "asks power", 1), []string{
			"Receiver theater: generation 4 asks power On; sent power On; the receiver reported power On after <time>",
		})
	})
}

// A new generation that changes another field is no new power. A
// spec.power the operator already applied is not sent again, although
// a person turned the receiver on after it.
func TestAnotherFieldsGenerationSendsNoPower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		api, operator, _ := reachedController(t, asking(fake, equipment.PowerStandby, equipment.PowerStandby), "127.0.0.1:1")
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, func(status ReceiverStatus) bool { return status.SettledPower == equipment.PowerStandby })
		handOnTheRemote(t, fake, denon.PowerOnCommand)
		waitForMainPower(t, operator, equipment.PowerOn)

		edited := asking(fake, equipment.PowerStandby, equipment.PowerStandby)
		edited.Metadata.Generation = 5
		edited.Metadata.Labels = map[string]string{"room": "den"}
		api.setReceivers(edited)
		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, "PWSTANDBY", quietPeriod)
	})
}

// decodedPower reads a power the way the operator reads it from an
// object.
func decodedPower(t *testing.T, raw string) equipment.Power {
	t.Helper()
	var power equipment.Power
	mustSucceed(t, json.Unmarshal([]byte(raw), &power))
	return power
}

// waitForMainPower waits until the unit reads the main zone's power.
func waitForMainPower(t *testing.T, operator *controller, power equipment.Power) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for mainZone(operator.units["theater"].driver.State()).Power != power {
		if time.Now().After(deadline) {
			t.Fatalf("the receiver never reported power %s", power)
		}
		time.Sleep(time.Millisecond)
	}
}

// A new address replaces the unit, and the new unit keeps the
// spec.power the old one settled, because a new wiring is no change of
// spec.power.
func TestANewAddressSendsNoPower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		first := startFakeDenon(t)
		api, operator, _ := reachedController(t, asking(first, equipment.PowerStandby, ""), "127.0.0.1:1")
		moved := switchedOn(t)

		api.setReceivers(asking(moved, equipment.PowerStandby, equipment.PowerStandby))
		mustSucceed(t, operator.pass(t.Context()))
		waitForMainPower(t, operator, equipment.PowerOn)
		waitForSurvey(t, operator)
		mustSucceed(t, operator.pass(t.Context()))

		moved.refuseCommand(t, "PWSTANDBY", quietPeriod)
	})
}
