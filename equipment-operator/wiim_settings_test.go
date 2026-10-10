package main

// The WiiM settings path: the operator sends only the declared fields
// the device reports at another value.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/equipment-operator/equipment"
	"github.com/liken-sh/equipment-operator/wiim"
)

// fakeWiim is a small stateful amp: the reads answer what the setters
// last set, so a poll reflects a declared setting and the confirm path
// is exercised. It serves TLS on the test network, because the
// driver's client speaks HTTPS and skips certificate verification.
type fakeWiim struct {
	address string

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
	certificate := testCertificate(t)
	amp.address = testNetwork.serveHTTP(t, http.HandlerFunc(amp.handle), &certificate)
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
// back the unit the controller would own. It runs in a synctest bubble,
// and the first poll has ended when every goroutine in the bubble
// waits. The unit applies no declared
// setting before the survey, so a test that waited only for the poll
// could apply before it and see nothing sent.
func waitingWiim(t *testing.T, amp *fakeWiim) (*wiim.Client, *receiverUnit) {
	t.Helper()
	client := wiim.NewClient(amp.address, nil)
	client.Dial = testNetwork.dial
	client.Listen = testNetwork.bind
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
	synctest.Wait()
	if client.State().Reachable != equipment.ConditionTrue || !client.Surveyed() {
		t.Fatal("the client never reached the fake amp")
	}
	return client, &receiverUnit{name: "studio", driver: client, wiimClient: client, log: newReceiverLog(io.Discard, "studio"), budget: newSendBudget(), settled: newSettledRecord(ReceiverStoredStatus{}, ReceiverSpec{})}
}

// A restart against a device that already reports every declared value
// sends nothing: the operator compares the spec with what the device
// reports, not with a memory it lost in the restart.
func TestARestartAgainstASettledWiimSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeWiim(t)
		_, unit := waitingWiim(t, amp)

		name, led := "Studio", true
		want := wiim.Settings{Device: wiim.DeviceSettings{Name: &name, LED: &led}}

		unit.setWiimSettings(want)
		unit.setWiimSettings(want)
		synctest.Wait()
		mustDeepEqual(t, amp.sent(), []string(nil))
	})
}

// One declared field the device reports at another value sends that
// field and nothing beside it.
func TestOneDifferingWiimFieldSendsOnlyThatField(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeWiim(t)
		_, unit := waitingWiim(t, amp)

		name, led := "Studio", false
		unit.setWiimSettings(wiim.Settings{Device: wiim.DeviceSettings{Name: &name, LED: &led}})
		synctest.Wait()

		mustDeepEqual(t, amp.sent(), []string{"LED_SWITCH_SET:0"})
	})
}

func TestAnUndeclaredWiimSettingSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeWiim(t)
		_, unit := waitingWiim(t, amp)

		unit.setWiimSettings(wiim.Settings{})
		synctest.Wait()
		mustDeepEqual(t, amp.sent(), []string(nil))
	})
}
