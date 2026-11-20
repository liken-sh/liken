---
name: new-component
description: Creates a new component of the liken repository - a Go operator, a CSI driver, a Rust crate or server, or a pinned image - from the design and its plan, through the files that ci reads and the lists outside the directory that name each component, to the local checks that let the first push to main pass. Use when asked to create a new operator, add a component, start a new driver, bring a project into the repository, or scaffold a component's directory.
---

# Create a component

A component is a top-level directory with a `package.toml`. `ci/`
reads that file and writes the component's CI workflow, its bake
targets, and its Dependabot entries. So a new directory that is set up
like its siblings builds, tests, and publishes on its first push to
`main`. A component that is set up differently fails on that push, or
it passes and then behaves differently from every other component.

The section "Code layout" of the root `AGENTS.md` sets the layout of
a component: the module, the program, `deploy/`, and `make test`.
This skill gives the order of the work, and the files outside the
directory that a new component changes. Each subject has its own
skill, and this skill names the one to load at the step that needs
it.

## Design it with the maintainer first

A request to create a component, such as "set up the component", is
a request to design it. Do not write the component's code, its
`package.toml`, or its `deploy/` yet. Do these steps:

1. Talk the design through with the maintainer, one question at a
   time. Settle the name of each kind, which work belongs in the new
   component and which stays in an existing one, whether it ships
   separately from the OS, and which components it depends on.
2. Write the plan. Load the `plans` skill first, and use the header
   that its section "Write a plan" gives. Name each guide of the
   manual in the plan, because each guide becomes a skill for the
   component's users.
3. Start the files of the component when the maintainer agrees to the
   plan.

A component has code only where a plan calls for it. So the first
files of a component are its plan and its description:

- `plans/00-design.md`, the design of the component as a whole, and
  `plans/README.md`, copied from a sibling.
- `README.md` and `AGENTS.md`, in the shapes that "The README and
  AGENTS.md" below describes.
- A root plan, only when the design crosses components. The detail
  goes in each component's own plan.

These files can go in a commit of their own, with no `package.toml`.
`astrophotography-operator` and `observatory-operator` started that
way. `ci` reads only directories that have a `package.toml`, so the
stub runs no job and publishes nothing.

## Choose the sibling to copy

Choose the sibling by the kind of workload that the component runs
and by what it claims. Then confirm the choice: read the sibling's
`deploy/` for the workload's `kind:` and any `ResourceClaimTemplate`,
and its `package.toml` for its jobs and its outputs.

- **An operator that runs as a `Deployment` and claims no device**:
  `people-operator`, which is small. It has leader election, watches
  through `kubernetes/informer`, and creates and follows short-lived
  pods. It has a fake API server in `fakeapi_test.go`, a manual with a
  generated reference page, and `deploy_test.go` for the base.
- **A device operator**, which is a DRA driver that runs as a
  `DaemonSet` and claims a device that the OS publishes:
  `bluetooth-operator`. Its `deploy/deviceclasses.yaml` selects the
  devices that the driver `liken.sh` publishes for one kernel driver.
  Its `deploy/operator.yaml` has the `ResourceClaimTemplate` that
  gives each pod the device of its own node. The tree is large, so
  copy these files and the shape of its DRA code, not every file.
- **A CSI driver**: `per-node-csi-driver`, whose `deploy/` has a
  `CSIDriver` and a node `DaemonSet`. It also has a `smoke/` script
  and a `lab/` of drills.
- **A Rust crate with no output**: `media-screen`.
- **A Rust server with an image and a `deploy/`**: `transect`, which
  has no manual. Its binary links glibc, so a closure script copies
  the binary and its libraries onto `scratch`.
- **A pinned image of upstream programs**: `astrometry`, with
  `version`, `revision`, and `[package.upstream]` in its
  `package.toml`. The rules of the tag and the revision are in the
  "Pinned components" section of the `releases` skill and in the
  `bump-components` skill.

Copy the sibling file by file, including `LICENSE` and
`.pre-commit-config.yaml`. Change each name, path, and comment for the
new component. `sed` changes the names in `.gitignore` and the
`Makefile`.

## Build the component

Do the steps in this order, because each step reads what the step
before it wrote.

1. **The module.** Input: the sibling's `go.mod`, or its `Cargo.toml`
   and `rust-toolchain.toml`. Write the module as
   `github.com/liken-sh/liken/<directory>`, with the Go version of the
   `golang` entry of `toolchains/images.toml`. Add a `replace` for each
   module of the tree that it imports. Then run `go mod tidy`, and
   compare each dependency's version with the siblings' versions.
   `go mod tidy` can move a module, such as the Kubernetes modules,
   past the version that the siblings use. Set each such module back
   to the siblings' version. `ci pins` fails when two modules use
   different versions of one dependency. Result: a module whose
   versions are the same as the siblings'.
2. **The code and the tests.** Load the `operators` skill and the
   `testing` skill first. Result: `make test-go` passes, or
   `make test` for a Rust workspace. Set the floor in
   `.testcoverage.yml`, or `COVERAGE_FLOOR` in a Rust `Makefile`, just
   under what the run measured.
