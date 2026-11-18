package main

// When a session reports a wake or a sleep to the TV of its room.
// television_session_test.go tests what each report writes.

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// roomRecord records the calls that a session makes on its room.
type roomRecord struct {
	mutex  sync.Mutex
	events []string
	// name and power are what television returns: the TV's name and
	// its status.power, both empty for a room with no TV.
	name, power string
}

func (r *roomRecord) television() (string, string) {
	return r.name, r.power
}

func (r *roomRecord) standby(trigger string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.events = append(r.events, "standby: "+trigger)
}

func (r *roomRecord) opened(awake bool, _ string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.events = append(r.events, fmt.Sprintf("opened (awake %t)", awake))
}

func (r *roomRecord) woke(trigger string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.events = append(r.events, "woke: "+trigger)
}

func (r *roomRecord) show(trigger string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.events = append(r.events, "show: "+trigger)
}

func (r *roomRecord) slept() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.events = append(r.events, "slept")
}

func (r *roomRecord) waitFor(t *testing.T, count int) []string {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for {
		r.mutex.Lock()
		events := slices.Clone(r.events)
		r.mutex.Unlock()
		if len(events) >= count {
			return events
		}
		if time.Now().After(deadline) {
			t.Fatalf("the room heard %q before the deadline, want %d events", events, count)
		}
		time.Sleep(time.Millisecond)
	}
}

// A session that starts with both flags on wakes nothing, because the
// operator adopts it.
func TestASessionWakesTheRoomOnlyWhenAFlagTurnsOn(t *testing.T) {
	cases := []struct {
		name   string
		begin  [2]bool
		flags  [][2]bool
		events []string
	}{
		{"a session that opens with a Play", [2]bool{true, true}, nil, []string{
			"opened (awake true)",
		}},
		{"a Play on an idle session", [2]bool{false, false}, [][2]bool{{true, false}}, []string{
			"opened (awake false)",
			"woke: a Play started on Player theater",
		}},
		{"a Play that ends and a screen that sleeps", [2]bool{true, true}, [][2]bool{{false, true}, {false, false}}, []string{
			"opened (awake true)",
			"slept",
		}},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newSessionHarness(t)
				room := &roomRecord{}
				h.room = room
				started := h.beginSession(t, "GAME", c.begin[0], c.begin[1])

				for _, flags := range c.flags {
					started.setFlags(flags[0], flags[1])
				}

				mustDeepEqual(t, room.waitFor(t, len(c.events)), c.events)
				time.Sleep(quietPeriod)
				mustDeepEqual(t, room.waitFor(t, len(c.events)), c.events)
			})
		})
	}
}

// In a room with no TV, the remote's power button puts the room to
// sleep when it turns the receiver off, and wakes it when it turns the
// receiver on.
func TestAToggleWakesAndSleepsTheRoom(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		room := &roomRecord{}
		h.room = room
		h.powerOn(t)
		held := h.beginIdle(t, "GAME")

		powerAsk(held, "toggle")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Power == equipment.PowerStandby })
		powerAsk(held, "toggle")
		mustMatch(t, h.equipment.waitForCommand(t), "PWSTANDBY")
		mustMatch(t, h.equipment.waitForCommand(t), denon.PowerOnCommand)

		mustDeepEqual(t, room.waitFor(t, 3), []string{
			"opened (awake false)",
			"slept",
			"woke: status.session.powerAsk requests toggle, and the receiver reports power Standby",
		})
	})
}
