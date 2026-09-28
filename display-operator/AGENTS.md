# Working on display-operator

This directory holds a Kubernetes DRA driver for `liken` clusters. It runs
the Weston compositor and publishes a graphics card's monitor outputs as
devices under `display.liken.sh`. The Go files and the manifests are
the documentation, and the comments teach how the system works.

@../brand/voice.md

The voice rules in that file govern all prose here, comments included.
The file is in `brand/`, the brand component
at the top of the repository.

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