3. **`deploy/`.** Input: the sibling's kustomization, with its
   `NamespaceTransformer`. The base refers to each image as
   `ghcr.io/liken-sh/<image>` with `newTag: latest`. `:latest` exists
   only after the component's first release. Until then, a person pins
   a development build, as the "Pin a component" section of the
   `releases` skill describes. A device `DaemonSet` needs the node
   affinity in the section "A device DaemonSet stays off a node
   labeled none" of the `operators` skill. Result: `kubectl kustomize
   deploy` builds, and a test reads the manifests of the base, as
   `people-operator/deploy_test.go` does.
4. **The `Dockerfile`.** Load the `images` skill first. Also load the
   `pins` skill when the image or the tree includes a file from
   outside the repository, such as the checked-in htmx of `transect`.
   Copy the sibling's `.dockerignore` when it has one, and list in it
   each file that no build reads, as `bluetooth-operator/.dockerignore`
   does. The section "The recipe of a pinned component" of the
   `images` skill explains how `.dockerignore` changes what a build
   context includes. Result: a Dockerfile in the repository's form
   for its kind of program. The `package.toml` of the next step lists
   its build contexts.
5. **The manual.** Copy the sibling's `docs/`, and load the `manuals`
   skill. Change these:
   - the module path of `docs/go.mod`
   - `baseURL` and `params.repository` in `hugo.yaml`
   - `BASE` and the `crdref` target in `docs/Makefile`
   - the exceptions in `docs/manual_test.go`, which list the files of
     `deploy/` that the site serves

   Run `make -C docs generate` and `make -C docs skills`. Result: the
   reference pages and `skills/`, which are committed. A component with
   no manual, such as `transect` or `media-screen`, has no `docs/` and
   no `[docs]` table. Its `README.md` tells a person what they need to
   run it.
6. **`package.toml`.** Input: the sibling's file. Result: a file that
   `ci` loads.
   - `name` is the directory's name, and `license` is `MIT`. `ci`
     fails on a name that differs from the directory, and on a key
     that its schema does not define.
   - `[depends]` lists each component that the build reads: a module
     that `go.mod` replaces, a crate that `Cargo.toml` uses by path, a
     build context, and an image of the repository that a `FROM` or a
     `COPY --from` refers to. A Dockerfile that builds from an image of
     `toolchains/images.toml`, as in `FROM golang`, makes the
     component depend on `toolchains`. So a change to that entry
     releases the component. `ci deps` fails on a dependency that the
     list does not include.
   - `[docs] prefix` sets the manual's address,
     `https://liken.sh/<prefix>/`.
   - Give the component at least one job. GitHub rejects a workflow
     with no jobs as an invalid file. A Go component has the `go`,
     `docs`, and `prek` jobs of `people-operator`. A Rust component
     has the `rust` and `prek` jobs of `media-screen`.
   - Each `[[outputs.images]]` entry has a `description` of one line,
     with no Markdown and no em-dash. `ci` writes it to the label that
     ghcr.io shows, and fails on a description that breaks a rule.
7. **The `README.md` and `AGENTS.md`.** Update the stub of the first
   commit for the code that exists. "The README and AGENTS.md" below
   describes the shapes.
8. **The files outside the component.** "Files outside the component"
   below describes how to find each one.

## The README and AGENTS.md

`README.md` is for a person, and is under 100 lines.
`audio-operator/README.md` and `media-operator/README.md` have this
shape:

- An opening paragraph on what the component lets you do.
- A paragraph on how it works, and on what it does not do.
- A paragraph on how it fits with the other components. A hardware
  operator links to
  `https://liken.sh/docs/concepts/claiming-hardware/` here.
- The kinds, each with a line or two.
- One short example, such as a manifest of a kind.
- A link to the manual at `https://liken.sh/<prefix>/`, a link to
  `plans/`, and the line `MIT. See [LICENSE](LICENSE).`

`AGENTS.md` is for an agent that changes the component. Every session
in the directory loads the root `AGENTS.md` with it, so it repeats
nothing that the root file says. The rules in `brand/instructions.md`
apply to it. `audio-operator/AGENTS.md` and
`firmware-operator/AGENTS.md` have this shape:

- The title `# Working on <component>`. Then a sentence on the type of
  the component, such as "`firmware-operator` is a Go Kubernetes
  operator", and the sentence "`README.md` says what it is for and
  describes its kinds."
- `## How it runs`: the processes, and how information and control
  move between them.
- `## Design`: one or two short paragraphs that describe the design
  and give the reason for each choice. Include what the component
  owns and how it talks to the components next to it. Write
  paragraphs, not a list of rules. The full reasons are in `plans/`.
- `## Finding things`: what a file name does not tell the reader.

## Files outside the component

`make workflows` at the top of the repository writes some of the
files that name each component, and you edit the others.

