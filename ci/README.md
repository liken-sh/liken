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

Each task that the paragraphs above name is one command of `ci`,
such as `generate`, `plan`, `pins`, or `publish`. The doc comment at the top of `main.go`
lists each command with its flags. `generate -check` fails when the
generated files on disk differ from what `generate` would write, and
CI runs it on each push.

## `package.toml`

The comments on `Component` in `component.go` and on `Job` in `job.go`
describe each table and field of a `package.toml`.

MIT. See [LICENSE](LICENSE).
