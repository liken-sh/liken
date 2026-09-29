# Working on the media operator

This directory holds a media routing and playback layer for a
[`liken`](https://liken.sh/) cluster. It declares players, plays,
remotes, and keymaps as Kubernetes resources, and it reconciles them
into pods that claim the hardware operators' devices. The documents, the
manifests, and the source files are the documentation, and the comments
teach how the system works.

@../brand/voice.md

The voice rules in that file govern all prose in this directory,
comments included. The file is in `brand/`, the brand component
at the top of the repository.

`plans/00-design.md` is the design, and the `plans/` directory holds the
plans that build it. Code exists only where a plan calls for it.

## Errors include their source's text

An error that wraps a tool, a daemon socket, a bus answer, or a provider
includes that source's own text word for word: its `stderr`, its
response body, or its error string. The wrapped error and the status
field or record that the failure writes both include it, so a person
reads the cause from the log or the status without opening a shell.

Every operation a person causes, such as a press, a playback request, a
volume change, a focus move, or an edit the operator acts on, writes one
log line that says what triggered it, what was sent, and what came back,
and a loop a person does not see, such as a poll or a repeat, writes none.

## Releases and development builds

One tag releases every component of the repository whose outputs
changed, and a push to `main` publishes a development build of each one
that changed. This directory's `package.toml` names its images, its
deploy directory, and the components it depends on.
`.github/workflows/component-media-operator.yaml` at the repository root is
generated from it; run `make workflows` there after you change it. The
`releases` skill under `.agents/skills` holds the version scheme and
the flow. Load it before you tag, publish an image, or pin a
development build.

The operator derives its companion images from its own pod's image, so
no other image name needs a version.
