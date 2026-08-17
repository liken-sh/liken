#!/bin/sh
set -eu

# The bus starts here, and the operator starts everything else.
#
# The bus is private to this pod. WirePlumber's device reservation
# speaks the system bus, and PipeWire's RTKit lookup falls back to it
# when no session bus answers. A pod has no host bus to join, so the
# image carries dbus and starts a bus with these processes alone on
# it. Neither daemon needs the bus to run: each one reports what it
# could not reach and continues, and no RTKit answers here, so
# PipeWire runs without a real-time priority.
#
# PipeWire and WirePlumber are the operator's own children, not this
# script's. The operator waits on them, so a daemon that dies ends the
# operator, which ends the container, and the kubelet restarts the
# whole set. That coupling is not optional. PipeWire holds the card,
# and this operator holds the card's exclusive claim, so an operator
# that outlived PipeWire would publish outputs it can no longer play
# and keep the hardware from a pod that could.

# dbus-daemon creates its socket in /run/dbus and does not create the
# directory.
mkdir -p /run/dbus

# dbus-daemon forks to the background by default, and the parent
# process exits 0 whether the bus came up or not. set -eu never sees a
# failure here, so this script waits for the socket itself instead of
# trusting the exit code.
dbus-daemon --system

SOCKET=/run/dbus/system_bus_socket
waited=0
while [ ! -S "$SOCKET" ]; do
    if [ "$waited" -ge 20 ]; then
        echo "dbus-daemon never created $SOCKET" >&2
        exit 1
    fi
    sleep 0.5
    waited=$((waited + 1))
done

exec /usr/local/bin/audio-operator "$@"
