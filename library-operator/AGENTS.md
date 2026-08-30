# Working on the library operator

This repository is the media library layer of a
[`liken`](https://liken.sh/) cluster. It declares libraries as
Kubernetes resources, keeps a catalog of what they hold, and puts a
media browser on the screens that
[`media-operator`](https://github.com/liken-sh/media-operator) plays to.
Like the rest of the `liken` project, it is written to be read: the
documents, manifests, and eventual source files are the documentation.

@docs/themes/brand/voice.md

The voice rules imported above govern all prose in this repository,
comments included, and they arrive with the brand theme submodule at
`docs/themes/brand`.

`plans/00-design.md` is the design, and `plans/README.md` indexes the
plans that build it. Code exists only where a plan calls for it. The
plans state contracts and leave the shape of the code to the person or
agent who builds each one.