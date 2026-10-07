package main

// Inactive Source. The adapter tells the TV that the Display has no
// picture when the Player's screen goes dark while the route still
// leads to the Display, and when the node workload stops. A room that
// goes to standby gets Standby instead, and a route that a person moved
// to another source is that source's.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// inactiveOf answers the Inactive Source messages the adapters sent.
func inactiveOf(wire *cectest.Bus) []string {
	var sent []string
	for _, message := range wire.Sent() {
		if opcode, _ := message.Opcode(); opcode == cec.OpInactiveSource {
			sent = append(sent, message.String())
		}
	}
	return sent
}

func TestADarkScreenSendsInactiveSource(t *testing.T) {
	cases := []struct {
		name     string
		before   []cec.Message
		standby  bool
		inactive []string
	}{
		{"the screen sleeps on the Display's route", nil, false, []string{"4->0 9d 13 00"}},
		{"a person moved the route to another source",
			[]cec.Message{cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x15, 0x00)}, false, nil},
		{"the room goes to standby", nil, true, nil},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := roomWithTV(televisionTV(cec.PowerOn))
				session := wokeNow()
				api := awakeRoom(t, wire, session)
				wokeWith(t, api, session)
				for _, message := range c.before {
					wire.Send(message)
				}
				// The adapter must hear the moved route before the
				// screen goes dark, or it still holds the route to the
				// Display and sends Inactive Source for it.
				synctest.Wait()

				asleep := *session
				asleep.Awake = false
				if c.standby {
					asleep.StandbyAt = time.Now().UTC().Format(wakeTimeLayout)
				}
				api.putTelevision(waking(&asleep))
				api.nudge()
				time.Sleep(quietPeriod)

				mustDeepEqual(t, inactiveOf(wire), c.inactive)
			})
		})
	}
}

// After Inactive Source the adapter holds no route to the Display, so
// it answers no Request Active Source for a screen that is dark.
func TestInactiveSourceEndsTheRoute(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(televisionTV(cec.PowerOn))
		session := wokeNow()
		api := awakeRoom(t, wire, session)
		wokeWith(t, api, session)
		asleep := *session
		asleep.Awake = false
		api.putTelevision(waking(&asleep))
		api.waitUntil(t, "Inactive Source", func() bool { return len(inactiveOf(wire)) == 1 })

		api.putTelevision(waking(session))
		api.nudge()
		time.Sleep(quietPeriod)
		askForTheSource(wire)
		time.Sleep(quietPeriod)

		mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00"})
	})
}

// A node workload that stops while the route leads to its Display, and
// no session holds the room awake, tells the TV that the Display has no
// source behind it. A session that holds the room awake keeps its
// picture through a restart of the node workload, so the adapter sends
// nothing then.
func TestAStoppedNodeSendsInactiveSource(t *testing.T) {
	cases := []struct {
		name     string
		awake    bool
		before   []cec.Message
		inactive []string
	}{
		{"no session and a route to the Display",
			false, []cec.Message{cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x13, 0x00)}, []string{"4->0 9d 13 00"}},
		{"a session that holds the room awake", true, nil, nil},
		{"no session and a route to another source",
			false, []cec.Message{cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x15, 0x00)}, nil},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := roomWithTV(televisionTV(cec.PowerOn))
				api := startCECAPI(t)
				api.putTelevision(lounge(""))
				_, stop, done := joinedNode(t, api, wire)
				if c.awake {
					session := wokeNow()
					wakeLive(t, api, session)
					wokeWith(t, api, session)
				}
				for _, message := range c.before {
					wire.Send(message)
				}
				// The adapter must hear the moved route before the
				// screen goes dark, or it still holds the route to the
				// Display and sends Inactive Source for it.
				synctest.Wait()
				time.Sleep(quietPeriod)

				stop()
				<-done

				mustDeepEqual(t, inactiveOf(wire), c.inactive)
			})
		})
	}
}
