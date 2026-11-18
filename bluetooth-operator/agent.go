package main

// This file implements the BlueZ pairing agent, and the checks that
// treat some BlueZ errors as success.
//
// BlueZ does not pair without an agent. During a pairing, it calls
// methods on an object that this operator exports on D-Bus. The
// agent's capability tells BlueZ whether the machine can display a
// passkey, accept a typed one, or neither.

import (
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// agentManager is the D-Bus object that registers agents. BlueZ has
// one for the daemon, not one for each adapter.
const agentManager = dbus.ObjectPath("/org/bluez")

// registerAgent exports the agent object, registers it, and makes it
// the default agent.
func (r *blueZRadio) registerAgent() error {
	if !r.agentExported {
		if err := r.conn.Export(&pairingAgent{}, agentPath, agentInterface); err != nil {
			return fmt.Errorf("exporting the pairing agent: %w", err)
		}
		r.agentExported = true
	}
	// BlueZ returns org.bluez.Error.AlreadyExists when this agent is
	// already registered, which happens when a second request opens a
	// window while the first is still open.
	if err := r.call(agentManager, agentManagerInterface+".RegisterAgent", callTimeout, agentPath, agentCapability); err != nil && !alreadyExists(err) {
		return fmt.Errorf("registering the pairing agent: %w", err)
	}
	if err := r.call(agentManager, agentManagerInterface+".RequestDefaultAgent", callTimeout, agentPath); err != nil {
		return fmt.Errorf("requesting the default agent role: %w", err)
	}
	return nil
}

// unregisterAgent unregisters the agent with BlueZ. The object stays
// exported for the next registerAgent. That is safe, because
// bluetoothd calls only a registered agent.
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

// Each of these reports a BlueZ error that means the call's goal is
// already in place, so the caller treats it as success.
func inProgress(err error) bool    { return isDBusError(err, "org.bluez.Error.InProgress") }
func alreadyExists(err error) bool { return isDBusError(err, "org.bluez.Error.AlreadyExists") }
func doesNotExist(err error) bool  { return isDBusError(err, "org.bluez.Error.DoesNotExist") }

// notRunning reports whether an error from StopDiscovery means that no
// discovery is running. BlueZ returns org.bluez.Error.Failed with the
// message "No discovery started" in that case, but it uses Failed for
// other failures too. So every Failed or NotReady error counts as not
// running.
func notRunning(err error) bool {
	return isDBusError(err, "org.bluez.Error.Failed") || isDBusError(err, "org.bluez.Error.NotReady")
}

// isDBusError reports whether err is a D-Bus error with the given
// name. godbus returns a remote error as a dbus.Error value on some
// paths and as a *dbus.Error on others, so it checks both.
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

// pairingAgent implements BlueZ's agent interface with the
// NoInputNoOutput capability.
//
// A NoInputNoOutput agent pairs with the Just Works method, which
// exchanges a key with no value for a person to compare. BlueZ still
// calls the agent to authorize the pairing and to authorize each
// service the device requests, and this agent accepts both. Because
// it accepts any pairing without asking anyone, the operator registers
// it only while a pairing window that a person opened is open.
//
// The methods that type or display a passkey or PIN return an error.
// The operator does not support a device that needs one, and the error
// makes its pairing fail instead of hang.
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
