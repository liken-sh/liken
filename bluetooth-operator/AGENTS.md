This repository is a Kubernetes DRA driver for `liken` clusters. It
publishes each paired Bluetooth controller as a device under the
driver name `bluetooth.liken.sh`, and its pod runs bluetoothd, so
the system image needs none. Like the rest of `liken`, it is written
to be read: the source files are the documentation, and the comments
teach how the system works.

@docs/themes/brand/voice.md

The voice rules imported above govern all prose in this repository,
comments included. They arrive with the brand theme submodule at
`docs/themes/brand`.

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
      - https://github.com/liken-sh/bluetooth-operator//deploy?ref=<full 40-character sha>
    images:
      - name: ghcr.io/liken-sh/bluetooth-operator
        newTag: 2026.09.03-007-dev-003-abcdef01
      - name: ghcr.io/liken-sh/bluetoothd
        newTag: 2026.09.03-007-dev-003-abcdef01
      - name: ghcr.io/liken-sh/bluetooth-bondfetch
        newTag: 2026.09.03-007-dev-003-abcdef01

A git fetch by sha needs all forty characters, so the short sha in
the version is not enough for `ref=`. The CI run's step summary
prints both lines for that commit.
