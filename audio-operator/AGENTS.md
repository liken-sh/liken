# Working on the audio operator

This repository is a Kubernetes DRA driver for the audio outputs of a
[`liken`](https://liken.sh/) machine. It publishes each physical
output as a device under the class `audio.liken.sh`, and it runs the
sound server the system image does not carry. Like the rest of the
liken project, it is written to be read: the Go files, manifests, and
workflows are the documentation.

@docs/themes/brand/voice.md

The voice rules imported above govern all prose in this repository,
comments included, and they arrive with the brand theme submodule at
`docs/themes/brand`.
