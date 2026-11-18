package main

// Tests of where the operator reads a Receiver's session. Receiver.session
// gives the reasons that status.session comes before spec.session.

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"testing/synctest"
)

// inStatus moves a Receiver's session from the spec to the status.
func inStatus(receiver Receiver) Receiver {
	receiver.Status.Session, receiver.Spec.Session = receiver.Spec.Session, nil
	return receiver
}

// A session in status.session or in spec.session selects its input.
func TestASessionInTheStatusOrTheSpecSelectsItsInput(t *testing.T) {
	cases := []struct {
		name  string
		place func(Receiver) Receiver
	}{
		{"status.session", inStatus},
		{"spec.session", func(receiver Receiver) Receiver { return receiver }},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := startFakeAPI(t)
				fake := startFakeDenon(t)
				api.setReceivers(testReceiver("theater", fake.address()))
				operator := startController(t, api)
				mustSucceed(t, operator.pass(t.Context()))
				api.waitForStatus(t, connected)

				api.setReceivers(one.place(sessionedReceiver("theater", fake.address(), "GAME")))
				mustSucceed(t, operator.pass(t.Context()))

				fake.waitForCommands(t, "SIGAME")
			})
		})
	}
}

// When both status.session and spec.session are set, the operator
// uses status.session.
func TestTheStatusSessionWinsOverTheSpecSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", fake.address()))
		operator := startController(t, api)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)

		both := sessionedReceiver("theater", fake.address(), "TV")
		both.Status.Session = sessionedReceiver("theater", fake.address(), "GAME").Spec.Session
		api.setReceivers(both)
		mustSucceed(t, operator.pass(t.Context()))

		fake.waitForCommands(t, "SIGAME")
		fake.refuseCommand(t, "SITV", quietPeriod)
	})
}

// A session that moves unchanged from spec.session to status.session
// keeps running, and the operator sends the receiver nothing.
func TestASessionThatMovesToTheStatusIsNoChange(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		operator, log := loggedController(t, api)
		api.setReceivers(testReceiver("theater", fake.address()))
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		spec := sessionedReceiver("theater", fake.address(), "GAME")
		api.setReceivers(spec)
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "SIGAME")

		moved := inStatus(spec)
		moved.Metadata.Generation++
		api.setReceivers(moved)
		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseEveryCommand(t, quietPeriod)
		mustDeepEqual(t, linesWith(log, "ended"), []string(nil))
	})
}

// statusFieldsApplied returns the name of each status field that the
// apply bodies set, once for each apply that set it.
func statusFieldsApplied(t *testing.T, bodies [][]byte) []string {
	t.Helper()
	var stated []string
	for _, body := range bodies {
		var applied struct {
			Status map[string]json.RawMessage `json:"status"`
		}
		mustSucceed(t, json.Unmarshal(body, &applied))
		stated = append(stated, slices.Collect(maps.Keys(applied.Status))...)
	}
	return stated
}

// The operator's status apply leaves out status.session, so it never
// overwrites the session that media-operator wrote.
func TestTheStatusApplyLeavesTheSessionOut(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		api.setReceivers(inStatus(sessionedReceiver("theater", fake.address(), "GAME")))
		operator := startController(t, api)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)

		api.mutex.Lock()
		stated := statusFieldsApplied(t, api.statusBodies)
		api.mutex.Unlock()

		mustMatch(t, slices.Contains(stated, "conditions"), true)
		mustMatch(t, slices.Contains(stated, "session"), false)
	})
}
