package main

// The pairing agent, and the BlueZ errors that mean "already done".
//
// BlueZ pairs nothing without an agent. During a pairing it calls back
// into an object this operator exports on the bus, and that object
// reports what the machine can do: display a passkey, take one typed
// in, or neither. This one is registered for the length of a pairing
// window and unregistered when the window closes, because it accepts
// on behalf of a person, and a person only asked for one window.

import (
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// agentManager is the object BlueZ registers agents through. There is
// one for the daemon, not one for each adapter.
const agentManager = dbus.ObjectPath("/org/bluez")

// registerAgent exports the agent object and makes it the default one
// for the length of the window. BlueZ calls back into it during a
// pairing, and a pairing with no agent registered is rejected.
func (r *blueZRadio) registerAgent() error {
	if !r.agentExported {
		if err := r.conn.Export(&pairingAgent{}, agentPath, agentInterface); err != nil {
			return fmt.Errorf("exporting the pairing agent: %w", err)
		}
		r.agentExported = true
	}
	// An agent that is already registered returns
	// org.bluez.Error.AlreadyExists, which is the ordinary case when a
	// second request opens a window while the first one is still open.
	if err := r.call(agentManager, agentManagerInterface+".RegisterAgent", callTimeout, agentPath, agentCapability); err != nil && !alreadyExists(err) {
		return fmt.Errorf("registering the pairing agent: %w", err)
	}
	if err := r.call(agentManager, agentManagerInterface+".RequestDefaultAgent", callTimeout, agentPath); err != nil {
		return fmt.Errorf("asking to be the default agent: %w", err)
	}
	return nil
}

// unregisterAgent removes the agent's registration with BlueZ. The
// export stays, because exporting is cheap and bluetoothd calls nothing
// on an agent it does not hold a registration for.
func (r *blueZRadio) unregisterAgent() error {
	if !r.agentExported {
		return nil
	}
	err := r.call(agentManager, agentManagerInterface+".UnregisterAgent", callTimeout, agentPath)
	if err != nil && !doesNotExist(err) {
		return fmt.Errorf("unregistering the pairing agent: %w", err)
	}
	return nil
}

// The BlueZ errors that mean "the state you asked for is already the
// state" rather than "the call failed". Each one is identified by its
// D-Bus error name.
func inProgress(err error) bool    { return isDBusError(err, "org.bluez.Error.InProgress") }
func alreadyExists(err error) bool { return isDBusError(err, "org.bluez.Error.AlreadyExists") }
func doesNotExist(err error) bool  { return isDBusError(err, "org.bluez.Error.DoesNotExist") }

// notRunning covers a StopDiscovery with no discovery running. BlueZ
// returns org.bluez.Error.Failed for that case, with the text "No
// discovery started", and uses the same error name for real failures,
// so this treats every Failed and NotReady from a stop as "not
// running".
func notRunning(err error) bool {
	return isDBusError(err, "org.bluez.Error.Failed") || isDBusError(err, "org.bluez.Error.NotReady")
}

// isDBusError reads the error name out of a reply. godbus carries a
// remote error as a value on one path and as a pointer on another, so
// both shapes are checked.
func isDBusError(err error, name string) bool {
	var value dbus.Error
	if errors.As(err, &value) {
		return value.Name == name
	}
	var pointer *dbus.Error
	if errors.As(err, &pointer) && pointer != nil {
		return pointer.Name == name
	}
	return false
}

// pairingAgent handles BlueZ's pairing callbacks with the
// NoInputNoOutput capability.
//
// A NoInputNoOutput agent takes the Just Works path, where the two
// sides agree on a key with nothing for a person to compare. BlueZ
// still asks the agent to authorize the pairing and to authorize each
// service the device asks for, and this agent accepts both. That is
// why it is registered only while a window is open: an agent that is
// registered outside a window would accept pairings no person asked
// for.
//
// The methods that need a passkey typed or displayed refuse. A device
// that needs one is out of scope for this API version, and refusing is
// what makes that visible as a failed pairing rather than a hang.
type pairingAgent struct{}

var errRejected = dbus.NewError("org.bluez.Error.Rejected", nil)

func (a *pairingAgent) Release() *dbus.Error { return nil }

func (a *pairingAgent) RequestPinCode(device dbus.ObjectPath) (string, *dbus.Error) {
	return "", errRejected
}

func (a *pairingAgent) DisplayPinCode(device dbus.ObjectPath, pincode string) *dbus.Error {
	return errRejected
}

func (a *pairingAgent) RequestPasskey(device dbus.ObjectPath) (uint32, *dbus.Error) {
	return 0, errRejected
}

func (a *pairingAgent) DisplayPasskey(device dbus.ObjectPath, passkey uint32, entered uint16) *dbus.Error {
	return errRejected
}

func (a *pairingAgent) RequestConfirmation(device dbus.ObjectPath, passkey uint32) *dbus.Error {
	return errRejected
}

func (a *pairingAgent) RequestAuthorization(device dbus.ObjectPath) *dbus.Error { return nil }

func (a *pairingAgent) AuthorizeService(device dbus.ObjectPath, uuid string) *dbus.Error { return nil }

func (a *pairingAgent) Cancel() *dbus.Error { return nil }
