package main

// The operator sends spec.power once for each change, and only when the
// receiver reports a different power. When it starts, it adopts the
// spec.power it finds and records it in status.settledPower, unless the
// value differs from status.settledPower, which makes it a change.

import (
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// switchedOn starts a fake receiver that reports power on.
func switchedOn(t *testing.T) *fakeDenon {
	t.Helper()
	fake := startFakeDenon(t)
	fake.mutex.Lock()
	fake.power = denon.PowerOnCommand
	fake.mutex.Unlock()
	return fake
}

func receiverDeclaring(fake *fakeDenon, power, settled equipment.Power) Receiver {
	receiver := testReceiver("theater", fake.address())
	receiver.Spec.Power = power
	receiver.Status.SettledPower = settled
	return receiver
}

// A restart with spec.power equal to status.settledPower sends nothing,
// even when a person turned the receiver on after the operator set it
// to standby.
func TestARestartAtTheSettledPowerSendsNoPower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := switchedOn(t)
		_, operator, log := reachedController(t, receiverDeclaring(fake, equipment.PowerStandby, equipment.PowerStandby))

		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, "PWSTANDBY", quietPeriod)
		mustDeepEqual(t, linesWith(log, "requests power"), []string{
			"Receiver theater: generation 4 requests power Standby; the operator found it when it started, so it sent nothing",
		})
	})
}

// A Receiver written by an earlier operator version has
// status.powerGeneration, no status.settledPower, and possibly a
// lowercase spec.power. The operator adopts that spec.power, sends
// nothing, and records it in status.settledPower.
func TestAnUpgradeAdoptsTheSpecPower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := switchedOn(t)
		stored := receiverDeclaring(fake, "", "")
		stored.Status.PowerGeneration = 3
		stored.Spec.Power = decodedPower(t, `"standby"`)
		api, operator, log := reachedController(t, stored)

		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, "PWSTANDBY", quietPeriod)
		mustDeepEqual(t, linesWith(log, "requests power"), []string{
			"Receiver theater: generation 4 requests power Standby; the operator found it when it started, so it sent nothing",
		})
		api.waitForStatus(t, func(status ReceiverStatus) bool { return status.SettledPower == equipment.PowerStandby })
	})
}

// After a restart, a spec.power that differs from status.settledPower
// was edited while the operator was down, so the operator sends it
// once.
func TestASpecPowerEditedWhileTheOperatorWasDownSendsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		api, operator, log := reachedController(t, receiverDeclaring(fake, equipment.PowerOn, equipment.PowerStandby))

		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, denon.PowerOnCommand)
		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, denon.PowerOnCommand, quietPeriod)
		mustDeepEqual(t, waitForLines(t, log, "requests power", 1), []string{
			"Receiver theater: generation 4 requests power On; sent power On; the receiver reported power On after <time>",
		})
		api.waitForStatus(t, func(status ReceiverStatus) bool { return status.SettledPower == equipment.PowerOn })
	})
}

func TestASpecPowerEditedWhileTheOperatorWasDownSendsNothingTheReceiverReports(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := switchedOn(t)
		api, operator, log := reachedController(t, receiverDeclaring(fake, equipment.PowerOn, equipment.PowerStandby))

		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, denon.PowerOnCommand, quietPeriod)
		mustDeepEqual(t, waitForLines(t, log, "requests power", 1), []string{
			"Receiver theater: generation 4 requests power On; sent nothing, because the receiver reports power On",
		})
		api.waitForStatus(t, func(status ReceiverStatus) bool { return status.SettledPower == equipment.PowerOn })
	})
}

func TestALiveSpecPowerChangeSendsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		api, operator, _ := reachedController(t, receiverDeclaring(fake, "", ""))

		changed := receiverDeclaring(fake, equipment.PowerOn, "")
		changed.Metadata.Generation = 5
		api.setReceivers(changed)
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, denon.PowerOnCommand)
		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, denon.PowerOnCommand, quietPeriod)
		api.waitForStatus(t, func(status ReceiverStatus) bool { return status.SettledPower == equipment.PowerOn })
	})
}

func TestALiveChangeSendsNoPowerTheReceiverReports(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := switchedOn(t)
		api, operator, log := reachedController(t, receiverDeclaring(fake, "", ""))

		changed := receiverDeclaring(fake, equipment.PowerOn, "")
		changed.Metadata.Generation = 5
		api.setReceivers(changed)
		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, denon.PowerOnCommand, quietPeriod)
		mustDeepEqual(t, linesWith(log, "requests power"), []string{
			"Receiver theater: generation 5 requests power On; sent nothing, because the receiver reports power On",
		})
	})
}

// A Receiver created while the operator runs counts as a change of
// spec.power.
func TestAReceiverCreatedWhileRunningSendsItsPowerOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		api := startFakeAPI(t)
		operator, log := loggedController(t, api)
		mustSucceed(t, operator.pass(t.Context()))

		api.setReceivers(receiverDeclaring(fake, equipment.PowerOn, ""))
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForFirstRead(t, operator)
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, denon.PowerOnCommand)
		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, denon.PowerOnCommand, quietPeriod)
		mustDeepEqual(t, waitForLines(t, log, "requests power", 1), []string{
			"Receiver theater: generation 4 requests power On; sent power On; the receiver reported power On after <time>",
		})
	})
}

// An edit of another field does not resend a spec.power that the
// operator already applied, even when a person has since turned the
// receiver on.
func TestAnotherFieldsGenerationSendsNoPower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		api, operator, _ := reachedController(t, receiverDeclaring(fake, equipment.PowerStandby, equipment.PowerStandby))
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, func(status ReceiverStatus) bool { return status.SettledPower == equipment.PowerStandby })
		handOnTheRemote(t, fake, denon.PowerOnCommand)
		waitForMainPower(t, operator, equipment.PowerOn)

		edited := receiverDeclaring(fake, equipment.PowerStandby, equipment.PowerStandby)
		edited.Metadata.Generation = 5
		edited.Metadata.Labels = map[string]string{"room": "den"}
		api.setReceivers(edited)
		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, "PWSTANDBY", quietPeriod)
	})
}

func decodedPower(t *testing.T, raw string) equipment.Power {
	t.Helper()
	var power equipment.Power
	mustSucceed(t, json.Unmarshal([]byte(raw), &power))
	return power
}

// waitForMainPower waits until the driver has read the main zone's
// power.
func waitForMainPower(t *testing.T, operator *controller, power equipment.Power) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for mainZone(operator.units["theater"].driver.State()).Power != power {
		if time.Now().After(deadline) {
			t.Fatalf("the receiver did not report main zone power %s before the timeout", power)
		}
		time.Sleep(time.Millisecond)
	}
}

// A new address replaces the unit, and the new unit does not resend
// the spec.power that the old one settled.
func TestANewAddressSendsNoPower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		first := startFakeDenon(t)
		api, operator, _ := reachedController(t, receiverDeclaring(first, equipment.PowerStandby, ""))
		moved := switchedOn(t)

		api.setReceivers(receiverDeclaring(moved, equipment.PowerStandby, equipment.PowerStandby))
		mustSucceed(t, operator.pass(t.Context()))
		waitForMainPower(t, operator, equipment.PowerOn)
		waitForFirstRead(t, operator)
		mustSucceed(t, operator.pass(t.Context()))

		moved.refuseCommand(t, "PWSTANDBY", quietPeriod)
	})
}
