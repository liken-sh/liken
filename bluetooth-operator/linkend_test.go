package main

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// sampleRemote is the remote address that the manual's examples use.
const sampleRemote = "C2:11:22:33:44:55"

// noAddress is how the zero bonds.Address prints, which
// disconnectedFrom returns for a signal it does not read.
const noAddress = "00:00:00:00:00:00"

// disconnected builds the signal that BlueZ emits on a device's object
// when its link ends.
func disconnected(path, reason string) *dbus.Signal {
	return &dbus.Signal{
		Sender: ":1.3",
		Path:   dbus.ObjectPath(path),
		Name:   "org.bluez.Device1.Disconnected",
		Body:   []any{reason, "Connection timed out"},
	}
}

func TestDisconnectedFromReadsTheDeviceAndTheReason(t *testing.T) {
	cases := []struct {
		name   string
		signal *dbus.Signal
		device string
		reason string
		read   bool
	}{
		{
			name:   "a link that timed out",
			signal: disconnected("/org/bluez/hci0/dev_C2_11_22_33_44_55", blueZReasonTimeout),
			device: sampleRemote,
			reason: blueZReasonTimeout,
			read:   true,
		},
		{
			name:   "a link the device closed",
			signal: disconnected("/org/bluez/hci0/dev_C2_11_22_33_44_55", blueZReasonRemote),
			device: sampleRemote,
			reason: blueZReasonRemote,
			read:   true,
		},
		{
			name:   "a property change",
			signal: &dbus.Signal{Path: "/org/bluez/hci0/dev_C2_11_22_33_44_55", Name: "org.freedesktop.DBus.Properties.PropertiesChanged", Body: []any{deviceInterface}},
			device: noAddress,
		},
		{
			name:   "a signal on an object that is not a device",
			signal: disconnected("/org/bluez/hci0", blueZReasonTimeout),
			device: noAddress,
		},
		{
			name:   "a signal with no reason",
			signal: &dbus.Signal{Path: "/org/bluez/hci0/dev_C2_11_22_33_44_55", Name: "org.bluez.Device1.Disconnected"},
			device: noAddress,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			device, reason, read := disconnectedFrom(c.signal)

			if read != c.read || device.Directory() != c.device || reason != c.reason {
				t.Errorf("disconnectedFrom returned %t, %s, %q, want %t, %s, %q", read, device.Directory(), reason, c.read, c.device, c.reason)
			}
		})
	}
}

// linkEnds keeps the newest reason for each device, and a pass that
// wrote one reason does not forget a newer one that arrived during the
// write.
func TestLinkEndsHoldTheNewestReasonUntilAPassWritesIt(t *testing.T) {
	device := testAddress(t, sampleRemote)
	ends := newLinkEnds()

	ends.record(device, blueZReasonRemote)
	ends.record(device, blueZReasonTimeout)
	read := ends.reason(device)
	ends.record(device, blueZReasonLocal)
	ends.forget(device, read)
	newer := ends.reason(device)
	ends.forget(device, newer)

	if read != blueZReasonTimeout {
		t.Errorf("the pass read %q, want the newest reason %q", read, blueZReasonTimeout)
	}
	if newer != blueZReasonLocal {
		t.Errorf("after the write the record holds %q, want the reason that arrived during it, %q", newer, blueZReasonLocal)
	}
	if left := ends.reason(device); left != "" {
		t.Errorf("after the second write the record holds %q, want nothing", left)
	}
}
