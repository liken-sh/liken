package main

// start-rescan puts a device back on the kernel's reconnect list when
// a link that the host ended leaves the device off it. These tests
// play the trace of a remote that went quiet: the kernel connects to
// it, ends the link two seconds later, and the controller completes
// the disconnect after the kernel stopped waiting for it.

import (
	"encoding/binary"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/bluetooth-operator/bluetoothd/mgmt"
)

// remoteHandle is the connection handle the controller gives the
// remote's link.
const remoteHandle = 16

// connected is LE Connection Complete for a link to address, with the
// handle, the central role, and the peer address type.
func connected(address string, peerType byte) []byte {
	params := []byte{hciLEConnectionComplete, 0}
	params = binary.LittleEndian.AppendUint16(params, remoteHandle)
	params = append(params, 0, peerType)
	params = append(params, kernelAddress(address)...)
	return hciEvent(hciEvLEMeta, append(params, 0x06, 0, 0x45, 0, 0x2c, 0x01, 0)...)
}

// disconnect is the host's HCI Disconnect of the remote's link, with
// reason 0x13, the reason hci_conn_timeout gives.
func disconnect() []byte {
	params := binary.LittleEndian.AppendUint16(nil, remoteHandle)
	return hciCommand(hciDisconnect, append(params, 0x13)...)
}

func disconnectComplete(reason byte) []byte {
	params := []byte{0}
	params = binary.LittleEndian.AppendUint16(params, remoteHandle)
	return hciEvent(hciEvDisconnectComplete, append(params, reason)...)
}

// linkUp is the kernel connecting to the remote after its
// advertisement, and the link coming up.
func linkUp(after time.Duration) []step {
	return append(attemptStarts(after), at(0, connected(remote, 0))...)
}

// A link that the host ends, and that the controller does not close
// within the kernel's wait, leaves the remote off the kernel's list
// of devices to reconnect. The program sends Add Device with another
// action and then with auto connect, rescanSettle after the
// Disconnect, because the kernel ignores an Add Device that repeats
// the action it holds.
func TestALinkThatTheHostEndsWithNoRescanAddsTheDeviceAgain(t *testing.T) {
	got, log := play(t, &managementKernel{},
		registered(mgmt.AddressLEPublic),
		linkUp(time.Hour),
		at(2*time.Second, disconnect(), commandStatus(hciDisconnect, 0)),
		at(3*time.Second, disconnectComplete(0x22)),
	)

	want := []string{
		"+1h0m7s hci0 58:AB:4B:24:91:47 type 1 action 1",
		"+1h0m7s hci0 58:AB:4B:24:91:47 type 1 action 2",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the kernel received %q, want %q; log:\n%s", got, want, log)
	}
	line := "start-rescan: hci0: the host ended the link to 58:AB:4B:24:91:47 and the kernel did not scan again within 5s; added the device again, so the kernel scans for it"
	if !strings.Contains(log, line) {
		t.Fatalf("the log has no %q:\n%s", line, log)
	}
}

// A link that ends the ordinary way is followed by the kernel's own
// scan, and a link to a device that bluetoothd does not reconnect on
// its own is none of the program's business.
func TestALinkThatEndsWithARescanIsLeftAlone(t *testing.T) {
	cases := []struct {
		name  string
		steps [][]step
	}{
		{
			name: "the controller closes the link and the kernel scans again",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				linkUp(time.Hour),
				at(2*time.Second, disconnect(), commandStatus(hciDisconnect, 0)),
				at(650*time.Millisecond, disconnectComplete(0x16)),
				at(1400*time.Millisecond, setScanEnable(1), commandComplete(hciLESetScanEnable, 0)),
			},
		},
		{
			name: "the link is to a device that bluetoothd did not add for auto connect",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				at(time.Hour, createConnection("11:22:33:44:55:66", 0), commandStatus(hciLECreateConnection, 0)),
				at(0, connected("11:22:33:44:55:66", 0)),
				at(2*time.Second, disconnect(), commandStatus(hciDisconnect, 0)),
			},
		},
		{
			name: "bluetoothd removes the device after the disconnect, as an unpair does",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				linkUp(time.Hour),
				at(2*time.Second, disconnect(), commandStatus(hciDisconnect, 0)),
				at(time.Second, removeDevice(remote, mgmt.AddressLEPublic)),
			},
		},
		{
			name: "the adapter powers off after the disconnect",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				linkUp(time.Hour),
				at(2*time.Second, disconnect(), commandStatus(hciDisconnect, 0)),
				at(time.Second, monitorPacket(monitorCloseIndex)),
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, log := play(t, &managementKernel{}, c.steps...)
			if len(got) != 0 {
				t.Fatalf("the kernel received %q, want nothing; log:\n%s", got, log)
			}
		})
	}
}
