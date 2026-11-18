package main

// The layout module opens a Wayland socket for each prepared claim.
// When a surface appears, the module reports the socket its client
// connected on, and the operator finds the claim from that socket.
//
// The CDI spec sets WAYLAND_DISPLAY to the claim's own socket, but it
// mounts the whole socket directory. So a client that sets another
// name can connect to another claim's socket. The socket name tells a
// well-behaved client where to connect, and it does not restrict
// access.

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// waylandDisplayVariable is set in each claim's CDI spec, and
// waylandSocket reads it back to find the claim's socket.
const waylandDisplayVariable = "WAYLAND_DISPLAY"

const waylandSocketPrefix = "wayland-"

// claimSocketNames returns the Wayland socket name for each output
// device in the claim's allocation results, keyed by output device.
// An output and its draw device share one socket, and a control
// device gets none.
//
// The module places every surface of a socket on one output, so a
// claim on several outputs needs a socket for each. The first output
// gets wayland-<claim UID>, and each later one gets
// wayland-<claim UID>-<output device>. The results keep the order the
// scheduler wrote, so a repeated prepare of the claim returns the same
// names.
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

// replaySockets opens the Wayland socket of every prepared claim
// again. The layout link calls it on each new connection to the
// module, because a compositor restart closes every socket the module
// opened, and running pods reconnect by the names in their
// environment.
//
// It reads the claims from the CDI specs on disk, so it also works
// after the operator's container restarts. The spec has the published
// device name, and the module needs the kernel's connector name, so it
// scans the card to map one to the other.
//
// A socket whose output is gone from the card, or that the module
// refuses, does not stop the others. It returns the errors joined.
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
				fmt.Errorf("socket %s is for output %s, and card %s has no such output", socket, device, p.card))
			continue
		}
		if err := p.layout.Listen(socket, connector); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// waylandSocket returns the WAYLAND_DISPLAY value in a device's CDI
// edits, or an empty string for a control device, which sets none.
func waylandSocket(edits cdiEdits) string {
	for _, entry := range edits.Env {
		if name, set := strings.CutPrefix(entry, waylandDisplayVariable+"="); set {
			return name
		}
	}
	return ""
}

// preparedSockets returns the Wayland sockets that one claim's CDI
// spec sets, for unprepare to close.
//
// It leaves out socketName, the compositor's own socket. Weston opens
// that socket itself, so the module must never be asked to close it.
// Prepare does not write it into a spec, but a spec that an earlier
// version of the operator wrote can still set it.
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

// preparedSocket is the claim UID and the output device of one
// prepared claim's socket.
type preparedSocket struct {
	claim  string
	device string
}

// preparedSocketClaims returns the socket of every prepared claim,
// keyed by socket name. The placement pass uses it to find the claim
// that drew a surface, because the module reports only the socket.
//
// It reads the claim UID from the spec file and not from the socket
// name, because a UID contains dashes, and so does the
// wayland-<claim UID>-<output device> form.
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

// preparedSocketOutputs returns the output device of each prepared
// socket, keyed by socket name. A draw device maps to the output it
// draws on.
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

// releaseSockets asks the layout module to close the sockets of one
// claim. It reads the socket names from the claim's CDI spec, so it
// must run before the spec is deleted.
//
// It only logs errors, so unprepare does not fail on them. The sockets
// exist only in the running compositor. A module that is not serving
// has already lost them, and a failed unprepare would make the kubelet
// retry for a socket that no longer exists.
func (p *draPlugin) releaseSockets(claimUID string) {
	sockets, err := preparedSockets(claimUID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the sockets of claim %s: %v\n", claimUID, err)
		return
	}
	for _, socket := range sockets {
		if err := p.layout.Close(socket); err != nil {
			fmt.Fprintf(os.Stderr, "closing the Wayland socket %s: %v\n", socket, err)
		}
	}
}