`make workflows` runs `cd ci && go run . generate -root ..`. That
command writes the files of `.github/workflows/`, `docker-bake.hcl`,
and `.github/dependabot.yml` from every `package.toml`, every `FROM`
line, and every `go.mod` and `Cargo.lock`. The component gets its own
`.github/workflows/component-<name>.yaml`. `scan.yaml` lists an image
only after its first publish.

To find the files that you edit by hand, search the repository for
the name of the sibling that you copied. Leave out the sibling's own
directory and every `plans/` directory:

```sh
git grep -l <sibling> -- . ':(exclude)<sibling>' ':(exclude,glob)**/plans/**'
```

Look at each hit. A list of components or a page that names each
component of the same kind needs an entry for the new component, such
as the root `README.md` and the concept pages under
`liken/docs/content/docs/concepts/`. Skip the generated files, which
`make workflows` writes, and the code of a component that uses the
sibling.

The search does not find these:

- **`SECURITY.md`**, section "What is in scope", for a component that
  is not an operator and not part of the build.
- **A component with a coverage job and no manual**: add
  `/coverage/<name>.html` to the exceptions in
  `liken/docs/linkcheck/manual_test.go`. Add the component to "The
  coverage report" in
  `liken/docs/content/docs/reference/release-channel.md`. The `site`
  target of the root `Makefile` serves the report at that address.
- **The `plugins` repository**, outside this repository, for a
  component with a `skills/` directory. Its `AGENTS.md` describes the
  catalog entry, the `everything` plugin, and the table in its
  `README.md`.

These files have no list of components, so a new component needs no
edit in them:

- The root `.pre-commit-config.yaml` selects its files with patterns,
  such as each `package.toml` and each `Dockerfile`.
- `liken.sh/repositories.tf` lists the repositories of the
  organization, not the components of this one.
- `brand/data/nav.yaml` has no entry for a manual, because the sidebar
  shows the manual. `cd ci && go run . sites -root ..` reads the
  prefix of each manual from its `package.toml`, and `make docs` and
  `make site` build each one.
- The checks in `ci/` find each component through its
  `package.toml`. A new component needs an entry in `ci/` only in a
  list of exceptions, such as `unmanifested` and
  `unmanifestedDownloads` in `ci/manifests.go`, and only when the
  `pins` skill says that a download of the component needs one.
- `versions.sh` finds each `latest.sh` in the tree, so `make versions`
  reports the pins of the new component with no edit.

## Check the component before the first push

Run these from the worktree, in this order. Each one is a check that
CI runs on the push. A check that you skip here fails on CI instead.

1. **Stage the component**: `git add <component>`. Several checks read
   only what git tracks. `make test-docs` fails on a generated page or
   skill that git does not track. The Makefile test in `ci/` lists the
   Makefiles with `git ls-files`. `prek run --all-files` reads only
   the tracked files. When the brief says not to change the index,
   copy the index to a scratch file with
   `cp "$(git rev-parse --git-path index)" <scratch>/index`, export
   `GIT_INDEX_FILE=<scratch>/index`, and run `git add` and the steps
   below with it. The `git rev-parse` command also finds the index of a
   worktree, where `.git` is a file.
2. **The component's own checks**: `make test` in the component's
   directory. For a Go component, it runs `test-go` and `test-docs`.
3. **The generated files**: `make workflows` at the top. A second
   `make workflows` changes nothing.
4. **The checks of CI's `graph` job**: run each `run:` step of the
   `graph` job in `.github/workflows/ci.yaml`, from `ci/`. The first
   step is `go run . generate -root .. -check`. This command lists
   the steps:

   ```sh
   sed -n '/^  graph:/,/^  [a-z]/p' .github/workflows/ci.yaml | grep 'run:'
   ```
5. **The tests of `ci`**: `cd ci && go test ./...`. They read files
   from the whole repository, such as each `Makefile`, which must
   declare `.DELETE_ON_ERROR:`.
6. **The hooks**: at the top,
   `SKIP=<the job's skip list> prek run --all-files <component>/`. This
   is the command that the `prek` job of `component-<name>.yaml` runs.
7. **The image**, for a component with one: `make bases`, then
   `docker buildx bake <image> --load`, then the component's `smoke/`
   script with the image's reference. CI's images job runs the same
   build and script.
8. **The prose**: check every comment, page, and commit message against
   `brand/voice.md`. The repository is public, so check the tree for
   details of a home cluster, as the section "Privacy" of the root
   `AGENTS.md` describes. A comment or a page never contains a
   measurement from one run, such as how many seconds a device took to
   leave the slice in a lab run. Put the measurement in the plan that
   made it, with the conditions of the run.

Then commit the component and the generated files together, with the
`commit-messages` skill.

## After the push

A push to `main` runs every job of the new component. When the
repository variable `PUBLISH` is `true`, the push also publishes a
development build of the component's images and its `deploy/`
artifact. Watch the run with a timeout, as in
`timeout 3000 gh run watch <run> --exit-status`, and read the log of
each failed job before you change anything. The `releases` skill
describes how a development build is versioned and how a person pins
it. Proof on real machines happens by hand on the `lab` fleet or a
physical test cluster, as the "Where real proof happens" section of
the `testing` skill describes.
