package main

// The WiiM settings path: the operator sends only the declared fields
// the device reports at another value.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
	"github.com/liken-sh/equipment-operator/wiim"
)

// fakeWiim is a small stateful amp: the reads answer what the setters
// last set, so a poll reflects a declared setting and the confirm path
// is exercised. It is a real TLS server, because the driver's client
// skips certificate verification and speaks HTTPS.
type fakeWiim struct {
	server *httptest.Server

	mutex    sync.Mutex
	name     string
	led      bool
	buttons  bool
	balance  float64
	commands []string
}

func startFakeWiim(t *testing.T) *fakeWiim {
	t.Helper()
	amp := &fakeWiim{name: "Studio", led: true}
	amp.server = httptest.NewTLSServer(http.HandlerFunc(amp.handle))
	t.Cleanup(amp.server.Close)
	return amp
}

func (f *fakeWiim) handle(w http.ResponseWriter, r *http.Request) {
	command := r.URL.Query().Get("command")
	f.mutex.Lock()
	defer f.mutex.Unlock()
	switch {
	case command == "getStatusEx":
		fmt.Fprintf(w, `{"DeviceName":%q,"uuid":"FF98F2F78136CE45A780D8A1"}`, f.name)
	case command == "LED_SWITCH_GET":
		fmt.Fprint(w, digit(f.led))
	case command == "Button_Enable_GET":
		fmt.Fprint(w, digit(f.buttons))
	case command == "getChannelBalance":
		fmt.Fprint(w, strconv.FormatFloat(f.balance, 'f', -1, 64))
	case strings.HasPrefix(command, "setDeviceName:"):
		f.name = strings.TrimPrefix(command, "setDeviceName:")
		f.record(command)
		fmt.Fprint(w, "OK")
	case strings.HasPrefix(command, "LED_SWITCH_SET:"):
		f.led = strings.TrimPrefix(command, "LED_SWITCH_SET:") == "1"
		f.record(command)
		fmt.Fprint(w, "OK")
	case strings.HasPrefix(command, "Button_Enable_SET:"):
		f.buttons = strings.TrimPrefix(command, "Button_Enable_SET:") == "1"
		f.record(command)
		fmt.Fprint(w, "OK")
	case strings.HasPrefix(command, "setChannelBalance:"):
		f.balance, _ = strconv.ParseFloat(strings.TrimPrefix(command, "setChannelBalance:"), 64)
		f.record(command)
		fmt.Fprint(w, "OK")
	case strings.HasPrefix(command, "MCUKeyShortClick:"),
		strings.HasPrefix(command, "startbtdiscovery:"),
		strings.HasPrefix(command, "connectbta2dpsynk:"),
		strings.HasPrefix(command, "disconnectbta2dpsynk:"),
		command == "reboot":
		f.record(command)
		fmt.Fprint(w, "OK")
	default:
		fmt.Fprint(w, "unknown command")
	}
}

// record appends one setter command. The caller holds the lock.
func (f *fakeWiim) record(command string) {
	f.commands = append(f.commands, command)
}

func (f *fakeWiim) sent() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]string(nil), f.commands...)
}

func digit(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

// waitingWiim starts a client, waits until its first poll reaches the
// device and its first survey of the device's settings ends, and hands
// back the unit the controller would own. The unit applies no declared
// setting before the survey, so a test that waited only for the poll
// could apply before it and see nothing sent.
func waitingWiim(t *testing.T, amp *fakeWiim) (*wiim.Client, *receiverUnit) {
	t.Helper()
	client := wiim.NewClient(amp.server.Listener.Addr().String(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		client.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	deadline := time.After(testTimeout)
	for client.State().Reachable != equipment.ConditionTrue || !client.Surveyed() {
		select {
		case <-deadline:
			t.Fatal("the client never reached the fake amp")
		case <-time.After(time.Millisecond):
		}
	}
	return client, &receiverUnit{name: "studio", driver: client, wiimClient: client, log: newReceiverLog(io.Discard, "studio"), budget: newSendBudget()}
}

// A restart against a device that already reports every declared value
// sends nothing: the operator compares the spec with what the device
// reports, not with a memory it lost in the restart.
func TestARestartAgainstASettledWiimSendsNothing(t *testing.T) {
	amp := startFakeWiim(t)
	_, unit := waitingWiim(t, amp)

	name, led := "Studio", true
	want := wiim.Settings{Device: wiim.DeviceSettings{Name: &name, LED: &led}}

	unit.setWiimSettings(want)
	unit.setWiimSettings(want)
	time.Sleep(50 * time.Millisecond)
	mustDeepEqual(t, amp.sent(), []string(nil))
}

// One declared field the device reports at another value sends that
// field and nothing beside it.
func TestOneDifferingWiimFieldSendsOnlyThatField(t *testing.T) {
	amp := startFakeWiim(t)
	_, unit := waitingWiim(t, amp)

	name, led := "Studio", false
	unit.setWiimSettings(wiim.Settings{Device: wiim.DeviceSettings{Name: &name, LED: &led}})

	deadline := time.After(testTimeout)
	for len(amp.sent()) < 1 {
		select {
		case <-deadline:
			t.Fatal("the operator sent nothing")
		case <-time.After(time.Millisecond):
		}
	}
	time.Sleep(50 * time.Millisecond)
	mustDeepEqual(t, amp.sent(), []string{"LED_SWITCH_SET:0"})
}

func TestAnUndeclaredWiimSettingSendsNothing(t *testing.T) {
	amp := startFakeWiim(t)
	_, unit := waitingWiim(t, amp)

	unit.setWiimSettings(wiim.Settings{})
	time.Sleep(50 * time.Millisecond)
	mustDeepEqual(t, amp.sent(), []string(nil))
}
