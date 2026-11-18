package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
)

// sessionReceiver is a Denon Receiver with a session and no requests.
func sessionReceiver(address string) Receiver {
	receiver := testReceiver("theater", address)
	receiver.Spec.Volume = &ReceiverVolume{Max: 69.5, Step: 0.5}
	receiver.Status.Session = &ReceiverSession{Player: "house/theater", Input: "GAME"}
	return receiver
}

// askAt returns the `at` of the nth request, in the format that
// media-operator writes.
func askAt(n int) string {
	return fmt.Sprintf("2026-10-04T12:15:25.%03dZ", n)
}

// askingVolume, askingPower, and askingInput copy the session before
// they change it, because the fake API still serves the Receiver that a
// test set earlier while the test builds the next one.
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

func passWith(t *testing.T, api *fakeAPI, operator *controller, receiver Receiver) {
	t.Helper()
	api.setReceivers(receiver)
	mustSucceed(t, operator.pass(t.Context()))
}

func TestANewVolumeAskSendsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		receiver := sessionReceiver(fake.address())
		api, operator, _ := reachedController(t, receiver)

		asked := askingVolume(receiver, 44.5, askAt(1))
		passWith(t, api, operator, asked)
		fake.waitForCommands(t, "MV445")
		passWith(t, api, operator, asked)

		fake.refuseEveryCommand(t, quietPeriod)
	})
}

// Each request in firstPassAsks would send a command to a receiver that
// is on and reports an input other than GAME.
var firstPassAsks = []struct {
	name string
	ask  func(Receiver) Receiver
}{
	{"volumeAsk", func(receiver Receiver) Receiver { return askingVolume(receiver, 44.5, askAt(1)) }},
	{"powerAsk", func(receiver Receiver) Receiver { return askingPower(receiver, "toggle", askAt(1)) }},
	{"inputAsk", func(receiver Receiver) Receiver { return askingInput(receiver, "ensure", askAt(1)) }},
}

// turnedToDVD returns a receiver that a person turned on and set to the
// DVD input with its own remote.
func turnedToDVD(t *testing.T) *fakeDenon {
	t.Helper()
	fake := startFakeDenon(t)
	handOnTheRemote(t, fake, denon.PowerOnCommand)
	fake.waitForCommands(t, denon.PowerOnCommand)
	handOnTheRemote(t, fake, "SIDVD")
	fake.waitForCommands(t, "SIDVD")
	return fake
}

// The operator records a request that it finds on its first pass and
// never sends it, on that pass or a later one.
func TestAskFoundOnFirstPassSendsNothing(t *testing.T) {
	t.Parallel()
	for _, one := range firstPassAsks {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fake := turnedToDVD(t)
				asked := one.ask(sessionReceiver(fake.address()))
				api, operator, _ := reachedController(t, asked)

				passWith(t, api, operator, asked)

				fake.refuseEveryCommand(t, quietPeriod)
			})
		})
	}
}

// On the first pass the driver has not connected yet, so
// TestAskFoundOnFirstPassSendsNothing does not cover a connected unit.
// This test gives a connected unit a request while adopting is true.
func TestAnAskReadWhileAdoptingSendsNothing(t *testing.T) {
	t.Parallel()
	for _, one := range firstPassAsks {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fake := turnedToDVD(t)
				receiver := sessionReceiver(fake.address())
				api, operator, _ := reachedController(t, receiver)
				asked := one.ask(receiver)

				operator.units["theater"].takeAsks(asked.Status.Session, true)
				passWith(t, api, operator, asked)

				fake.refuseEveryCommand(t, quietPeriod)
			})
		})
	}
}

// volumeCommands returns the commands that set the volume, without the
// queries.
func volumeCommands(commands []string) []string {
	return slices.DeleteFunc(slices.Clone(commands), func(command string) bool {
		return !strings.HasPrefix(command, "MV") || strings.HasSuffix(command, "?")
	})
}

// holdEchoes makes the fake receiver apply each volume it is sent and
// not report it.
func (f *fakeDenon) holdEchoes() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.holdingEchoes = true
}

// The receiver never reports the volumes it applies, so each request
// after the first arrives while the operator waits for a report.
func TestVolumeAsksFasterThanTheReceiverSendOnlyTheNewest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		receiver := sessionReceiver(fake.address())
		api, operator, _ := reachedController(t, receiver)
		fake.holdEchoes()
		passWith(t, api, operator, askingVolume(receiver, 45, askAt(1)))
		fake.waitForCommands(t, "MV45")

		passWith(t, api, operator, askingVolume(receiver, 44.5, askAt(2)))
		passWith(t, api, operator, askingVolume(receiver, 44, askAt(3)))
		passWith(t, api, operator, askingVolume(receiver, 43.5, askAt(4)))

		mustDeepEqual(t, volumeCommands(fake.waitForCommands(t, "MV435")), []string{"MV435"})
	})
}

// A power request follows the same rules as spec.power: a toggle while
// the room is in standby turns the receiver on and selects the
// session's input.
func TestAPowerAskTogglesTheRoom(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		receiver := sessionReceiver(fake.address())
		api, operator, _ := reachedController(t, receiver)

		passWith(t, api, operator, askingPower(receiver, "toggle", askAt(1)))

		fake.waitForCommands(t, denon.PowerOnCommand)
		fake.waitForCommands(t, "SIGAME")
	})
}

// An input request of ensure or show sets a receiver that is on back to
// the session's input.
func TestAnInputAskEnsuresTheInput(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"ensure", "show"} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fake := turnedToDVD(t)
				receiver := sessionReceiver(fake.address())
				api, operator, _ := reachedController(t, receiver)

				passWith(t, api, operator, askingInput(receiver, action, askAt(1)))

				fake.waitForCommands(t, "SIGAME")
			})
		})
	}
}

// The fake receiver starts at volume 50 with mute off.
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
				receiver := sessionReceiver(fake.address())
				api, operator, log := reachedController(t, receiver)
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

// setCommands returns the commands the fake receiver has received,
// without the queries, and clears them.
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

// A change of input replaces the session.
func TestASessionThatReplacesAnotherSendsNoAskAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := startFakeDenon(t)
		receiver := sessionReceiver(fake.address())
		api, operator, _ := reachedController(t, receiver)
		asked := askingVolume(receiver, 44.5, askAt(1))
		passWith(t, api, operator, asked)
		fake.waitForCommands(t, "MV445")

		asked.Status.Session.Input = "DVD"
		passWith(t, api, operator, asked)

		fake.refuseEveryCommand(t, quietPeriod)
	})
}

func inputSelectedCondition(status ReceiverStatus) Condition {
	for _, condition := range status.Conditions {
		if condition.Type == inputSelectedConditionType {
			return condition
		}
	}
	return Condition{}
}

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
				api, _, _ := reachedController(t, sessionReceiver(fake.address()))

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

// Before the operator reaches the receiver there is no input to
// compare, so InputSelected is Unknown.
func TestInputSelectedIsUnknownBeforeTheReceiverAnswers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		receiver := sessionReceiver("127.0.0.1:1")
		api.setReceivers(receiver)
		operator := startController(t, api)

		mustSucceed(t, operator.pass(t.Context()))

		status := api.waitForStatus(t, func(status ReceiverStatus) bool { return len(status.Conditions) > 1 })
		mustMatch(t, inputSelectedCondition(status).Status, ConditionUnknown)
		mustMatch(t, inputSelectedCondition(status).Reason, reasonUnreachable)
	})
}
