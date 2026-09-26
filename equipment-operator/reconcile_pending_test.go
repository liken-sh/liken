package main

// The operator sends only the declared fields the receiver does not
// already hold: a restart against a settled receiver sends nothing, one
// differing field sends that field, a change at the receiver is driven
// back, and a field the receiver never reports is sent once per spec
// change.

import (
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
)

// settledOperator starts a controller for the receiver, waits for the
// survey, has the receiver report the lines the test names, and waits
// until the operator has folded them.
func settledOperator(t *testing.T, fake *fakeDenon, receiver Receiver, reports []string, folded func(*controller) bool) (*fakeAPI, *controller) {
	t.Helper()
	api := startFakeAPI(t)
	api.setReceivers(receiver)
	operator := startController(t, api)
	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)
	waitForSurvey(t, operator)
	fake.volunteer(reports...)
	waitFor(t, func() bool { return folded(operator) })
	return api, operator
}

// The settings and zones the tests declare, and the lines a receiver
// that already holds them reports. The fake reports PSDRC OFF and
// PSLFE 00 by itself after MS?.
func settledReceiver(fake *fakeDenon) Receiver {
	receiver := testReceiver("theater", fake.address())
	drc, lfe, control, volume := "off", 0, true, 40.0
	receiver.Spec.Denon.Settings = denon.Settings{
		Audio: denon.AudioSettings{DRC: &drc, LFE: &lfe},
		HDMI:  denon.HDMISettings{Control: &control},
	}
	receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Input: "PHONO", Volume: &volume}}
	return receiver
}

var settledReports = []string{"SSHOSCON ON", "Z2PHONO", "Z240"}

// settledFolded answers whether the operator has read every declared
// value back from the receiver.
func settledFolded(operator *controller) bool {
	unit := operator.units["theater"]
	s := unit.denonClient.Settings()
	zone, reported := unit.driver.State().Zone("zone2")
	return s.Audio.DRC != nil && s.Audio.LFE != nil && s.HDMI.Control != nil &&
		reported && zone.Input == "PHONO" && zone.Volume == 80
}

func TestARestartAgainstASettledReceiverSendsNothing(t *testing.T) {
	fake := startFakeDenon(t)
	_, operator := settledOperator(t, fake, settledReceiver(fake), settledReports, settledFolded)

	mustSucceed(t, operator.pass(t.Context()))
	mustSucceed(t, operator.pass(t.Context()))

	fake.refuseAnySet(t, quietPeriod)
}

func TestOneDifferingFieldSendsOnlyThatField(t *testing.T) {
	cases := []struct {
		name    string
		reports []string
		want    string
	}{
		{"a setting", []string{"SSHOSCON ON", "Z2PHONO", "Z240", "PSDRC LOW"}, "PSDRC OFF"},
		{"an HDMI setting", []string{"SSHOSCON OFF", "Z2PHONO", "Z240"}, "SSHOSCON ON"},
		{"a zone control", []string{"SSHOSCON ON", "Z2CD", "Z240"}, "Z2PHONO"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			fake := startFakeDenon(t)
			_, operator := settledOperator(t, fake, settledReceiver(fake), one.reports, func(operator *controller) bool {
				unit := operator.units["theater"]
				zone, reported := unit.driver.State().Zone("zone2")
				return unit.denonClient.Settings().HDMI.Control != nil && reported && zone.Volume == 80
			})

			mustSucceed(t, operator.pass(t.Context()))
			sent := fake.waitForCommands(t, one.want)
			for _, command := range sent[:len(sent)-1] {
				if !strings.HasSuffix(command, "?") && (isDenonSetting(command) || strings.HasPrefix(command, "Z2")) {
					t.Fatalf("the operator sent %q beside %q", command, one.want)
				}
			}
		})
	}
}

// A change a person makes at the receiver differs from the spec, so
// the next pass sends the declared value back.
func TestAChangeAtTheReceiverIsDrivenBack(t *testing.T) {
	fake := startFakeDenon(t)
	_, operator := settledOperator(t, fake, settledReceiver(fake), settledReports, settledFolded)
	mustSucceed(t, operator.pass(t.Context()))
	fake.refuseAnySet(t, quietPeriod)

	fake.volunteer("PSDRC LOW", "Z230")
	waitForObservedSettings(t, operator, "theater", func(s denon.Settings) bool {
		return s.Audio.DRC != nil && *s.Audio.DRC == "low"
	})
	waitForObservedZone(t, operator, "theater", "zone2", 60)

	mustSucceed(t, operator.pass(t.Context()))
	fake.waitForCommands(t, "PSDRC OFF")
	fake.waitForCommands(t, "Z2MV40")
}

