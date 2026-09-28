# Working on the library operator

This directory holds the media library layer of a
[`liken`](https://liken.sh/) cluster. It
declares libraries as Kubernetes resources, keeps a catalog of what they
hold, and puts a media browser on the screens that
[`media-operator`](https://github.com/liken-sh/media-operator) plays to.
The documents, the manifests, and the source files are the
documentation, and the comments teach how the system works.

@docs/themes/brand/voice.md

The voice rules in that file govern all prose in this directory,
comments included. They arrive with the brand theme submodule at
`docs/themes/brand`.

`plans/00-design.md` is the design, and `plans/README.md` indexes the
plans that build it. Code exists only where a plan calls for it. The
plans state contracts and leave the shape of the code to the person or
agent who builds each one.

## Errors include their source's text

An error that wraps a tool, a daemon socket, a bus answer, or a provider
includes that source's own text word for word: its `stderr`, its
response body, or its error string. The wrapped error and the status
field or record that the failure writes both include it, so a person
reads the cause from the log or the status without opening a shell.

Every operation at human scale prints one line that says its cause,
what was sent, and what came back, and names a title only by its
catalog id through `opaqueID` or `log::opaque`.

## Releases and development builds

Releases from this directory are paused while the components move into
one repository, under plan 69 in `plans/` at the repository root.
`.github/workflows/ci.yaml` at the root runs this directory's tests,
docs checks, and image builds, and it publishes nothing. Do not push a
tag. Step 2 of plan 69 brings back the releases and the development
builds for every component.

The operator derives its companion images from its own pod's image, so
no other image name needs a version.
