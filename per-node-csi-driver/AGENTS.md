# Working on the per-node CSI driver

This directory holds a Kubernetes CSI driver that gives a pod a directory
on the node it runs on, on a [`liken`](https://liken.sh/) cluster. The
directory stays on the node when the pod leaves, and a pod on another
node gets a directory of its own there. The Go files and the manifests
are the documentation, and the comments teach how the system works.

@../brand/voice.md

The voice rules in that file govern all prose in this directory,
comments included. The file is in `brand/`, the brand component
at the top of the repository.

`plans/00-design.md` is the design, and the `plans/` directory holds the
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

One tag releases every component of the repository whose outputs
changed, and a push to `main` publishes a development build of each one
that changed. This directory's `package.toml` names its images, its
deploy directory, and the components it depends on.
`.github/workflows/component-per-node-csi-driver.yaml` at the repository root is
generated from it; run `make workflows` there after you change it. The
`releases` skill under `.agents/skills` holds the version scheme and
the flow. Load it before you tag, publish an image, or pin a
development build.
