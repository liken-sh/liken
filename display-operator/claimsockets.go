package main

// The Wayland socket a claim holds: what it is named, how it comes
// back after a compositor restart, and how it goes when the claim
// does.
//
// The layout module opens one socket for each prepared claim, and the
// socket a surface arrives on is the claim that drew it. Nothing in a
// consumer's container can change it: the CDI spec names the claim's
// own socket, and the module reports the socket it accepted the
// client on.

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// waylandDisplayVariable names the socket a Wayland client connects
// to. The operator writes it into every Wayland delivery and reads it
// back out of the spec, so the socket a claim holds is stated in one
// place.
const waylandDisplayVariable = "WAYLAND_DISPLAY"

// waylandSocketPrefix starts the name of every socket the module
// opens for a claim. libwayland builds a client's path from
// XDG_RUNTIME_DIR and the socket's name, and the name says nothing
// about who may connect: the CDI spec is what tells one claim's pods
// which name is theirs.
const waylandSocketPrefix = "wayland-"

// claimSocketNames names the Wayland socket every result in one
// claim delivers, keyed by the output device the result names.
//
// One claim gets one socket, `wayland-<claim UID>`, because the
// socket is the claim's identity: the module reports which socket a
// surface arrived on, and the claim's status.reservedFor names the
// pods that hold it. A claim's output result and its draw result on
// the same connector deliver that one name.
//
// A claim that allocated two connectors is the exception. A socket
// belongs to one output, because the module remembers which output
// the surfaces on a socket go on, so the second connector and every
// one after it gets `wayland-<claim UID>-<output device>`. The
// results are walked in the order the scheduler wrote them, so the
// names are the same on every prepare of the same claim.
//
// A control result gets no socket. Its whole delivery is the i2c
// node, and it draws nothing.
func claimSocketNames(claimUID string, results []AllocatedDevice) map[string]string {
	sockets := map[string]string{}
	for _, result := range results {
		if result.Driver != DriverName {
			continue
		}
		device := result.Device
		if _, control := outputOfControl(device); control {
			continue
		}
		if output, draw := outputOfDraw(device); draw {
			device = output
		}
		if _, named := sockets[device]; named {
			continue
		}
		name := waylandSocketPrefix + claimUID
		if len(sockets) > 0 {
			name += "-" + device
		}
		sockets[device] = name
	}
	return sockets
}

// replaySockets re-opens the Wayland socket every prepared claim
// holds. It runs on every new connection to the module, because the
// compositor's restart took every socket the module had opened, and
// the clients reconnect to the names their environment already
// carries.
//
// The specs on disk are what says which claims hold a socket, so a
// restart of the operator's container replays the same set. The
// connector comes from a fresh walk of the card, because the spec
// names the published device and the module names the kernel's
// connector.
//
// A socket whose output the card does not carry is reported and
// skipped, and so is one the module refuses, because one claim must
// not cost every other claim its socket.
func (p *draPlugin) replaySockets() error {
	sockets, err := preparedSocketOutputs()
	if err != nil {
		return err
	}
	connectors := map[string]string{}
	for _, output := range discoverOutputs(p.sysRoot, p.card) {
		connectors[deviceName(output.Connector)] = output.Connector
	}
	var failures []error
	for socket, device := range sockets {
		connector, carried := connectors[device]
		if !carried {
			failures = append(failures,
				fmt.Errorf("%s was opened for output %s, which %s does not carry", socket, device, p.card))
			continue
		}
		if err := p.layout.Listen(socket, connector); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// waylandSocket names the socket one device's edits deliver. It is
// empty for a control device, whose whole delivery is an i2c node.
func waylandSocket(edits cdiEdits) string {
	for _, entry := range edits.Env {
		if name, set := strings.CutPrefix(entry, waylandDisplayVariable+"="); set {
			return name
		}
	}
	return ""
}

// preparedSockets names the Wayland sockets one claim holds, which is
// what an unprepare gives back to the layout module. A claim that
// allocated two connectors holds one socket for each of them.
//
// The compositor's own socket is not in the answer. weston opens
// wayland-0 itself, and a pod prepared before the per-claim sockets
// existed still holds it in its environment, so this operator never
// asks the module to close it.
func preparedSockets(claimUID string) ([]string, error) {
	spec, err := readCDISpec(claimUID)
	if err != nil {
		return nil, err
	}
	var sockets []string
	for _, device := range spec.Devices {
		name := waylandSocket(device.ContainerEdits)
		if name == "" || name == socketName || slices.Contains(sockets, name) {
			continue
		}
		sockets = append(sockets, name)
	}
	return sockets, nil
}

// preparedSocket is one socket a prepared claim holds: the claim that
// holds it, and the output device the module opened it for.
type preparedSocket struct {
	claim  string
	device string
}

// preparedSocketClaims names every socket the prepared claims hold,
// keyed by socket name. The placement pass reads it to learn which
// claim drew a surface, because the module reports the socket a
// surface arrived on and nothing else about who owns it.
//
// The name is not parsed for the claim's UID. A UID carries dashes of
// its own, so wayland-<claim UID>-<output device> cannot be split on
// one, and the spec file that named the socket already states which
// claim it was written for.
func preparedSocketClaims() (map[string]preparedSocket, error) {
	sockets := map[string]preparedSocket{}
	err := eachPreparedSpec(func(claimUID string, spec cdiSpec) {
		for _, device := range spec.Devices {
			name := waylandSocket(device.ContainerEdits)
			if name == "" || name == socketName {
				continue
			}
			held, prefixed := strings.CutPrefix(device.Name, claimUID+"-")
			if !prefixed {
				continue
			}
			if output, draw := outputOfDraw(held); draw {
				held = output
			}
			sockets[name] = preparedSocket{claim: claimUID, device: held}
		}
	})
	return sockets, err
}

// preparedSocketOutputs names the socket every prepared claim holds
// and the output device it was opened for, keyed by socket name. The
// replay reads it after every new connection to the layout module,
// and resolves each output device to its connector against a fresh
// connector walk.
//
// A draw device's socket is the socket of the output it draws on, so
// its name resolves to the same connector as the output device's.
func preparedSocketOutputs() (map[string]string, error) {
	sockets, err := preparedSocketClaims()
	if err != nil {
		return nil, err
	}
	outputs := make(map[string]string, len(sockets))
	for name, socket := range sockets {
		outputs[name] = socket.device
	}
	return outputs, nil
}

// releaseSockets asks the module to close the sockets one claim
// held, before the spec that names them goes.
//
// A failure here fails no unprepare. The sockets exist only in the
// running compositor, so a module that is not serving lost them
// already, and an unprepare that failed on one would hold the
// kubelet for a socket that is gone.
func (p *draPlugin) releaseSockets(claimUID string) {
	sockets, err := preparedSockets(claimUID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the sockets claim %s holds: %v\n", claimUID, err)
		return
	}
	for _, socket := range sockets {
		if err := p.layout.Close(socket); err != nil {
			fmt.Fprintf(os.Stderr, "closing the Wayland socket %s: %v\n", socket, err)
		}
	}
}
