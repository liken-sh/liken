package main

// These tests cover a link that the host ends. main.go describes how
// the kernel can then leave the device off its list of devices to
// reconnect.

import (
	"encoding/binary"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/bluetooth-operator/bluetoothd/mgmt"
)

// remoteHandle is the connection handle of the remote's link.
const remoteHandle = 16

// connected returns the LE Connection Complete event for a link to
// address, as the central, with remoteHandle.
func connected(address string, peerType byte) []byte {
	params := []byte{hciLEConnectionComplete, 0}
	params = binary.LittleEndian.AppendUint16(params, remoteHandle)
	params = append(params, 0, peerType)
	params = append(params, kernelAddress(address)...)
	return hciEvent(hciEvLEMeta, append(params, 0x06, 0, 0x45, 0, 0x2c, 0x01, 0)...)
}

// disconnect returns the HCI Disconnect command for the remote's link,
// with reason 0x13, which hci_conn_timeout sends.
func disconnect() []byte {
	params := binary.LittleEndian.AppendUint16(nil, remoteHandle)
	return hciCommand(hciDisconnect, append(params, 0x13)...)
}

func disconnectComplete(reason byte) []byte {
	params := []byte{0}
	params = binary.LittleEndian.AppendUint16(params, remoteHandle)
	return hciEvent(hciEvDisconnectComplete, append(params, reason)...)
}

// linkUp returns the steps of the kernel starting a connection to the
// remote and the link coming up.
func linkUp(after time.Duration) []step {
	return append(attemptStarts(after), at(0, connected(remote, 0))...)
}

// The trace of a remote that stopped responding: the kernel connects,
// sends HCI Disconnect two seconds later, and the controller completes
// the disconnect only after the kernel's two-second wait has run out.
// The kernel does not scan again, so rescanSettle after the Disconnect
// the program sends Add Device with action 1 and then with action 2,
// auto connect.
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
		t.Fatalf("the kernel received %q, want %q, log:\n%s", got, want, log)
	}
	line := "start-rescan: hci0: the host ended the link to 58:AB:4B:24:91:47 and the kernel did not scan again within 5s, so start-rescan added the device again to make the kernel scan for it"
	if !strings.Contains(log, line) {
		t.Fatalf("the log lacks the line %q, log:\n%s", line, log)
	}
}

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
				t.Fatalf("the kernel received %q, want nothing, log:\n%s", got, log)
			}
		})
	}
}
