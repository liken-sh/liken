// start-rescan works around two kernel bugs that leave a bonded
// Bluetooth Low Energy remote unable to reconnect, with no log line
// anywhere, until something powers the radio off and on.
//
// # The kernel bugs
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
// The second bug takes the device off that list. When a link comes
// up, le_conn_complete_evt in net/bluetooth/hci_event.c takes the
// device off the list, and hci_disconn_complete_evt puts it back when
// the link ends. When the host ends a link, hci_abort_conn_sync in
// net/bluetooth/hci_sync.c sends HCI Disconnect and waits two seconds
// for the controller's Disconnection Complete. When the remote has
// stopped answering, the controller can take longer. For example, a
// remote can send a directed advertisement and go quiet. The kernel
// ends the link when its read of the remote's features times out, and
// the controller reports the link closed only later, with LL Response
// Timeout. When the two seconds run out, hci_abort_conn_sync ends the
// link through hci_conn_failed. hci_connect_le_scan_cleanup looks for the
// device on the list, does not find it, and returns, so the device
// stays off the list. The kernel deletes the connection, sends no
// Device Disconnected event, and ignores the Disconnection Complete
// when it arrives. bluetoothd still reports the device Connected,
// the scan stays off, and a Device1.Connect does not help:
// hci_explicit_conn_params_set puts an auto connect device back on the
// list only when its action is not auto connect.
//
// Both bugs are the same in Linux 7.3.1. The first one is the same in
// Linux 6.12 and on Linus Torvalds' master and bluetooth-next as of
// 2026-10-17, and no patch for it was on the linux-bluetooth
// patchwork then. liken boots Ubuntu's mainline kernel packages,
// which this project does not build, so the fix cannot ship as a
// kernel patch. When an upstream kernel fixes both and liken boots
// that kernel, delete this program, its container in
// deploy/operator.yaml, and the pages of the manual that name it.
//
// # The workaround
//
// This program reads the HCI monitor channel, the kernel's copy of
// every HCI command, HCI event, and Management API command on the
// machine. btmon reads the same channel. For each adapter, the program
// tracks:
//
//   - the devices that bluetoothd added with the auto connect action
//     and has not removed,
//   - the LE Create Connection attempt in progress, and the device it
//     is for,
//   - the device at the other end of each link that is up,
//   - and when the kernel last turned the scan on.
//
// When an attempt to an auto connect device fails, or the host sends
// HCI Disconnect for a link to one, the program starts a deadline of
// rescanSettle. When the kernel starts the scan again by itself, it
// does so within about two seconds, so a scan before the deadline
// cancels it, and so does a new attempt. When the deadline passes with
// no scan, the program sends a repair, which is Add Device for the
// device twice: first with the
// action that allows an incoming connection, then with the auto
// connect action that bluetoothd sent. The kernel ignores an Add
// Device that repeats the action it holds (hci_conn_params_set in
// net/bluetooth/mgmt.c), and each change of action puts the device
// back on the list. Each command ends in the call to
// hci_update_passive_scan in add_device_sync, which starts the scan.
// No connection drops, and the next press reconnects the remote.
//
// A failed attempt to a device that bluetoothd did not add for auto
// connect, such as a Device1.Connect to a speaker, takes the kernel's
// explicit cleanup, which rescans by itself. The program never adds
// such a device, because the command would turn on auto connect for a
// device that bluetoothd did not choose.
//
// bluetoothd ignores the Device Added event that each command causes,
// so its own record of the device does not change. When bluetoothd
// sends Remove Device, as it does on an unpair, the program cancels
// the device's deadline, so a repair never turns auto connect on again
// for a device that bluetoothd removed.
//
// # The order of the containers
//
// The program learns the auto connect devices from bluetoothd's own
// Add Device commands, which bluetoothd sends when it registers an
// adapter, and the Management API has no command that lists them. The
// program receives only the commands sent after it opens the monitor
// channel, so its container starts before bluetoothd's. When it restarts
// alone, it tracks only the devices that bluetoothd adds after the
// restart, until the pod restarts.
//
// # Where it runs
//
// The monitor channel needs CAP_NET_RAW, and Add Device needs
// CAP_NET_ADMIN. This container has those two capabilities and no
// others, so the operator's container needs none. The program logs
// one line for each repair it sends, with the device, the failed
// attempt's status or the ended link, and the kernel's response.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liken-sh/liken/bluetooth-operator/bluetoothd/mgmt"
)

// rescanSettle is how long the program waits for the kernel to start
// the scan by itself before it sends a repair. When a failed
// connection still has references, the kernel sends LE Create
// Connection Cancel only when the last one drops (hci_conn_drop and
// hci_conn_timeout), and rescans after the cancel, so the program
// starts the wait again at the cancel. A remote that stops answering
// can wait about this long before it reconnects.
const rescanSettle = 5 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := run(ctx, openMonitor, mgmt.Open, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "start-rescan: %v\n", err)
		os.Exit(1)
	}
}

// run reads the monitor channel until ctx ends or the channel fails.
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
