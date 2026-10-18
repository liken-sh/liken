// start-rescan works around a kernel bug that leaves a bonded
// Bluetooth Low Energy remote unable to reconnect, with no log line
// anywhere, until something powers the radio off and on.
//
// # The kernel bug
//
// bluetoothd registers each bonded device that reconnects on its own,
// such as a HID remote, with the kernel's Add Device command and the
// auto connect action. The kernel then keeps the radio in a passive
// scan while any such device is disconnected. When the device
// advertises, the kernel stops the scan, because some controllers
// cannot scan and connect at once, and sends LE Create Connection
// (hci_le_create_conn_sync in net/bluetooth/hci_sync.c). The scan has
// to start again after the attempt, whether it succeeds or fails.
//
// After a failed attempt, it does not always start again. Linux 7.2.6
// fails an attempt through hci_conn_failed, which reaches
// hci_connect_le_scan_cleanup in net/bluetooth/hci_conn.c. For an auto
// connect device, that function returns before its call to
// hci_update_passive_scan, because the call belongs to the cleanup of
// an explicit connect. The device stays on the kernel's list of
// devices to reconnect, and the scan stays off. The attempt fails
// without a log line in each of these cases:
//
//   - The controller refuses LE Create Connection in its Command
//     Status.
//   - The controller reports LE Connection Complete with an error,
//     such as 0x3e, Connection Failed to be Established. A remote that
//     stops advertising as the radio connects causes this one. The
//     kernel's own rescan then runs while the failed connection still
//     exists, and does nothing.
//   - The attempt times out after 20 seconds, the kernel cancels it,
//     and the controller sends no final LE Connection Complete.
//
// Any later call to hci_update_passive_scan starts the scan again: a
// power-on, an Add Device, or another device's connect or disconnect.
// A radio with one bonded remote has no other device, so the scan
// stays off until a person restarts the pod. The remote's presses
// reach no receiver, and nothing logs.
//
// The early return is the same in Linux 6.12 and on Linus Torvalds'
// master and bluetooth-next as of 2026-10-17, and no patch for it was
// on the linux-bluetooth patchwork then. liken boots Ubuntu's mainline
// kernel packages, which this project does not build, so the fix
// cannot ship as a kernel patch. When an upstream kernel fixes it and
// liken boots that kernel, delete this program, its container in
// deploy/operator.yaml, and the pages of the manual that name it.
//
// # The workaround
//
// This program reads the HCI monitor channel, the kernel's copy of
// every HCI command, HCI event, and Management API command on the
// machine. btmon reads the same channel. The program keeps three
// facts for each adapter:
//
//   - the devices that bluetoothd added with the auto connect action
//     and has not removed,
//   - the LE Create Connection attempt in progress, and the device it
//     is for,
//   - and when the kernel last turned the scan on.
//
// When an attempt to an auto connect device fails, the program starts
// a deadline of rescanSettle. The kernel starts the scan again in a
// few milliseconds when it works, so a scan within the deadline
// cancels it, and so does a new attempt. When the deadline passes with
// no scan, the program sends the same Add Device command that
// bluetoothd sent for the device, with the same action. The kernel
// already holds that entry, so the command changes no setting. Its
// one effect is the call to hci_update_passive_scan at the end of
// add_device_sync in net/bluetooth/mgmt.c, which starts the scan. No
// connection drops, and the next press reconnects the remote.
//
// A failed attempt to a device that bluetoothd did not add for auto
// connect, such as a Device1.Connect to a speaker, takes the kernel's
// explicit cleanup, which rescans by itself. The program never adds
// such a device, because the command would turn on auto connect for a
// device that bluetoothd did not choose.
//
// bluetoothd does not handle the Device Added event that the command
// causes, so bluetoothd keeps its own view of the device unchanged.
//
// # The order of the containers
//
// The program learns the auto connect devices from bluetoothd's own
// Add Device commands, which bluetoothd sends when it registers an
// adapter. The Management API has no command that lists them. So the
// container starts before bluetoothd's, the same as the btmon
// container and for the same reason. When this container restarts
// alone, it knows only the devices that bluetoothd adds after the
// restart, until the pod restarts.
//
// # Where it runs
//
// The monitor channel needs CAP_NET_RAW, and Add Device needs
// CAP_NET_ADMIN. The container holds those two and nothing else, so
// the operator's container keeps no capability. The program writes
// one log line for each Add Device it sends, with the device, the
// status of the failed attempt, and the kernel's answer.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liken-sh/bluetooth-operator/bluetoothd/mgmt"
)

// rescanSettle is how long the program waits after a failed attempt
// for the kernel to start the scan by itself. The kernel's own rescan
// takes milliseconds. When a failed connection lingers, the kernel
// ends it with a Create Connection Cancel when its last reference
// drops (hci_conn_drop and hci_conn_timeout), and that cancel starts
// the wait again. A person pressing a remote that does not answer
// waits about this long before the remote can reconnect.
const rescanSettle = 5 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := run(ctx, openMonitor, mgmt.Open, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "start-rescan: %v\n", err)
		os.Exit(1)
	}
}

// run reads the monitor channel until ctx ends or the channel fails,
// and adds a device again each time the kernel leaves its scan off.
func run(ctx context.Context, open func() (io.ReadCloser, error), management func() (io.ReadWriteCloser, error), log io.Writer) error {
	monitor, err := open()
	if err != nil {
		return err
	}
	fmt.Fprintln(log, "start-rescan: reading the HCI monitor channel")
	packets, failed := readPackets(ctx, monitor)
	go func() {
		<-ctx.Done()
		monitor.Close()
	}()
	w := newWatcher(management, log)
	return w.run(ctx, packets, failed)
}
