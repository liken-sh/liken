# Working on the audio operator

This directory holds a Kubernetes DRA driver for the audio outputs of a
[`liken`](https://liken.sh/) machine. It publishes each physical output as a device under
the class `audio.liken.sh`, and it runs the sound server that the system
image does not contain. The Go files and the manifests are the
documentation, and the comments teach how the system works.

@../brand/voice.md

The voice rules in that file govern all prose in this repository,
comments included. The file is in `brand/`, the brand component
at the top of the repository.

## Errors include their source's text

An error that wraps a tool, a daemon socket, a bus answer, or a provider
includes that source's own text word for word: its `stderr`, its
response body, or its error string. The wrapped error and the status
field or record that the failure writes both include it, so a person
reads the cause from the log or the status without opening a shell.

## Releases and development builds

One tag releases every component of the repository whose outputs
changed, and a push to `main` publishes a development build of each one
that changed. This directory's `package.toml` names its images, its
deploy directory, and the components it depends on.
`.github/workflows/component-audio-operator.yaml` at the repository root is
generated from it; run `make workflows` there after you change it. The
`releases` skill under `.agents/skills` holds the version scheme and
the flow. Load it before you tag, publish an image, or pin a
development build.
