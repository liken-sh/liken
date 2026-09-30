package main

// The session in status.session. The media operator writes it under
// its own field manager, and the operator reads it first. A Receiver
// with no status.session falls back to spec.session, so an older media
// operator still drives the receiver. The operator's own status apply
// never states the session, so it never removes it.

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

// A session in either place starts on its input.
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

// status.session wins when both places hold a session, so a media
// operator that has moved to the status is not undone by the spec
// session it has not cleared yet.
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

// A session that moves from spec.session to status.session unchanged is
// the same session: it does not end, and it sends nothing.
func TestASessionThatMovesToTheStatusIsNoChange(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		operator, log := loggedController(t, api, "127.0.0.1:1")
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

// statedStatusFields answers each status field the applies stated, once
// for each apply that stated it.
func statedStatusFields(t *testing.T, bodies [][]byte) []string {
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

// The operator's status apply states no session, so the API server
// leaves the session the media operator's field manager owns in place.
func TestTheStatusApplyNeverStatesTheSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		api.setReceivers(inStatus(sessionedReceiver("theater", fake.address(), "GAME")))
		operator := startController(t, api)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)

		api.mutex.Lock()
		stated := statedStatusFields(t, api.statusBodies)
		api.mutex.Unlock()

		mustMatch(t, slices.Contains(stated, "conditions"), true)
		mustMatch(t, slices.Contains(stated, "session"), false)
	})
}
