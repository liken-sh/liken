# Working on the library operator

This directory holds the media library layer of a
[`liken`](https://liken.sh/) cluster. It
declares libraries as Kubernetes resources, keeps a catalog of what they
hold, and puts a media browser on the screens that
[`media-operator`](../media-operator/) plays to.
The documents, the manifests, and the source files are the
documentation, and the comments teach how the system works.

@../brand/voice.md

The voice rules in that file govern all prose in this directory,
comments included. The file is in `brand/`, the brand component
at the top of the repository.

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

One tag releases every component of the repository whose outputs
changed, and a push to `main` publishes a development build of each one
that changed. This directory's `package.toml` names its images, its
deploy directory, and the components it depends on.
`.github/workflows/component-library-operator.yaml` at the repository root is
generated from it; run `make workflows` there after you change it. The
`releases` skill under `.agents/skills` holds the version scheme and
the flow. Load it before you tag, publish an image, or pin a
development build.

The operator derives its companion images from its own pod's image, so
no other image name needs a version.
