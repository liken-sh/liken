package main

// The asks in status.session against the fake Denon: each new at sends
// once, an ask the first pass finds sends nothing, and volume asks that
// arrive faster than the receiver reports leave only the newest. The
// sessions here name no topic, so they set the level only from the
// asks and open no broker connection.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
)

// topiclessReceiver is a Denon with an idle session in status.session
// that names no volume topic and no power topic.
func topiclessReceiver(address string) Receiver {
	held := testReceiver("theater", address)
	held.Spec.Volume = &ReceiverVolume{Max: 69.5, Step: 0.5}
	held.Status.Session = &ReceiverSession{Player: "house/theater", Input: "GAME"}
	return held
}

// askAt is the at of the nth ask, with milliseconds, as the media
// operator writes it.
func askAt(n int) string {
	return fmt.Sprintf("2026-10-04T12:15:25.%03dZ", n)
}

// The ask helpers copy the session, because the fake API encodes the
// Receiver a test set while the test builds the next one.
func askingVolume(receiver Receiver, level float64, at string) Receiver {
	session := *receiver.Status.Session
	session.VolumeAsk = &ReceiverVolumeAsk{Level: level, At: at}
	receiver.Status.Session = &session
	return receiver
}

func askingPower(receiver Receiver, action, at string) Receiver {
	session := *receiver.Status.Session
	session.PowerAsk = &ReceiverPowerAsk{Action: action, At: at}
	receiver.Status.Session = &session
	return receiver
}

func askingInput(receiver Receiver, action, at string) Receiver {
	session := *receiver.Status.Session
	session.InputAsk = &ReceiverInputAsk{Action: action, At: at}
	receiver.Status.Session = &session
	return receiver
}

// passWith sets the one Receiver and runs a pass on it.
func passWith(t *testing.T, api *fakeAPI, operator *controller, receiver Receiver) {
	t.Helper()
	api.setReceivers(receiver)
	mustSucceed(t, operator.pass(t.Context()))
}

func TestANewVolumeAskSendsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		receiver := topiclessReceiver(fake.address())
		api, operator, _ := reachedController(t, receiver, "127.0.0.1:1")

		asked := askingVolume(receiver, 44.5, askAt(1))
		passWith(t, api, operator, asked)
		fake.waitForCommands(t, "MV445")
		passWith(t, api, operator, asked)

		fake.refuseEveryCommand(t, quietPeriod)
	})
}

// firstPassAsks are one ask of each kind, each of which sends a command
// to a receiver that is on and reports another input than GAME.
var firstPassAsks = []struct {
	name string
	ask  func(Receiver) Receiver
}{
	{"volumeAsk", func(receiver Receiver) Receiver { return askingVolume(receiver, 44.5, askAt(1)) }},
	{"powerAsk", func(receiver Receiver) Receiver { return askingPower(receiver, "toggle", askAt(1)) }},
	{"inputAsk", func(receiver Receiver) Receiver { return askingInput(receiver, "ensure", askAt(1)) }},
}

// turnedToDVD is a receiver a person turned on and moved to DVD, so
// every ask in firstPassAsks has a command to send it.
func turnedToDVD(t *testing.T) *fakeDenon {
	t.Helper()
	fake := startFakeDenon(t)
	handOnTheRemote(t, fake, denon.PowerOnCommand)
	fake.waitForCommands(t, denon.PowerOnCommand)
	handOnTheRemote(t, fake, "SIDVD")
	fake.waitForCommands(t, "SIDVD")
	return fake
}

// An ask can be older than a turn of the knob, so an ask the operator
// finds when it starts is recorded and sends nothing, on this pass and
// on the passes after it.
func TestAnAskTheFirstPassFindsSendsNothing(t *testing.T) {
	t.Parallel()
	for _, one := range firstPassAsks {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fake := turnedToDVD(t)
				asked := one.ask(topiclessReceiver(fake.address()))
				api, operator, _ := reachedController(t, asked, "127.0.0.1:1")

				passWith(t, api, operator, asked)

				fake.refuseEveryCommand(t, quietPeriod)
			})
		})
	}
}

// The first pass reaches each unit before its driver has connected, so
// this test hands the rule an ask on a unit that is connected: an ask
// read while the unit adopts is recorded and sends nothing.
func TestAnAskReadWhileAdoptingSendsNothing(t *testing.T) {
	t.Parallel()
	for _, one := range firstPassAsks {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fake := turnedToDVD(t)
				receiver := topiclessReceiver(fake.address())
				api, operator, _ := reachedController(t, receiver, "127.0.0.1:1")
				asked := one.ask(receiver)

				operator.units["theater"].takeAsks(asked.Status.Session, true)
				passWith(t, api, operator, asked)

				fake.refuseEveryCommand(t, quietPeriod)
			})
		})
	}
}

// volumeCommands keeps the volume sets out of the commands a receiver
// took, and leaves out the queries.
func volumeCommands(commands []string) []string {
	return slices.DeleteFunc(slices.Clone(commands), func(command string) bool {
		return !strings.HasPrefix(command, "MV") || strings.HasSuffix(command, "?")
	})
}

// The receiver reports none of the volumes it takes, so each ask after
// the first arrives before it answers. The asks that arrive during the
// wait leave only the newest, and the receiver goes straight to it.
func TestVolumeAsksFasterThanTheReceiverSendOnlyTheNewest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		receiver := topiclessReceiver(fake.address())
		api, operator, _ := reachedController(t, receiver, "127.0.0.1:1")
		fake.holdEchoes()
		passWith(t, api, operator, askingVolume(receiver, 45, askAt(1)))
		fake.waitForCommands(t, "MV45")

		passWith(t, api, operator, askingVolume(receiver, 44.5, askAt(2)))
		passWith(t, api, operator, askingVolume(receiver, 44, askAt(3)))
		passWith(t, api, operator, askingVolume(receiver, 43.5, askAt(4)))

		mustDeepEqual(t, volumeCommands(fake.waitForCommands(t, "MV435")), []string{"MV435"})
	})
}

