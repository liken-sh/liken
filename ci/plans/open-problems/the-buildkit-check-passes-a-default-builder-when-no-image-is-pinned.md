# The BuildKit check passes a default builder when no image is pinned

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19. A review of the comments against the code found it.

The comment on `buildkitKey` in `ci/buildkit.go` says that
`checkBuildKit` fails a `docker/setup-buildx-action` step that sets
another image in `driver-opts`, or none. `ci pins` runs the check
(`ci/main.go`), so the check keeps a workflow off
`moby/buildkit:buildx-stable-1`, the default image of buildx, which
moves to each BuildKit release.

`checkBuildKit` reads the image from the `buildkit` entry of
`toolchains/images.toml` and passes it to `unpinnedBuilders`. When
`images.toml` has no `buildkit` entry, the image is `""`.
`unpinnedBuilders` compares the image of each step with a `switch`
whose first case is `case image:`. A step with no image in
`driver-opts` gives `""`, matches that first case, and passes. So with
the entry missing, `ci pins` passes every step that runs the moving
default, and it fails only a step that names an image.

The comment on `checkBuildKit` states this behavior, so it can be
deliberate. The generator refuses the same case: `readBuildKit`
returns an error when the entry is missing, so `make workflows` fails
for a component that builds images. A composite action under
`.github/actions/`, or any workflow that the generator does not write,
has no such guard. `buildkit_test.go` tests `unpinnedBuilders` only
with a pinned image, `fixtureBuildKit`.

## The reproduction

The test wrote a tree whose `toolchains/images.toml` has a `[build]`
table with no `buildkit` entry, and a composite action under
`.github/actions/build-setup/` that runs `setup-buildx-action` with no
`driver-opts`. It called `checkBuildKit` on the tree and asserted at
least one finding. It failed with:

```
the findings are empty, want one for the step that runs moby/buildkit:buildx-stable-1
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When `images.toml` pins no BuildKit image, should `ci pins` report the
missing pin, or report each build step that names no image?

Whether a missing `buildkit` entry is itself a finding of `ci pins`,
as it is an error of the generator, or whether `unpinnedBuilders`
reports a step with no image before it compares with the pin, so that
both cases fail.
