package main

// start-bluetoothd powers every adapter off before it starts
// bluetoothd.
//
// When bluetoothd registers an adapter that is already powered, it
// reads the kernel's open connections and marks each of those devices
// Connected, but it does not attach the input profile to them. So a
// remote whose link outlived the previous bluetoothd shows as Connected,
// has no HID device, and sends no input. bluetoothd powers its adapters
// off when it exits on TERM, which ends their links, but a bluetoothd
// that is killed or crashes leaves the adapters powered and the links
// up.
//
// So this program sends SET_POWERED off for each adapter over the
// Bluetooth management channel (package mgmt), then replaces itself
// with bluetoothd. bluetoothd registers each adapter powered off, so it
// reads no connections, and then powers it on because main.conf sets
// AutoEnable. Each device that had a link reconnects, as it does after
// any restart of the pod.
//
// bluetoothd manages every adapter that the kernel lists, so this
// program powers off all of them, not only the adapter that the pod
// claimed. The kernel accepts SET_POWERED only from a socket that a
// process with CAP_NET_ADMIN opened. This container has that capability
// because bluetoothd needs it.
//
// A failure here does not stop the start. bluetoothd then starts on a
// powered adapter, which works for every device that has no link at
// that moment, and the log line gives the kernel's error.

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/liken-sh/liken/bluetooth-operator/bluetoothd/mgmt"
)

// execFunc has the signature of syscall.Exec, so a test can replace it
// and record the call.
type execFunc func(path string, argv, env []string) error

// becomeBluetoothd powers every adapter off and then replaces this
// process with bluetoothd. main passes mgmt.Open as open, and a test
// passes a fake of the kernel.
//
// -n keeps bluetoothd in the foreground. By default it daemonizes,
// which would end the container's main process.
func becomeBluetoothd(open func() (io.ReadWriteCloser, error), log io.Writer, exec execFunc) error {
	powerOffAdapters(open, log)
	return exec(bluetoothdPath, []string{bluetoothdPath, "-n"}, os.Environ())
}

// powerOffAdapters logs one line for each adapter it powers off or
// cannot power off. It returns no error, because a failure does not stop the start.
func powerOffAdapters(open func() (io.ReadWriteCloser, error), log io.Writer) {
	channel, err := open()
	if err != nil {
		fmt.Fprintf(log, "start-bluetoothd: opening the management channel: %v, so bluetoothd starts each adapter as it is\n", err)
		return
	}
	defer channel.Close()
	indexes, err := readIndexList(channel)
	if err != nil {
		fmt.Fprintf(log, "start-bluetoothd: listing the adapters: %v, so bluetoothd starts each adapter as it is\n", err)
		return
	}
	for _, index := range indexes {
		if _, err := mgmt.Command(channel, mgmt.OpSetPowered, index, []byte{0}); err != nil {
			fmt.Fprintf(log, "start-bluetoothd: powering hci%d off: %v, so bluetoothd starts it as it is\n", index, err)
			continue
		}
		fmt.Fprintf(log, "start-bluetoothd: powered hci%d off, so bluetoothd starts it with no connection\n", index)
	}
}

// readIndexList returns the index of every adapter that the kernel
// lists. The reply is a 16-bit count followed by that many 16-bit
// indexes.
func readIndexList(channel io.ReadWriter) ([]uint16, error) {
	reply, err := mgmt.Command(channel, mgmt.OpReadIndexList, mgmt.IndexNone, nil)
	if err != nil {
		return nil, err
	}
	if len(reply) < 2 {
		return nil, fmt.Errorf("the index list reply holds %d bytes, and its adapter count needs 2", len(reply))
	}
	count := int(binary.LittleEndian.Uint16(reply))
	if len(reply) < 2+2*count {
		return nil, fmt.Errorf("the index list reply counts %d adapters and needs %d bytes, and it holds %d", count, 2+2*count, len(reply))
	}
	indexes := make([]uint16, count)
	for i := range indexes {
		indexes[i] = binary.LittleEndian.Uint16(reply[2+2*i:])
	}
	return indexes, nil
}