// A field the receiver never reports cannot be compared, so it is not
// sent after a restart, it is sent once when the spec changes it, and
// it is not sent again while the spec stands.
func TestAnUnreportedFieldIsSentOncePerSpecChange(t *testing.T) {
	fake := startFakeDenon(t)
	receiver := testReceiver("theater", fake.address())
	eco, sleep := "on", 30
	receiver.Spec.Denon.Settings = denon.Settings{System: denon.SystemSettings{Eco: &eco}}
	receiver.Spec.Zones = map[string]ZoneSpec{"zone3": {Sleep: &sleep}}
	api, operator := settledOperator(t, fake, receiver, nil, func(*controller) bool { return true })

	mustSucceed(t, operator.pass(t.Context()))
	fake.refuseAnySet(t, quietPeriod)

	eco, sleep = "off", 60
	receiver.Spec.Denon.Settings = denon.Settings{System: denon.SystemSettings{Eco: &eco}}
	receiver.Spec.Zones = map[string]ZoneSpec{"zone3": {Sleep: &sleep}}
	api.setReceivers(receiver)
	mustSucceed(t, operator.pass(t.Context()))
	fake.waitForCommands(t, "ECOOFF")
	fake.waitForCommands(t, "Z3SLP060")

	mustSucceed(t, operator.pass(t.Context()))
	fake.refuseAnySet(t, quietPeriod)
}

// waitFor polls the check until it passes, and fails the test instead
// of hanging when it never does.
func waitFor(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.After(testTimeout)
	for !check() {
		select {
		case <-deadline:
			t.Fatal("the condition never held")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// A field the receiver takes but never reports at the declared value is
// sent three times in one spec generation, then the operator stops and
// the status names the field as not confirmed. A spec change starts the
// count again.
func TestAFieldTheReceiverNeverConfirmsStopsAfterThreeSends(t *testing.T) {
	fake := startFakeDenon(t)
	receiver := testReceiver("theater", fake.address())
	arc := true
	receiver.Spec.Denon.Settings = denon.Settings{HDMI: denon.HDMISettings{ARC: &arc}}
	fake.holdSetting("SSHOSCONARC ON", "SSHOSCONARC OFF")
	api, operator := settledOperator(t, fake, receiver, []string{"SSHOSCONARC OFF"}, func(operator *controller) bool {
		return operator.units["theater"].denonClient.Settings().HDMI.ARC != nil
	})

	for range sendLimit {
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "SSHOSCONARC ON")
	}
	mustSucceed(t, operator.pass(t.Context()))
	fake.refuseAnySet(t, quietPeriod)

	status := api.waitForStatus(t, func(status ReceiverStatus) bool { return len(status.Conditions) == 2 })
	mustMatch(t, status.Conditions[1], Condition{
		Type:               settingsConfirmedConditionType,
		Status:             ConditionFalse,
		ObservedGeneration: receiver.Metadata.Generation,
		Reason:             reasonNotConfirmed,
		Message:            "the receiver did not report the declared value after 3 sends: spec.denon.settings.hdmi.arc",
		LastTransitionTime: timestamp(statusNow),
	})

	receiver.Metadata.Generation++
	api.setReceivers(receiver)
	mustSucceed(t, operator.pass(t.Context()))
	fake.waitForCommands(t, "SSHOSCONARC ON")
}

// A zone control the zone never reports at the declared value stops
// after three sends the same way.
func TestAZoneControlTheReceiverNeverConfirmsStopsAfterThreeSends(t *testing.T) {
	fake := startFakeDenon(t)
	receiver := testReceiver("theater", fake.address())
	volume := 40.0
	receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Volume: &volume}}
	fake.holdSetting("Z2MV40", "Z230")
	api, operator := settledOperator(t, fake, receiver, []string{"Z230"}, func(operator *controller) bool {
		zone, reported := operator.units["theater"].driver.State().Zone("zone2")
		return reported && zone.Volume == 60
	})

	for range sendLimit {
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "Z2MV40")
	}
	mustSucceed(t, operator.pass(t.Context()))
	fake.refuseAnySet(t, quietPeriod)

	status := api.waitForStatus(t, func(status ReceiverStatus) bool { return len(status.Conditions) == 2 })
	mustMatch(t, status.Conditions[1].Message, "the receiver did not report the declared value after 3 sends: spec.zones.zone2.volume")
}
