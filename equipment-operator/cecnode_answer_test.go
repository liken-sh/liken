package main

// The adapter's answers to Request Active Source and Set Stream Path.
// A TV that boots, and a receiver that wakes with it, ask the bus which
// source is active, and the active source must answer. A TV whose menu
// picks an input names its address in Set Stream Path, and the device
// there must answer. While a Player's session holds the room awake, the
// adapter that speaks for the session's Display answers with Active
// Source for that Display. cecnode_wake_test.go holds the wake
// fixtures.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// askForTheSource puts a Request Active Source from the TV on the wire.
func askForTheSource(wire *cectest.Bus) {
	wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpRequestActiveSource))
}

// askedAfterTheWake wakes the room with a session, waits for the wake
// to end, and then applies the session as it stands after the wake.
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
		{"a session that holds the room awake",
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
				wire := roomWithTV(televisionTV(cec.PowerOn))
				askedAfterTheWake(t, wire, c.after)

				askForTheSource(wire)
				time.Sleep(quietPeriod)

				mustDeepEqual(t, claimsOf(wire), c.claims)
				// The answer is Active Source alone. The asker can be the
				// receiver while the TV is off, and Image View On would wake a
				// TV a person turned off.
				mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
			})
		})
	}
}

// A source that claimed the input after the wake's guard is a person's
// choice, and that source is the active source now. It answers the TV
// itself, so the adapter sends nothing.
func TestTheAdapterLeavesTheRequestToTheSourceAPersonChose(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(televisionTV(cec.PowerOn))
		askedAfterTheWake(t, wire, func(session TelevisionSession) TelevisionSession { return session })
		wire.Send(cec.ActiveSource(8, 0x1500))

		askForTheSource(wire)
		time.Sleep(quietPeriod)

		mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00"})
	})
}

// A TV that boots slowly asks for the active source before it reports
// On, which is before the wake sends its own Active Source. The last
// Active Source the adapter heard is then from an earlier evening, and
// the wake answers anyway, because it is claiming the input.
func TestTheWakeAnswersARequestBeforeItsOwnClaim(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// The TV stays in its transition past the end of the test, so the
		// wake's own Active Source never goes out, and the one on the wire is
		// the answer.
		wire := roomWithTV(televisionTV(cec.PowerStandby))
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

// The answer is an operation a person notices, so it writes one line
// that names the session it answers for.
func TestTheAnswerWritesALine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := roomWithTV(televisionTV(cec.PowerOn))
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
			`Television lounge: "TV" (logical 0, 0.0.0.0) broadcast Request Active Source; the adapter on node-1 answered with Active Source for 1.3.0.0, Display acm-0001-receiver, because Player media/den's session holds the room awake`,
		})
	})
}

// A TV whose menu picks the Display's input names the Display's address
// in Set Stream Path, and the adapter answers. An address of another
// source is that source's to answer.
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
				wire := roomWithTV(televisionTV(cec.PowerOn))
				askedAfterTheWake(t, wire, func(session TelevisionSession) TelevisionSession { session.Awake = c.awake; return session })
				// A streaming player's claim after the guard does not stop the
				// answer: the TV chose the Display by its address.
				wire.Send(cec.ActiveSource(8, 0x1500))

				wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, byte(c.address>>8), byte(c.address)))
				time.Sleep(quietPeriod)

				mustDeepEqual(t, claimsOf(wire), c.claims)
				// The TV asked for the Display, so it needs no Image View On.
				mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
			})
		})
	}
}

// A route that moves away from the Display takes the adapter's
// active-source status with it: a switch that routes another input, a
// TV that picks another source, and a Standby. The session can stay
// awake while a person watches another source, so the adapter answers
// no later request until the route leads to the Display again.
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
				wire := roomWithTV(televisionTV(cec.PowerOn))
				askedAfterTheWake(t, wire, func(session TelevisionSession) TelevisionSession { return session })
				wire.Send(c.message)

				askForTheSource(wire)
				time.Sleep(quietPeriod)

				mustDeepEqual(t, claimsOf(wire), c.claims)
			})
		})
	}
}
