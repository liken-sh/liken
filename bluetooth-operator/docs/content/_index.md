---
title: bluetooth.liken.sh
---

# `bluetooth.liken.sh`

`bluetooth-operator` is a Kubernetes DRA driver for
[`liken`](https://liken.sh/) clusters. It publishes each paired
Bluetooth controller as a device under the driver name
`bluetooth.liken.sh`. A pod claims one controller by its MAC address
and receives that controller's evdev nodes, and no other input
device on the machine. The operator runs as an ordinary workload:
its pod carries bluetoothd, so the `liken` system image carries no
BlueZ and no D-Bus.

The manual for the operator will publish here.

Until then, read the
[repository](https://github.com/liken-sh/bluetooth-operator) and the
[`liken` manual](https://liken.sh/docs/).
