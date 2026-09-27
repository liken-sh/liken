package main

// What a session tells the TV of its room: a session that starts is
// adopted and wakes nothing, a flag that turns on and a toggle to on are
// wakes, and a toggle to standby or both flags going off is a sleep.
// television_session_test.go tests what each of them writes.

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// roomRecord records the wakes and the sleeps a session reports. The
// TV side is the API server's, and these tests are about when the
// session reports, so a record is the narrowest stand-in.
type roomRecord struct {
	mutex  sync.Mutex
	events []string
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

func (r *roomRecord) slept() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.events = append(r.events, "slept")
}

// waitFor waits until the record holds a count of events, and answers
// them.
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
			t.Fatalf("the room heard %q, want %d events", events, count)
		}
		time.Sleep(time.Millisecond)
	}
}

// A session that starts, even with both flags on, is adopted and wakes
// nothing; a flag that turns on later is a wake.
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
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
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
	}
}

// The remote's power button wakes the room when it turns the receiver
// on, and puts it to sleep when it turns the receiver off.
func TestAToggleWakesAndSleepsTheRoom(t *testing.T) {
	h := newSessionHarness(t)
	h.powerTopic = testPowerTopic
	room := &roomRecord{}
	h.room = room
	h.powerOn(t)
	h.beginIdle(t, "GAME")
	broker := h.brokers.waitForSession(t)
	broker.waitForTopic(t, ownerTopic(testVolumeTopic))

	broker.push(testPowerTopic, []byte(`{"action":"toggle"}`))
	h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Power == equipment.PowerStandby })
	broker.push(testPowerTopic, []byte(`{"action":"toggle"}`))
	mustMatch(t, h.equipment.waitForCommand(t), "PWSTANDBY")
	mustMatch(t, h.equipment.waitForCommand(t), denon.PowerOnCommand)

	mustDeepEqual(t, room.waitFor(t, 3), []string{
		"opened (awake false)",
		"slept",
		"woke: the power topic asks toggle",
	})
}
