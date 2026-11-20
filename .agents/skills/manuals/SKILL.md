---
name: manuals
description: Writes or changes a page of a component's manual in the liken repository, the Hugo site under a component's docs/ that liken.sh serves - where a guide, a concept page, and a reference page go, the front matter that turns a guide into a skill for the component's users, the reference pages that crdref generates from the descriptions in a CRD, which hand-written pages each kind of code change makes wrong, and the make targets that generate, check, and preview a manual. Use before writing or editing a file under a docs/content directory, before the commit of a change to a manifest, a container, a flag, a metric, a printer column, a condition, an Event, or a behavior that a person sees, and when make test-docs fails.
---

# Manuals

A reader installs and runs a component from its manual alone. So when
the manual is behind the code, it gives that reader a wrong command. A
commit that changes what a person installs, runs, reads, or sees
changes the manual in the same commit. An audit in 2026-10 found the
same kinds of difference between manual and code in most components.
Each one came from a change that left the manual for later.

`brand/voice.md` applies to every sentence of a manual. Its section
"Pages" says how a concept page, a guide, a reference page, and a
front page each start. Read it before you write a page, and check the
page against it before you commit.

## Where a page goes

A component's manual is a Hugo site in its `docs/`. It is served at
`https://liken.sh/<prefix>/`, where the prefix is the value of
`[docs] prefix` in the component's `package.toml`. The manual of the
OS is in `liken/docs/`, and it is served at the root.

- `content/_index.md` is the front page of the manual.
- `content/docs/guides/` contains the guides. A person writes each
  one.
- `content/docs/concepts/` contains the concept pages.
- `content/docs/reference/` contains the reference pages. `crdref`
  generates the page of each kind from the CRD. A person writes the
  other reference pages, such as the metrics and the devices.

The look comes from the theme in `brand/`, which every site shares. So
a manual has no template and no stylesheet of its own. Hugo publishes
each page as HTML, and also as the Markdown that you wrote. A title
that is only in the front matter does not appear in the Markdown, so
each page writes its own top-level heading in its content.

Explain each thing on one page only. When a page of one component
needs an explanation that is already in another component's manual,
link to it with the full `https://liken.sh/` address.

## A guide is also a skill

`make -C docs skills` writes each guide as a skill into the
component's `skills/`, for the agents of the component's users. The
skill gets its `description` from the guide's front matter:

```yaml
---
title: Give a person a picture
weight: 20
description: "Give a Person a picture with spec.avatar ... Use when a person's face should appear on the screens, when a replaced picture does not show, or when a picture fails to load."
---
```

Write the description in the form that a host uses to select a skill:
what the guide does, and then "Use when" with the words that a
person's request would contain. Do not edit a file under `skills/`.
Edit the guide, and run the target.

## A generated reference page

`crdref` writes the reference page of a kind from the `description`
of each field in the CRD. To change the text of that page, edit the
description in the CRD, or in the template or the Go type that
generates the CRD. Then run `make -C docs generate`. Write the
description as text for the manual, with each identifier in
backticks. Do not edit the generated page.

Each component commits its generated pages, except `liken/docs/`.
There the build writes the pages, and git ignores them.

## Pages that a change makes wrong

When you run the generators, the generated pages and the skills become
current. The pages that a person wrote do not. Check these pages for
each kind of change:

| Change | Pages to update |
|---|---|
| A file added to or removed from `deploy/kustomization.yaml` | Every file list in the install guide: the install from raw URLs, the GitOps example, and the removal steps |
| A container, a `Deployment`, or a `DaemonSet` added or removed | The install guide's description of what runs, and the pin of a development build, which lists every image |
| A metric added, renamed, or removed | `reference/metrics.md` |
| A flag of the operator or of the `kubectl liken` plugin | The guide that runs the command |
| A printer column, a condition, or the reason of an `Event` | Every sample of `kubectl get` or `kubectl describe` output that shows it |
| A behavior that the operator adds on its own and that a person can see, such as a recovery or a retry | The guide that a reader opens when the behavior surprises them |
| A field or a topic removed from the message bus or the API | Every page of another component that mentions it |
| A new failure, a new retry or recovery, or a new reason of a condition or an `Event` | `guides/troubleshoot.md`, in a component that has one |
| A topic, a payload, a writer, a reader, or the delivery of a message on the bus | `reference/bus.md` of the component that publishes the topic, in `library-operator` and `media-operator` |

For the OS, in `liken/docs/`:

| Change | Pages to update |
|---|---|
| A command or a flag of the `liken` CLI | `content/docs/reference/cli.md`, and each guide that runs the command |
| A flow that a person follows: install, adoption, adding machines, upgrades, rollback | The guide of that flow in `content/docs/guides/` |
| The layout or the artifacts of the release channel | `content/docs/reference/release-channel.md` |
| The schema of a CRD | The descriptions in the schema, from which the reference page is generated |

A change that a person cannot see needs no change to the manual. The
comments in the source describe it.

## Generate, check, and preview

Run these from the component's directory:

- `make -C docs generate` writes the reference pages again.
- `make -C docs skills` writes the skills again.
- `make test-docs` writes both again, checks every internal link,
  and builds the site. It fails when a generated file differs from
  the git index, so stage the generated files together with the guide
  or the CRD that changed them. CI runs the same target.
- `make -C docs build` builds the site into `docs/dist/site`.

`make preview` at the top of the repository builds every manual into
one tree, and serves it at <http://localhost:8080/> the way liken.sh
serves it. Use it to check a link from one manual to another.
