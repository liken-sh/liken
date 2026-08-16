#!/bin/sh
set -eu

# Three processes start here, in this order: a D-Bus bus, bluetoothd,
# and the operator.
#
# The bus belongs to this pod. bluetoothd's whole API is the D-Bus
# system bus, and a pod has no host bus to join, so the image carries
# dbus and starts a bus with one service and one client on it. This is
# also why liken's system image needs no D-Bus: the bus exists for one
# daemon, in the image that carries that daemon.
#
# The operator is the container's main process, so the container ends
# when the operator ends. bluetoothd runs behind it, and the operator
# watches BlueZ's bus name: a bluetoothd that leaves the bus ends the
# operator, which ends the container, and the kubelet restarts the
# pair. That coupling is not optional. bluetoothd owns the HID
# sessions, and killing it disconnects every controller at once, so an
# operator that outlived it would publish devices that no pod can use.

# BlueZ's ClassicBondedOnly setting, written here rather than baked
# into the image, because its two values are a security choice that
# belongs to the moment.
#
# true, the default, is BlueZ's own default and the fix for
# CVE-2023-45866: with it off, an attacker in radio range can open an
# HID channel without any bond and inject keystrokes into the machine.
# false is what a DualSense needs for its first pairing, because the
# controller opens its HID channel before the bond registers. Turn it
# off for the pairing, pair, and turn it back on. The bonds persist on
# their volume, so the flip costs one restart and nothing else.
: "${BLUETOOTH_CLASSIC_BONDED_ONLY:=true}"
cat > /etc/bluetooth/input.conf <<EOF
[General]
ClassicBondedOnly=${BLUETOOTH_CLASSIC_BONDED_ONLY}
EOF

# The bus lives at a path of this operator's own, not at dbus's
# packaged /run/dbus, and every process here finds it through
# DBUS_SYSTEM_BUS_ADDRESS. bluetoothd and bluetoothctl read that
# variable through libdbus, and the operator reads it through godbus.
#
# The path is a contract, chosen now while moving it costs nothing.
# Nothing mounts it today. A later capability that has to reach this
# bluetoothd over its bus, such as a PipeWire that handles Bluetooth
# audio in another pod, needs a path that was stable before it
# arrived, and a bus under /run/dbus cannot be shared without moving
# the packaged one out from under dbus itself.
#
# The directory is the mount unit, never the socket file.
# dbus-daemon unlinks and recreates the socket at every start, so a
# mount of the file alone would pin the inode the daemon deleted, and
# whatever mounted it would hold a socket that nothing listens on.
#
# /var/run is /run under its older name, which is the machine's
# runtime tmpfs, so the socket lasts one boot and no cleanup has to
# remove it.
BUS_DIR=/var/run/bluetooth.liken.sh/dbus
export DBUS_SYSTEM_BUS_ADDRESS="unix:path=${BUS_DIR}/system_bus_socket"
mkdir -p "$BUS_DIR"

# dbus-daemon writes the pid file that its packaged system.conf names,
# and it does not create that directory either.
mkdir -p /run/dbus

# Without --nofork, dbus-daemon returns only after the bus socket
# accepts connections, so bluetoothd never races it. --address
# overrides the listen address in the packaged system.conf and leaves
# every policy rule in it, including the one that lets root own
# org.bluez.
dbus-daemon --system --address="$DBUS_SYSTEM_BUS_ADDRESS"

# -n keeps bluetoothd in the foreground, because it daemonizes by
# default and a daemonized process gives this script nothing to hold.
/usr/libexec/bluetooth/bluetoothd -n &

exec /usr/local/bin/bluetooth-operator "$@"
