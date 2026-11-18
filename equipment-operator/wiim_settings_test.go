package main

// The operator sends only the declared WiiM settings whose value
// differs from what the device reports.

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

	"github.com/liken-sh/liken/equipment-operator/equipment"
	"github.com/liken-sh/liken/equipment-operator/wiim"
)

// fakeWiim is a fake amp whose read commands return the values that its
// setter commands last set. It serves HTTPS, as the client expects.
type fakeWiim struct {
	address string

	mutex    sync.Mutex
	name     string
	led      bool
	buttons  bool
	balance  float64
	commands []string
	// unread is the read commands that this fake model does not support.
	// The fake answers them as a real amp answers an unknown command.
	unread map[string]bool
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
	case f.unread[command]:
		fmt.Fprint(w, "unknown command")
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

// record appends a setter command to commands. The caller holds
// f.mutex.
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

// waitingWiim starts a client in a synctest bubble, waits until its
// first poll has read every settings family, and returns the client and
// the receiverUnit that the controller would create for it. The unit
// applies no declared setting until the client has read the settings,
// so a test that applied earlier would see nothing sent.
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
	if client.State().Reachable != equipment.ConditionTrue || !client.FirstReadDone() {
		t.Fatal("the client did not reach the fake amp and complete its first full read")
	}
	return client, &receiverUnit{name: "studio", driver: client, wiimClient: client, log: newReceiverLog(io.Discard, "studio"), budget: newSendBudget(), settled: newSettledRecord(ReceiverStoredStatus{}, ReceiverSpec{})}
}

// A device that already reports every declared value receives
// nothing, even from a new unit with no state, as after an operator
// restart.
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

// When one declared field differs from what the device reports, the
// operator sends that field and no other.
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

// When the model has no read command for a field, the operator cannot
// confirm the field's value, so it sends the declared value once and
// not again on later passes.
func TestAWiimFieldTheModelCannotReadIsSentOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeWiim(t)
		amp.unread = map[string]bool{"LED_SWITCH_GET": true}
		_, unit := waitingWiim(t, amp)

		led := true
		want := wiim.Settings{Device: wiim.DeviceSettings{LED: &led}}
		for range sendLimit + 1 {
			unit.setWiimSettings(want)
			synctest.Wait()
		}

		mustDeepEqual(t, amp.sent(), []string{"LED_SWITCH_SET:1"})
	})
}
