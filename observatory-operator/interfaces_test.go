package main

import (
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// dialAsClient connects to a server as a client such as KStars does,
// and returns a function that sends one message on that connection.
func (w *world) dialAsClient(server string) func(message string) {
	w.t.Helper()
	client, err := w.indi.DialContext(w.t.Context(), "tcp", server+".observatory.svc:7624")
	if err != nil {
		w.t.Fatalf("connecting to the INDI server %s: %v", server, err)
	}
	w.t.Cleanup(func() { client.Close() })
	go func() { _, _ = io.Copy(io.Discard, client) }()
	return func(message string) { _, _ = client.Write([]byte(message)) }
}

func wheelPhase(w *world) observatory.DevicePhase {
	wheel, _ := decode[observatory.FilterWheel](w.t, w.api, kindCollection(observatory.FilterWheelKind), "east")
	return wheel.Status.Phase
}

// When a device deletes its interface's properties during a session
// and defines them again, the operator does not write the settings a
// second time, because a client such as a sequencer may have changed
// one since.
func TestADeviceThatDefinesItsInterfaceAgainKeepsWhatAClientChanged(t *testing.T) {
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
		w.until(time.Minute, "the filter wheel is not Connected again", func() bool { return wheelPhase(w) == observatory.DeviceConnected })
		time.Sleep(time.Minute)
		synctest.Wait()
		if n := w.indi.count("east-telescope", "CCD Simulator.FILTER_NAME"); n != before+1 {
			t.Errorf("%s changes = %d, want only the client's", names, n-before)
		}
	})
}

// When a driver does not answer a request for its properties, Ready
// says so, and the operator sends the request again only when the
// device's CONNECTION changes, not on every change that wakes the
// runner.
func TestADriverThatDoesNotAnswerIsAskedAgainOnlyWhenItsConnectionChanges(t *testing.T) {
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
			t.Errorf("the operator sent the silent driver %d more getProperties requests, want none until CONNECTION changes", n)
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
