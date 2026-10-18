package main

// Powering every adapter off before bluetoothd starts.
//
// When bluetoothd registers an adapter that is already powered, it
// reads the kernel's open connections and marks each of those devices
// Connected. It never attaches the input profile to such a link, so a
// remote whose link outlived the last bluetoothd is Connected with no
// HID device and sends no input. A bluetoothd that exits on TERM powers
// its adapters off first, and their links end with it. A bluetoothd
// that the kernel kills, or that crashes, leaves the adapters powered
// and their links up.
//
// So this program powers every adapter off at the kernel before it
// becomes bluetoothd. bluetoothd then registers each adapter unpowered,
// does not read the connections, and powers the adapter on itself,
// because main.conf sets AutoEnable. Each device that had a link
// connects again, the same as after any restart of the pod.
//
// The command goes over the Bluetooth management channel (package
// mgmt). bluetoothd manages every adapter that the channel lists, so
// this program powers off every one of them, not only the adapter
// that the pod claimed. The kernel accepts SET_POWERED only from a
// socket that a process with CAP_NET_ADMIN opened, and this container
// holds that capability for bluetoothd.
//
// A failure here does not stop the start. bluetoothd then starts on a
// powered adapter, which works for every device that has no link at
// that moment, and the log line names the kernel's answer.

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/liken-sh/bluetooth-operator/bluetoothd/mgmt"
)

// execFunc has the signature of syscall.Exec, so a test can see what
// would replace this process.
type execFunc func(path string, argv, env []string) error

// becomeBluetoothd powers every adapter off and then replaces this
// process with bluetoothd. open opens the management channel; the
// program passes mgmt.Open, and a test passes a fake kernel.
//
// -n keeps bluetoothd in the foreground, because it daemonizes by
// default and a daemonized process would leave this container with
// nothing running in it.
func becomeBluetoothd(open func() (io.ReadWriteCloser, error), log io.Writer, exec execFunc) error {
	powerOffAdapters(open, log)
	return exec(bluetoothdPath, []string{bluetoothdPath, "-n"}, os.Environ())
}

// powerOffAdapters powers off each adapter that the management channel
// lists, and writes one log line for each result. It returns nothing,
// because no failure here stops the start.
func powerOffAdapters(open func() (io.ReadWriteCloser, error), log io.Writer) {
	channel, err := open()
	if err != nil {
		fmt.Fprintf(log, "start-bluetoothd: %v; bluetoothd starts each adapter as it is\n", err)
		return
	}
	defer channel.Close()
	indexes, err := readIndexList(channel)
	if err != nil {
		fmt.Fprintf(log, "start-bluetoothd: listing the adapters failed: %v; bluetoothd starts each adapter as it is\n", err)
		return
	}
	for _, index := range indexes {
		if _, err := mgmt.Command(channel, mgmt.OpSetPowered, index, []byte{0}); err != nil {
			fmt.Fprintf(log, "start-bluetoothd: powering hci%d off failed: %v; bluetoothd starts it as it is\n", index, err)
			continue
		}
		fmt.Fprintf(log, "start-bluetoothd: powered hci%d off, so bluetoothd starts it with no connection\n", index)
	}
}

// readIndexList asks for the index of every adapter the kernel offers
// to the management channel. The return parameters are a count and
// then that many indexes.
func readIndexList(channel io.ReadWriter) ([]uint16, error) {
	reply, err := mgmt.Command(channel, mgmt.OpReadIndexList, mgmt.IndexNone, nil)
	if err != nil {
		return nil, err
	}
	if len(reply) < 2 {
		return nil, fmt.Errorf("the index list reply holds %d bytes", len(reply))
	}
	count := int(binary.LittleEndian.Uint16(reply))
	if len(reply) < 2+2*count {
		return nil, fmt.Errorf("the index list reply names %d adapters in %d bytes", count, len(reply))
	}
	indexes := make([]uint16, count)
	for i := range indexes {
		indexes[i] = binary.LittleEndian.Uint16(reply[2+2*i:])
	}
	return indexes, nil
}
