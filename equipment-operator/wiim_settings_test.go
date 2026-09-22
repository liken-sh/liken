package main

// The WiiM settings path: a declared block reaches the device once, and
// a block the device already reports is not re-sent.

import (
	"context"
	"fmt"
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
// device, and hands back the unit the controller would own.
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
	for client.State().Reachable != equipment.ConditionTrue {
		select {
		case <-deadline:
			t.Fatal("the client never reached the fake amp")
		case <-time.After(time.Millisecond):
		}
	}
	return client, &receiverUnit{name: "studio", driver: client, wiimClient: client}
}

// A declared block is enforced once even when the device already
// reports it, because the operator starts with no memory of what it
// applied, and a second pass with the same block sends nothing.
func TestADeclaredWiimSettingReachesTheDeviceOnce(t *testing.T) {
	amp := startFakeWiim(t)
	_, unit := waitingWiim(t, amp)

	name := "Studio"
	led := true
	want := wiim.Settings{Device: wiim.DeviceSettings{Name: &name, LED: &led}}

	unit.setWiimSettings(want)
	deadline := time.After(testTimeout)
	for len(amp.sent()) < 2 {
		select {
		case <-deadline:
			t.Fatalf("only sent %v", amp.sent())
		case <-time.After(time.Millisecond):
		}
	}
	mustDeepEqual(t, amp.sent(), []string{"setDeviceName:Studio", "LED_SWITCH_SET:1"})

	// The device reports the declared values and the operator remembers
	// applying them, so the same block sends nothing again.
	unit.setWiimSettings(want)
	time.Sleep(50 * time.Millisecond)
	mustDeepEqual(t, amp.sent(), []string{"setDeviceName:Studio", "LED_SWITCH_SET:1"})
}

func TestAnUndeclaredWiimSettingSendsNothing(t *testing.T) {
	amp := startFakeWiim(t)
	_, unit := waitingWiim(t, amp)

	unit.setWiimSettings(wiim.Settings{})
	time.Sleep(50 * time.Millisecond)
	mustDeepEqual(t, amp.sent(), []string(nil))
}
