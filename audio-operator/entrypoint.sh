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
# that outlived PipeWire would publish outputs that no pod can play
# through and keep the hardware from a pod that could.

# dbus-daemon creates its socket in /run/dbus and does not create the
# directory.
mkdir -p /run/dbus

# Without --nofork, dbus-daemon returns only after the bus socket
# accepts connections, so nothing races it.
dbus-daemon --system

exec /usr/local/bin/audio-operator "$@"
