# brand

`brand` makes every `liken` site, screen, and text look and read as
one project. It contains the mark, the stylesheet, and the Hugo theme
that every manual builds with. It also contains the rules for every
word the project publishes, and the tools that write a manual's
reference pages, its skills, and its coverage report.

`brand` decides how a page looks and sets the rules for the prose. It
does not decide what a page says. Each component writes its own
manual, and each site owns its own layouts and parameters. `brand`
publishes no image and no release of its own.

Every other component reads this directory from the tree at the same
commit, so a change here reaches every site and screen at once. Each
kind of consumer uses it in its own way:

- A Hugo site sets `themesDir: ../..` and `theme: brand`.
- A Go program imports the module `github.com/liken-sh/liken/brand`,
  with a `replace` directive that points to this directory.
- A manual's docs module declares `crdref`, `apiref`, `skills`, and
  `coverage` as Go tool dependencies, and its tests import
  `linkcheck`.
- A Rust program that draws with `iced` lists `iced/` as a path
  dependency, and its image build uses this directory as the build
  context `brand`.

## The parts

- [`mark.md`](mark.md) describes the mark, `liken.svg`: the lichen
  that it shows, its colors, and the files that `make` writes from
  it, such as `favicon.ico` and `liken.png`.
- [`motion.md`](motion.md) has the rules for the mark in motion and
  for the focus outline.
- `liken.css` is the stylesheet that every site and the release
  channel inline into each page.
- [`theme.md`](theme.md) describes the Hugo theme: `layouts/`,
  `static/`, `assets/`, the top nav in `data/nav.yaml`, and the
  settings that a site's `hugo.yaml` needs.
- [`voice.md`](voice.md) is the voice for every word the project
  publishes. The root `AGENTS.md` imports it.
  [`messages.md`](messages.md) adds the rules for errors, log lines,
  and the messages of conditions, `Event`s, and test failures.
  [`names.md`](names.md) adds the rules for names.
  [`instructions.md`](instructions.md) adds the rules for an
  `AGENTS.md` file, a skill, and a brief for an agent.
- [`editing-prompt.md`](editing-prompt.md) is the brief that a
  session gives to an agent when it asks the agent to correct the
  prose, messages, or names in one part of the repository.
- The Go package `brand` embeds `liken.css` and `liken.svg` for a
  program that builds pages without Hugo, such as the index page of
  the release channel in `liken/releases`.
- `crdref/` writes a manual's reference page for a CRD from its
  schema.
- `apiref/` writes a manual's route reference page from an OpenAPI
  document.
- `skills/` writes each guide of a manual as an Agent Skill, into the
  component's `skills/` directory.
- `coverage/` writes one HTML page from the Go and Rust coverage data
  of a component's tests.
- `linkcheck/` checks that each internal link of a manual leads to a
  page that exists, and to a heading that exists when the link names
  one. The manual's own tests run it.
- `preview/` serves every manual together on a workstation. Run it
  with `make preview` at the top of the repository.
- `iced/` is the Rust crate `liken-iced`, which the screens of
  `media-operator` and `library-operator` use. It draws the mark,
  reads the palette from `liken.css`, runs the pulse animation that
  `motion.md` describes, and loads the fonts.
- `fonts/` contains Source Sans 3: the static upright and italic
  files that `liken-iced` embeds, and variable files for a web page.
  The stylesheet of the sites does not use these fonts. It uses the
  system's sans-serif face.

The doc comment at the top of each tool's `main.go`, and of each
module of `iced/src/`, describes its input, its output, and its
flags. The designs of the skills generator and of the route
reference are in `plans/completed/`.

MIT. See [LICENSE](LICENSE). The fonts are under the SIL Open Font
License, in [`fonts/LICENSE.md`](fonts/LICENSE.md).