// A power ask follows the rules of the power topic: a toggle on a room
// in standby turns the receiver on and selects the session's input.
func TestAPowerAskTogglesTheRoom(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		receiver := topiclessReceiver(fake.address())
		api, operator, _ := reachedController(t, receiver, "127.0.0.1:1")

		passWith(t, api, operator, askingPower(receiver, "toggle", askAt(1)))

		fake.waitForCommands(t, denon.PowerOnCommand)
		fake.waitForCommands(t, "SIGAME")
	})
}

// An input ask of ensure or show brings a receiver that is on back to
// the session's input.
func TestAnInputAskEnsuresTheInput(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"ensure", "show"} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fake := turnedToDVD(t)
				receiver := topiclessReceiver(fake.address())
				api, operator, _ := reachedController(t, receiver, "127.0.0.1:1")

				passWith(t, api, operator, askingInput(receiver, action, askAt(1)))

				fake.waitForCommands(t, "SIGAME")
			})
		})
	}
}

// A volume ask is absolute, so each part goes out only when the
// receiver reports another value. The fake receiver starts at volume 50
// with mute off.
func TestAVolumeAskSendsOnlyWhatDiffers(t *testing.T) {
	cases := []struct {
		name string
		ask  ReceiverVolumeAsk
		want []string
	}{
		{"the mute", ReceiverVolumeAsk{Level: 50, Mute: true}, []string{"MUON"}},
		{"the volume and the mute", ReceiverVolumeAsk{Level: 49.5, Mute: true}, []string{"MV495", "MUON"}},
		{"nothing", ReceiverVolumeAsk{Level: 50}, nil},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fake := startFakeDenon(t)
				receiver := topiclessReceiver(fake.address())
				api, operator, log := reachedController(t, receiver, "127.0.0.1:1")
				asked := askingVolume(receiver, one.ask.Level, askAt(1))
				asked.Status.Session.VolumeAsk.Mute = one.ask.Mute

				passWith(t, api, operator, asked)

				waitForLines(t, log, "status.session.volumeAsk", 1)
				time.Sleep(quietPeriod)
				mustDeepEqual(t, setCommands(fake), one.want)
			})
		})
	}
}

// setCommands drains the commands a receiver took, and leaves out the
// queries.
func setCommands(fake *fakeDenon) []string {
	var sets []string
	for {
		select {
		case command := <-fake.commands:
			if !strings.HasSuffix(command, "?") {
				sets = append(sets, command)
			}
		default:
			return sets
		}
	}
}

// A session that replaces another one, here for a new input, carries
// the asks of the session before it, and the operator does not send
// them again.
func TestASessionThatReplacesAnotherSendsNoAskAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		receiver := topiclessReceiver(fake.address())
		api, operator, _ := reachedController(t, receiver, "127.0.0.1:1")
		asked := askingVolume(receiver, 44.5, askAt(1))
		passWith(t, api, operator, asked)
		fake.waitForCommands(t, "MV445")

		asked.Status.Session.Input = "DVD"
		passWith(t, api, operator, asked)

		fake.refuseEveryCommand(t, quietPeriod)
	})
}

// A session with no volume topic and no power topic has nothing on the
// bus, so it opens no broker connection: no owner mark and no will.
func TestASessionWithNoTopicsOpensNoBrokerConnection(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		brokers := startFakeBrokerServer(t)
		_, operator, log := reachedController(t, topiclessReceiver(fake.address()), brokers.address())

		time.Sleep(quietPeriod)
		operator.stopAll()

		mustMatch(t, len(brokers.sessions), 0)
		mustMatch(t, len(linesWith(log, "owner mark")), 0)
	})
}

// inputSelectedCondition answers a status's InputSelected condition.
func inputSelectedCondition(status ReceiverStatus) Condition {
	for _, condition := range status.Conditions {
		if condition.Type == inputSelectedConditionType {
			return condition
		}
	}
	return Condition{}
}

// InputSelected follows the input the receiver reports: True on the
// session's input, and False on any other.
func TestInputSelectedFollowsTheReportedInput(t *testing.T) {
	cases := []struct {
		line   string
		status ConditionStatus
		reason string
	}{
		{"SIGAME", ConditionTrue, reasonSessionInput},
		{"SIDVD", ConditionFalse, reasonOtherInput},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.line, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fake := startFakeDenon(t)
				api, _, _ := reachedController(t, topiclessReceiver(fake.address()), "127.0.0.1:1")

				handOnTheRemote(t, fake, one.line)

				status := api.waitForStatus(t, func(status ReceiverStatus) bool {
					return status.Zones["main"].Input == one.line[2:]
				})
				condition := inputSelectedCondition(status)
				mustMatch(t, condition.Status, one.status)
				mustMatch(t, condition.Reason, one.reason)
			})
		})
	}
}

// A receiver the operator has not reached has no input to compare, so
// the condition is Unknown and not the verdict of a stale report.
func TestInputSelectedIsUnknownBeforeTheReceiverAnswers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		receiver := topiclessReceiver("127.0.0.1:1")
		api.setReceivers(receiver)
		operator := startController(t, api)

		mustSucceed(t, operator.pass(t.Context()))

		status := api.waitForStatus(t, func(status ReceiverStatus) bool { return len(status.Conditions) > 1 })
		mustMatch(t, inputSelectedCondition(status).Status, ConditionUnknown)
		mustMatch(t, inputSelectedCondition(status).Reason, reasonUnreachable)
	})
}
