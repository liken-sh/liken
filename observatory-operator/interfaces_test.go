package main

// A resource serves its interface while its INDI device defines the
// interface's detection properties (interfaces.go). The verdict changes
// when the device defines them or deletes them, and the operator asks
// the driver for its properties only when it has a reason to.

import (
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// dialAsClient opens a connection to a server as a client such as
// KStars does, and answers a function that sends one message on it.
func (w *world) dialAsClient(server string) func(message string) {
	w.t.Helper()
	client, err := w.indi.DialContext(w.t.Context(), "tcp", server+".observatory.svc:7624")
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { client.Close() })
	go func() { _, _ = io.Copy(io.Discard, client) }()
	return func(message string) { _, _ = client.Write([]byte(message)) }
}

// wheelPhase answers the phase of the example's FilterWheel east.
func wheelPhase(w *world) observatory.DevicePhase {
	wheel, _ := decode[observatory.FilterWheel](w.t, w.api, kindCollection(observatory.FilterWheelKind), "east")
	return wheel.Status.Phase
}

// A device that deletes its interface's properties during a session
// and defines them again heals without its settings, because they went
// out once for the reservation, and a client such as a sequencer may
// have changed one since.
func TestAResourceThatHealsDuringASessionKeepsWhatAClientChanged(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		const names = "east-telescope CCD Simulator.FILTER_NAME"
		before := w.indi.count("east-telescope", "CCD Simulator.FILTER_NAME")
		send := w.dialAsClient("east-telescope")
		send(`<newTextVector device="CCD Simulator" name="FILTER_NAME"><oneText name="FILTER_SLOT_NAME_1">Clear</oneText></newTextVector>`)
		w.until(time.Minute, "the client's change does not reach the camera", func() bool {
			return w.indi.count("east-telescope", "CCD Simulator.FILTER_NAME") == before+1
		})

		w.indi.conceal("CCD Simulator", "FILTER_SLOT")
		w.until(time.Minute, "the filter wheel does not lack its interface", func() bool { return wheelPhase(w) == observatory.DeviceError })
		w.indi.reveal("CCD Simulator", "FILTER_SLOT")
		w.until(time.Minute, "the filter wheel does not heal", func() bool { return wheelPhase(w) == observatory.DeviceConnected })
		time.Sleep(time.Minute)
		synctest.Wait()
		if n := w.indi.count("east-telescope", "CCD Simulator.FILTER_NAME"); n != before+1 {
			t.Errorf("%s changes = %d, want only the client's", names, n-before)
		}
	})
}

// A driver that does not answer a request for its properties gets a
// verdict that says so, and the operator asks it again only when the
// device's CONNECTION changes, not on each change that wakes the
// runner.
func TestADriverThatDoesNotAnswerIsAskedAgainWhenItsConnectionChanges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.indi.withhold("CCD Simulator", "FILTER_SLOT")
		w.indi.silence("CCD Simulator")
		readyOf(w)
		silent := "Failed: INDI device CCD Simulator did not answer a request for its properties"
		if ready := w.stateOf(observatory.FilterWheelKind, "east", observatory.ConditionReady); !strings.Contains(ready, silent) {
			t.Errorf("Ready = %q, want %q", ready, silent)
		}
		asked := w.indi.requests("CCD Simulator")
		for _, name := range []string{"spare-1", "spare-2", "spare-3"} {
			w.put(observatory.FocuserKind, name, map[string]any{"driver": map[string]any{"name": "indi_simulator_focus"}})
			time.Sleep(time.Minute)
			synctest.Wait()
		}
		if n := w.indi.requests("CCD Simulator") - asked; n != 0 {
			t.Errorf("the operator asked the silent driver %d more times, want none until CONNECTION changes", n)
		}

		w.indi.unsilence("CCD Simulator")
		send := w.dialAsClient("east-telescope")
		send(`<newSwitchVector device="CCD Simulator" name="CONNECTION"><oneSwitch name="CONNECT">Off</oneSwitch><oneSwitch name="DISCONNECT">On</oneSwitch></newSwitchVector>`)
		send(`<newSwitchVector device="CCD Simulator" name="CONNECTION"><oneSwitch name="CONNECT">On</oneSwitch><oneSwitch name="DISCONNECT">Off</oneSwitch></newSwitchVector>`)
		lacks := "Failed: INDI device CCD Simulator defines no FILTER_SLOT after it connects"
		w.until(time.Minute, "the operator does not ask the driver again", func() bool {
			return strings.Contains(w.stateOf(observatory.FilterWheelKind, "east", observatory.ConditionReady), lacks)
		})
	})
}
