# Working on the audio operator

This directory holds a Kubernetes DRA driver for the audio outputs of a
[`liken`](https://liken.sh/) machine. It publishes each physical output as a device under
the class `audio.liken.sh`, and it runs the sound server that the system
image does not contain. The Go files, the manifests, and the workflows
are the documentation, and the comments teach how the system works.

@docs/themes/brand/voice.md

The voice rules in that file govern all prose in this repository,
comments included. They arrive with the brand theme submodule at
`docs/themes/brand`.

## Errors include their source's text

An error that wraps a tool, a daemon socket, a bus answer, or a provider
includes that source's own text word for word: its `stderr`, its
response body, or its error string. The wrapped error and the status
field or record that the failure writes both include it, so a person
reads the cause from the log or the status without opening a shell.

## Releases and development builds

Releases from this directory are paused while the components move into
one repository, under plan 69 in `plans/` at the repository root.
`.github/workflows/ci.yaml` at the root runs this directory's tests,
docs checks, and image builds, and it publishes nothing. Do not push a
tag. Step 2 of plan 69 brings back the releases and the development
builds for every component.
