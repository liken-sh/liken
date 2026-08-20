# Working on the media operator

This repository is a media routing and playback layer for a
[`liken`](https://liken.sh/) cluster. It declares players, plays,
remotes, and keymaps as Kubernetes resources, and it
reconciles them into pods that claim the hardware operators'
devices. Like the rest of the `liken` project, it is written to be
read: the documents, manifests, and eventual Go files are the
documentation.

@docs/themes/brand/voice.md

The voice rules imported above govern all prose in this repository,
comments included, and they arrive with the brand theme submodule at
`docs/themes/brand`.

`plans/00-design.md` is the design, and `plans/README.md` indexes
the plans that build it. Code exists only where a plan calls for it.
