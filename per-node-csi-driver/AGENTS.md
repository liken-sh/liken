# Working on the per-node CSI driver

This directory holds a Kubernetes CSI driver that gives a pod a directory
on the node it runs on, on a [`liken`](https://liken.sh/) cluster. The
directory stays on the node when the pod leaves, and a pod on another
node gets a directory of its own there. The Go files and the manifests
are the documentation, and the comments teach how the system works.

@docs/themes/brand/voice.md

The voice rules in that file govern all prose in this directory,
comments included. They arrive with the brand theme submodule at
`docs/themes/brand`.

`plans/00-design.md` is the design, and `plans/README.md` indexes the
plans that build it. Code exists only where a plan calls for it. A plan
states contracts and leaves the shape of the code to the person or agent
who builds it.

## Errors include their source's text

An error that wraps a tool, a daemon socket, a bus answer, or a provider
includes that source's own text word for word: its `stderr`, its
response body, or its error string. The wrapped error and the status
field or record that the failure writes both include it, so a person
reads the cause from the log or the status without opening a shell.

## The lab

Nothing in this directory is installed on a real cluster until a person
has seen it work. `lab/` boots one `liken` machine in QEMU from the
public release channel. Every drill runs there.

## Releases and development builds

Releases from this directory are paused while the components move into
one repository, under plan 69 in `plans/` at the repository root.
`.github/workflows/ci.yaml` at the root runs this directory's tests,
docs checks, and image builds, and it publishes nothing. Do not push a
tag. Step 2 of plan 69 brings back the releases and the development
builds for every component.
