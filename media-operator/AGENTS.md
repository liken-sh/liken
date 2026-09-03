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

## Releases and development builds

A pushed tag is a release. It names a version in liken's calendar
scheme, `2026.09.03-007`, and `release.yaml` builds every image and
pushes them under that tag and `:latest`.

A push to main is a development build. `release.yaml` runs the same
tests, builds the same images, and pushes them under the most recent
release tag, from `git describe`, plus a suffix:
`2026.09.03-007-dev-003-abcdef01` is three commits past that
release, at commit `abcdef01`. Every image in the repository
carries the same version, and `:latest` never moves. The suffix
sorts after its release and before the next one, and the tag shape
check in `release.yaml` never accepts it.

To run a development build, pin the manifests to the full sha of
the commit and the image to the version:

    resources:
      - https://github.com/liken-sh/media-operator//deploy?ref=<full 40-character sha>
    images:
      - name: ghcr.io/liken-sh/media-operator
        newTag: 2026.09.03-007-dev-003-abcdef01

A git fetch by sha needs all forty characters, so the short sha in
the version is not enough for `ref=`. The CI run's step summary
prints both lines for that commit. The operator derives its
companion images from its own pod's image, so nothing else names a
version.
