package main

// The operator sends only the declared fields the receiver does not
// already hold: a restart against a settled receiver sends nothing, one
// differing field sends that field, a change at the receiver is driven
// back, and a field the receiver never reports is sent once per spec
// change.

import (
	"maps"
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
	t.Parallel()
	fake := startFakeDenon(t)
	_, operator := settledOperator(t, fake, settledReceiver(fake), settledReports, settledFolded)

	mustSucceed(t, operator.pass(t.Context()))
	mustSucceed(t, operator.pass(t.Context()))

	fake.refuseAnySet(t, quietPeriod)
}

func TestOneDifferingFieldSendsOnlyThatField(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
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
	t.Parallel()
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

// unreportedReceiver declares a setting, a channel volume, and a zone
// control the fake receiver never reports.
func unreportedReceiver(fake *fakeDenon) Receiver {
	receiver := testReceiver("theater", fake.address())
	eco, sleep := "on", 30
	receiver.Spec.Denon.Settings = denon.Settings{
		System:         denon.SystemSettings{Eco: &eco},
		ChannelVolumes: map[string]float64{"SW": 1.5},
	}
	receiver.Spec.Zones = map[string]ZoneSpec{"zone3": {Sleep: &sleep}}
	return receiver
}

// recordedBlocks is the status.settledSettings an operator writes after
// it sent a Receiver's declared blocks.
func recordedBlocks(receiver Receiver) map[string]string {
	return map[string]string{
		denonSettingsBlock: digest(receiver.Spec.Denon.Settings),
		zonesBlock:         digest(receiver.Spec.Zones),
	}
}

// A field the receiver never reports cannot be compared, so after a
// restart the operator sends it only when its block differs from the
// block status.settledSettings records: a new Receiver and a block
// edited while the operator was down send it once. A restart with the
// recorded blocks sends nothing, whatever metadata.generation says, and
// so does an upgrade that finds the earlier operator's
// status.settingsGeneration and no status.settledSettings.
func TestAnUnreportedFieldIsSentOnlyForABlockTheStatusDoesNotRecord(t *testing.T) {
	t.Parallel()
	edited := func(receiver *Receiver) {
		receiver.Status.SettledSettings = map[string]string{
			denonSettingsBlock: digest(denon.Settings{}),
			zonesBlock:         digest(map[string]ZoneSpec(nil)),
		}
	}
	cases := []struct {
		name  string
		store func(*Receiver)
	}{
		{"a new Receiver", func(*Receiver) {}},
		{"blocks edited while the operator was down", edited},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			fake := startFakeDenon(t)
			receiver := unreportedReceiver(fake)
			one.store(&receiver)
			api, operator := settledOperator(t, fake, receiver, nil, func(*controller) bool { return true })

			mustSucceed(t, operator.pass(t.Context()))
			fake.waitForCommands(t, "ECOON")
			fake.waitForCommands(t, "Z3SLP030")
			api.waitForStatus(t, func(status ReceiverStatus) bool {
				return maps.Equal(status.SettledSettings, recordedBlocks(receiver))
			})

			mustSucceed(t, operator.pass(t.Context()))
			fake.refuseAnySet(t, quietPeriod)
		})
	}
}

func TestARestartSendsNoUnreportedFieldTheStatusRecords(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		store func(*Receiver)
	}{
		{"the recorded blocks", func(receiver *Receiver) { receiver.Status.SettledSettings = recordedBlocks(*receiver) }},
		{"the recorded blocks at a later generation", func(receiver *Receiver) {
			receiver.Status.SettledSettings = recordedBlocks(*receiver)
			receiver.Metadata.Generation = 9
		}},
		{"an upgrade at the recorded generation", func(receiver *Receiver) { receiver.Status.SettingsGeneration = 4 }},
		{"an upgrade after a session flag moved the generation", func(receiver *Receiver) { receiver.Status.SettingsGeneration = 3 }},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			fake := startFakeDenon(t)
			receiver := unreportedReceiver(fake)
			one.store(&receiver)
			api, operator := settledOperator(t, fake, receiver, nil, func(*controller) bool { return true })

			mustSucceed(t, operator.pass(t.Context()))
			mustSucceed(t, operator.pass(t.Context()))

			fake.refuseAnySet(t, quietPeriod)
			mustDeepEqual(t, api.lastStatus().SettledSettings, recordedBlocks(receiver))
		})
	}
}

// While the operator runs, a spec change that changes an unreported
// field sends that field once, and a spec change that leaves it alone
// does not send it.
func TestAnUnreportedFieldIsSentOncePerSpecChange(t *testing.T) {
	t.Parallel()
	fake := startFakeDenon(t)
	receiver := unreportedReceiver(fake)
	receiver.Status.SettledSettings = recordedBlocks(receiver)
	api, operator := settledOperator(t, fake, receiver, nil, func(*controller) bool { return true })

	mustSucceed(t, operator.pass(t.Context()))
	fake.refuseAnySet(t, quietPeriod)

	receiver.Metadata.Generation = 5
	receiver.Spec.Volume = &ReceiverVolume{Max: 60}
	api.setReceivers(receiver)
	mustSucceed(t, operator.pass(t.Context()))
	fake.refuseAnySet(t, quietPeriod)

	eco, sleep := "off", 60
	receiver.Metadata.Generation = 6
	receiver.Spec.Denon.Settings = denon.Settings{System: denon.SystemSettings{Eco: &eco}}
	receiver.Spec.Zones = map[string]ZoneSpec{"zone3": {Sleep: &sleep}}
	api.setReceivers(receiver)
	mustSucceed(t, operator.pass(t.Context()))
	fake.waitForCommands(t, "ECOOFF")
	fake.waitForCommands(t, "Z3SLP060")

	mustSucceed(t, operator.pass(t.Context()))
	fake.refuseAnySet(t, quietPeriod)
}

// A field the receiver takes but never reports at the declared value is
// sent three times at that value, then the operator stops and the
// status names the field as not confirmed. A new metadata.generation
// that leaves the field alone does not start the count again. A report
// of the declared value does, so a later change at the receiver is
// driven back.
func TestAFieldTheReceiverNeverConfirmsStopsAfterThreeSends(t *testing.T) {
	t.Parallel()
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
	fake.refuseAnySet(t, quietPeriod)

	fake.volunteer("SSHOSCONARC ON")
	waitForObservedSettings(t, operator, "theater", func(s denon.Settings) bool { return *s.HDMI.ARC })
	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, func(status ReceiverStatus) bool { return len(status.Conditions) == 1 })
	fake.volunteer("SSHOSCONARC OFF")
	waitForObservedSettings(t, operator, "theater", func(s denon.Settings) bool { return !*s.HDMI.ARC })
	mustSucceed(t, operator.pass(t.Context()))
	fake.waitForCommands(t, "SSHOSCONARC ON")
}

// A zone control the zone never reports at the declared value stops
// after three sends the same way.
func TestAZoneControlTheReceiverNeverConfirmsStopsAfterThreeSends(t *testing.T) {
	t.Parallel()
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
