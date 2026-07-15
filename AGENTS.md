# Working on liken

liken aims to be a real, public OS distribution, and one that is written to
be read. That ambition shapes how everything here should be written.

## This is a literate project

There will be very little "code-code" here. The repo is mostly shell
scripts, configuration, manifests, and build automation, and those files
*are* the documentation. Write them in a literate style: generous comments
that carry instruction, explanation, and commentary, so that reading the
repo top to bottom teaches you how a Linux system boots and how Kubernetes
takes over from there.

A few rules for those comments:

* **Teach the domain, not the syntax.** Don't explain what `mkdir -p`
  does; explain why the kernel doesn't mount `/proc` for you, why k3s
  needs cgroups, why an initramfs is a cpio archive. Assume the reader
  knows their tools but is here to learn how systems boot.
* **Explain why, then what.** The reasoning behind a choice is more
  valuable than a restatement of it. If we chose something over an obvious
  alternative, say so and say why.
* **Comments are timeless.** They describe the system as it is now, never
  how it got that way. No "changed from X", no "used to be Y". That
  history belongs in commit messages, where a reader can find it when it's
  relevant and skip it when it isn't.
* **Prose quality matters.** Comments here are writing for a public
  audience. Plain language, complete sentences, no filler.

Some explanations are too big for comments: a design decision that spans
several files, or a survey of alternatives. Those go in a markdown document
next to the thing they describe, organized by domain.

## Organization

Organize by domain, not by kind. Directories should be named for what
part of the system they are (the kernel, the init, the image), and each
should contain everything that domain needs: scripts, config, and docs
together. No `scripts/` junk drawer.

## Licensing

liken's own code is MIT, but releases redistribute other projects'
binaries, several under GPL or LGPL. That never touches liken's
license (they are aggregated, never linked), but it does oblige the
release channel to ship third-party notices with the binaries and to
offer each component's source from the same channel. The licensing
domain owns both: every release bundles its `LICENSES.md` as an
artifact, and the release workflow publishes its source mirror to
`sources/<component>/<version>/`.

When a vendored pin changes, licensing/ must move with it: the source
pins in `licensing/sources.sh` and the notices in
`licensing/NOTICES.md`. Those files explain the reasoning.
