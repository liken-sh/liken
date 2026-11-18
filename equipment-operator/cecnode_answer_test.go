package main

// Tests of the adapter's answers to Request Active Source and Set
// Stream Path. The wake fixtures are in cecnode_wake_test.go.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// askForTheSource sends a broadcast Request Active Source from the TV.
func askForTheSource(wire *cectest.Bus) {
	wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpRequestActiveSource))
}

// askedAfterTheWake wakes the room with a session, waits for the wake
// to end, and then replaces the session with the result of after.
func askedAfterTheWake(t *testing.T, wire *cectest.Bus, after func(session TelevisionSession) TelevisionSession) *cecAPI {
	t.Helper()
	session := wokeNow()
	api := awakeRoom(t, wire, session)
	wokeWith(t, api, session)
	later := after(*session)
	api.putTelevision(waking(&later))
	api.nudge()
	time.Sleep(quietPeriod)
	return api
}

func TestTheAdapterAnswersARequestForTheActiveSource(t *testing.T) {
	cases := []struct {
		name   string
		after  func(session TelevisionSession) TelevisionSession
		claims []string
	}{
		{"an awake session",
			func(session TelevisionSession) TelevisionSession { return session },
			[]string{"4->f 82 13 00", "4->f 82 13 00"}},
		{"a session that went to sleep",
			func(session TelevisionSession) TelevisionSession { session.Awake = false; return session },
			[]string{"4->f 82 13 00"}},
		{"a session on another Display",
			func(session TelevisionSession) TelevisionSession {
				session.Display = "bnq-0002-monitor"
				return session
			},
			[]string{"4->f 82 13 00"}},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithTV(televisionTV(cec.PowerOn))
				askedAfterTheWake(t, wire, c.after)

				askForTheSource(wire)
				time.Sleep(quietPeriod)

				mustDeepEqual(t, claimsOf(wire), c.claims)
				// The answer is Active Source alone, so the wake sent the only
				// Image View On.
				mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
			})
		})
	}
}

// After the wake, a source that sends Active Source is the active
// source, so the adapter leaves the TV's request to it.
func TestTheAdapterLeavesTheRequestToTheSourceAPersonChose(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		askedAfterTheWake(t, wire, func(session TelevisionSession) TelevisionSession { return session })
		wire.Send(cec.ActiveSource(8, 0x1500))

		askForTheSource(wire)
		time.Sleep(quietPeriod)

		mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00"})
	})
}

// A TV that boots slowly sends Request Active Source before it reports
// On, so before the wake sends its own Active Source. The wake answers
// although the last Active Source on the bus is another source's,
// because the wake is about to claim the input.
func TestTheWakeAnswersARequestBeforeItsOwnClaim(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// The TV stays in its power transition until the test ends, so
		// the only Active Source that the adapter sends is the answer.
		wire := busWithTV(televisionTV(cec.PowerStandby))
		api := controlling(t, wire, lounge(""))
		api.scanned(t, "node-1")
		wire.Send(cec.ActiveSource(8, 0x1500))
		booting := televisionTV(cec.PowerStandby)
		booting.Transition = 1000
		wire.Add(booting)
		session := wokeNow()
		api.putTelevision(waking(session))
		api.waitUntil(t, "Image View On", func() bool { return sentOf(wire, cec.OpImageViewOn) == 1 })

		askForTheSource(wire)

		api.waitUntil(t, "the answer", func() bool { return len(claimsOf(wire)) == 1 })
		mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00"})
	})
}

// An answer writes one log line that names the Television, the Display,
// and the Player.
func TestTheAnswerWritesALine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := busWithTV(televisionTV(cec.PowerOn))
		_, device := usbAdapter(wire)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		api.putTelevision(lounge(""))
		log := loggedNode(t, api, device)
		session := wokeNow()
		wakeLive(t, api, session)
		wokeWith(t, api, session)

		askForTheSource(wire)

		api.waitUntil(t, "the answer", func() bool { return len(linesWith(log, "Request Active Source")) == 2 })
		mustDeepEqual(t, linesWith(log, "Request Active Source"), []string{
			`CECBus den: "TV" (logical 0, 0.0.0.0) broadcast Request Active Source`,
			`Television lounge: "TV" (logical 0, 0.0.0.0) broadcast Request Active Source; the adapter on node-1 answered with Active Source for 1.3.0.0, Display acm-0001-receiver, because Player media/den's session is awake`,
		})
	})
}

// The adapter answers a Set Stream Path for the Display's address, and
// leaves one for another address to that source.
func TestTheAdapterAnswersASetStreamPathToTheDisplay(t *testing.T) {
	cases := []struct {
		name    string
		address cec.PhysicalAddress
		awake   bool
		claims  []string
	}{
		{"the Display's address", 0x1300, true, []string{"4->f 82 13 00", "4->f 82 13 00"}},
		{"another source's address", 0x1500, true, []string{"4->f 82 13 00"}},
		{"a session that went to sleep", 0x1300, false, []string{"4->f 82 13 00"}},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithTV(televisionTV(cec.PowerOn))
				askedAfterTheWake(t, wire, func(session TelevisionSession) TelevisionSession { session.Awake = c.awake; return session })
				// Another source's Active Source does not stop the answer,
				// because the TV chose the Display by its address.
				wire.Send(cec.ActiveSource(8, 0x1500))

				wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, byte(c.address>>8), byte(c.address)))
				time.Sleep(quietPeriod)

				mustDeepEqual(t, claimsOf(wire), c.claims)
				// The TV is on and chose the Display, so no Image View On is sent.
				mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
			})
		})
	}
}

// When a switch, the TV, or a Standby moves the route away from the
// Display, the adapter answers no Request Active Source until the route
// leads to the Display again, although the session stays awake.
func TestARouteThatMovesAwayEndsTheAnswer(t *testing.T) {
	cases := []struct {
		name    string
		message cec.Message
		claims  []string
	}{
		{"a Routing Change to another input",
			cec.NewMessage(5, cec.AddressBroadcast, cec.OpRoutingChange, 0x13, 0x00, 0x15, 0x00),
			[]string{"4->f 82 13 00"}},
		{"a Routing Information for another input",
			cec.NewMessage(5, cec.AddressBroadcast, cec.OpRoutingInformation, 0x15, 0x00),
			[]string{"4->f 82 13 00"}},
		{"a Set Stream Path to another source",
			cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x15, 0x00),
			[]string{"4->f 82 13 00"}},
		{"a Standby",
			cec.Standby(cec.AddressTV, cec.AddressBroadcast),
			[]string{"4->f 82 13 00"}},
		{"a Routing Change back to the Display's input",
			cec.NewMessage(5, cec.AddressBroadcast, cec.OpRoutingChange, 0x15, 0x00, 0x13, 0x00),
			[]string{"4->f 82 13 00", "4->f 82 13 00"}},
		{"a Routing Information for the receiver above the Display",
			cec.NewMessage(5, cec.AddressBroadcast, cec.OpRoutingInformation, 0x10, 0x00),
			[]string{"4->f 82 13 00", "4->f 82 13 00"}},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithTV(televisionTV(cec.PowerOn))
				askedAfterTheWake(t, wire, func(session TelevisionSession) TelevisionSession { return session })
				wire.Send(c.message)

				askForTheSource(wire)
				time.Sleep(quietPeriod)

				mustDeepEqual(t, claimsOf(wire), c.claims)
			})
		})
	}
}
