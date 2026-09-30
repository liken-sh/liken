package main

// A session that stands while its unit is replaced for a new wiring. A
// WiiM declared by its UUID alone has no address until discovery finds
// it, a few seconds after the operator starts, and a Denon's declared
// address can change. Either one replaces the unit, and the session the
// old unit held is the same session: the new unit takes it over with
// its flags and sends nothing for them, and the owner mark stays on the
// broker, so the media side never sees the level without an owner.

import (
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/wiim"
)

// rewired runs the first pass for a Receiver that holds a standing
// session and a status an earlier operator wrote, then moves its wiring
// and runs the next pass.
func rewired(t *testing.T, receiver Receiver, move func(*controller, *Receiver)) *logBuffer {
	t.Helper()
	api := startFakeAPI(t)
	brokers := startFakeBrokerServer(t)
	operator := newController(api.client, brokers.address(), testMetrics(t))
	operator.dial = testNetwork.dial
	operator.now = func() time.Time { return statusNow }
	log := &logBuffer{}
	operator.log = log
	t.Cleanup(operator.stopAll)
	receiver.Spec.Session.Active, receiver.Spec.Session.Awake = true, true
	receiver.Status.SettingsGeneration, receiver.Status.PowerGeneration = 377, 377
	api.setReceivers(receiver)
	mustSucceed(t, operator.pass(t.Context()))
	waitForLines(t, log, "published the owner mark", 1)

	move(operator, &receiver)
	api.setReceivers(receiver)
	mustSucceed(t, operator.pass(t.Context()))
	waitForLines(t, log, "published the owner mark", 2)
	return log
}

func TestARewiredUnitKeepsTheSessionThatStands(t *testing.T) {
	cases := []struct {
		name     string
		receiver func(*testing.T) Receiver
		move     func(*testing.T) func(*controller, *Receiver)
	}{
		{
			"a WiiM that discovery finds after the start",
			func(*testing.T) Receiver {
				receiver := idleReceiver("", ReceiverVolume{Max: 100})
				receiver.Spec.Denon = nil
				receiver.Spec.Wiim = &WiimProtocol{UUID: "FF98F2F78136CE45A780D8A1"}
				return receiver
			},
			func(t *testing.T) func(*controller, *Receiver) {
				amp := startFakeWiim(t)
				return func(operator *controller, _ *Receiver) {
					operator.discovery.store([]wiim.Device{{UUID: "FF98F2F78136CE45A780D8A1", Address: amp.server.Listener.Addr().String()}})
				}
			},
		},
		{
			"a Denon whose address changes",
			func(t *testing.T) Receiver { return idleReceiver(switchedOn(t).address(), ReceiverVolume{Max: 69.5}) },
			func(t *testing.T) func(*controller, *Receiver) {
				moved := switchedOn(t)
				return func(_ *controller, receiver *Receiver) { receiver.Spec.Denon.Address = moved.address() }
			},
		},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			log := rewired(t, one.receiver(t), one.move(t))

			mustDeepEqual(t, linesWith(log, "cleared the owner mark"), []string(nil))
			mustDeepEqual(t, linesWith(log, "session for Player"), []string{
				"Receiver theater: a session for Player house/theater started: input GAME, volume topic liken/players/theater/volume, no power topic, active true, awake true; the operator found it when it started, so it sends nothing for these flags",
				"Receiver theater: a session for Player house/theater started: input GAME, volume topic liken/players/theater/volume, no power topic, active true, awake true; the unit that held it before the wiring changed handed it over, so it sends nothing for these flags",
			})
			mustDeepEqual(t, linesWith(log, "sent"), []string(nil))
		})
	}
}
