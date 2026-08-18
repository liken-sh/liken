This repository is a Kubernetes DRA driver for `liken` clusters. It
publishes each paired Bluetooth controller as a device under the
driver name `bluetooth.liken.sh`, and its pod carries bluetoothd, so
the system image needs none. Like the rest of `liken`, it is written
to be read: the source files are the documentation, and the comments
teach how the system works.

@docs/themes/brand/voice.md

The voice rules imported above govern all prose in this repository,
comments included. They arrive with the brand theme submodule at
`docs/themes/brand`.
