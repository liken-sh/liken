# ci

`ci` is the program that runs the parts of CI that need to see every
component and the dependencies between them. You declare a
component's checks, dependencies, and outputs in its `package.toml`.
From those files, `ci` writes the workflows that test, build, publish,
and sign each component. On each workflow run, `ci` decides which
components to build and publish.

`ci` writes `.github/workflows/`, `.github/dependabot.yml`, and
`docker-bake.hcl`. In a workflow run, it plans what the run does. It
checks the pins, the dependencies, and the deploy bases. It publishes
a component's images and deploy artifact, scans the published images,
and writes the GitHub release for a tag. `ci` builds nothing itself.
Each check runs the component's own `make` target, and each image is
built by `docker buildx bake`. The `ci` component has no outputs of
its own to publish.

`ci` reads every `package.toml`, the `FROM` lines of every Dockerfile,
the `go.mod` and `Cargo.lock` files, and the images in
`toolchains/images.toml`. It queries ghcr.io and the release channel
to find what each component last published. In GitHub Actions, the
generated workflows run `ci`. On a workstation, `make workflows` at
the top of the repository runs it.

## The commands

- **`generate`** writes the workflows, the bake file, and
  `dependabot.yml`. With `-check`, it fails when the files on disk
  differ from what it would write.
- **`plan`** decides what a workflow run builds and publishes. It
  decides from the paths that changed and from each component's newest
  published version.
- **`bases`** checks the published image of each pinned base, and
  writes `docker-bake.override.hcl`.
- **`pins`**, **`deps`**, **`namespaces`**, and **`selectors`** are
  checks. `pins` checks the pins and downloads. `deps` checks each
  `[depends]` table against the code. `namespaces` checks the
  namespace of each object in a deploy base. `selectors` checks that
  each selector of a `DeviceClass` tests that an attribute exists
  before it reads the attribute.
- **`publish`** pushes one component's outputs.
- **`record`** writes the GitHub release for a tag.
- **`scan`** scans one published image with Trivy. **`scans`** lists
  the published images and the newest OS release for the scheduled
  scan.
- **`sites`**, **`reports`**, and **`profile`** collect the manuals and
  the coverage reports for the website.

`main.go` lists each command with its flags.

## `package.toml`

A `package.toml` has the tables `[package]`, `[depends]`, `[docs]`,
`[[jobs]]`, `[outputs]`, and `[[outputs.images]]`. The comments on
`Component` in `component.go` and on `Job` in `job.go` describe each
field.

MIT. See [LICENSE](LICENSE).
