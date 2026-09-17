# 01, The guides as skills

Every guide on a liken site becomes an agent skill, emitted by one
generator this repository publishes. An agent that installs a
repository's `skills/` directory gets the same steps a person reads
on the site, with a trigger line that says when to use them. There
is no MCP server.

## The problem

An agent that operates a liken cluster needs the procedures: pair a
controller, map its keys, declare a `Player`, install an operator,
roll a fleet back. Today those live only on the sites, written for
a person with a browser. An agent with a shell and `kubectl` has
everything else it needs, and nothing tells it the steps.

The first idea was an MCP server. It has no consumer. Every agent
we can name has a shell: Claude Code on a workstation, and the phone
through Remote Control, which drives that same session. The Claude
mobile app cannot reach a server on the LAN at all: a custom
connector connects from Anthropic's cloud and needs a public
address, on every client
([help center](https://support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp),
read 2026-09-16). So the agent-facing surface is skills, and the
skills are the guides.

## The design

```
docs/content/docs/guides/<slug>.md     the guide, the one source
        |  go tool skills               this repository's generator
        v
skills/<slug>/SKILL.md                 committed; CI fails when stale
```

### The generator

`skills/` in this repository is a Go program, run as `go tool skills`
and pinned in each docs module the way `crdref` is:

    go tool skills -base https://media.liken.sh content/docs/guides ../skills

For every `*.md` in the guides directory except `_index.md`, it
writes `<out>/<slug>/SKILL.md`. The slug is the file's base name,
and it must satisfy the Agent Skills name rule (lowercase, digits,
hyphens; at most 64 characters), because the spec says the `name`
must match the directory
([specification](https://agentskills.io/specification)).

The front matter maps as follows. `title` becomes the H1 the body
already has, so it is dropped. `description` is required and copied
verbatim; a guide without one fails the build, because the trigger
line is the one thing a skill has that a guide does not. `weight`
is dropped.

The body is the guide's body, with two changes:

* Every absolute site link, `](/docs/...)`, becomes a full URL under
  `-base`. Every relative link, `](../claim/)`, resolves against the
  guide's own URL, `<base>/docs/guides/<slug>/`, first. An agent
  reads the skill from disk, so a site-relative path resolves to
  nothing.
* One fixed paragraph opens the body, before the H1: where the guide
  lives on the site, and an instruction to confirm the `kubectl`
  context before the first command. The testbed-context mistake is
  the first thing an agent gets wrong.

The generator owns the output directory. After it writes, it removes
every subdirectory it did not write, so a renamed or deleted guide
leaves no stale skill. `_index.md` and any file that is not `.md`
are skipped, not errors.

The link rewrite reuses `linkcheck`'s link grammar, so the two
programs agree on what a link is.

### Each site

Each repository with a docs site gets:

* A `description` line in every guide's front matter, written as a
  trigger: what the guide does, and when an agent should reach for
  it. Hugo can show the same line on the guides index later.
* A `skills` target in `docs/Makefile` that runs the generator into
  `../skills`, and a line in the root `test-docs` target that runs it
  and fails when `git` reports the directory changed or untracked.
* The output, committed. `npx skills add liken-sh/<repo>` and Claude
  Code's `--plugin-dir` both read `skills/` at a repository root, so
  the directory must be in the checkout.

The repositories in scope are the ten with a guides section: liken,
media-operator, display-operator, audio-operator, bluetooth-operator,
library-operator, people-operator, equipment-operator,
git-csi-driver, and per-node-csi-driver.

### Developer skills stay where they are

The skills a developer of a repository uses (release,
bump-components, dev-cluster-drills in liken; devlog-copyedit in
log) are not guides and are not emitted. They move to
`.agents/skills/`, the path Codex and Cursor read natively, with
`.claude/skills` a symlink to it, the same convention as
`AGENTS.md` and `CLAUDE.md`. Claude Code documents only
`.claude/skills/`
([skills](https://code.claude.com/docs/en/skills)), and Cursor
reads both
([Cursor docs](https://cursor.com/docs/context/skills)).

### No CLI verb

The shape conversation named `liken debug <node>` as a candidate
verb for the busybox-with-hostPath recipe. `kubectl debug
node/<node> --profile=sysadmin` already gives a host-PID shell with
the node's filesystem at `/host`, so the verb is one line in the
troubleshoot guide, not code.

## Open problems

* **A marketplace and a bundle.** Claude Code can install every
  operator's skills with two commands if a marketplace lists each
  repository and a bundle plugin depends on all of them
  ([plugin dependencies](https://code.claude.com/docs/en/plugin-dependencies)).
  The marketplace pins each repository to a ref, and a pinned ref
  means a catalog commit per operator release. An unpinned entry
  floats to main and loses the known-good set. Neither is chosen.
  The catalog would be a new `liken-sh/plugins` repository; nothing
  else in the org is about the org.
* **Skills for other harnesses.** `npx skills add` installs from a
  GitHub repository into every agent it knows, and the plan assumes
  it scans `skills/` at the root. That is unverified until someone
  runs it against one of these repositories.
* **Guides that are not procedures.** Some guides describe a thing
  (the catalog, scanning, franchises) and give no steps. They emit
  as skills too, and their `description` says what they explain.
  Whether an agent benefits from them as skills, or only from the
  procedures, is a question for use.
