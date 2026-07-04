# Working on liken

liken is a learning project as much as it is a distro, and that shapes how
everything here should be written.

## This is a literate project

There will be very little "code-code" here. The repo is mostly shell
scripts, configuration, manifests, and build automation — and those files
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
  how it got that way. No "changed from X", no "used to be Y" — that
  history belongs in commit messages, where it can be found when it's
  relevant and ignored when it isn't.
* **Prose quality matters.** Comments here are writing for a public
  audience. Plain language, complete sentences, no filler.

When a file's story gets too big for comments — a design decision that
spans files, a survey of alternatives — it goes in a markdown document
next to the thing it describes, organized by domain.

## Organization

Organize by domain, not by kind. Directories should be named for what
part of the system they are (the kernel, the init, the image), and each
should contain everything that domain needs — scripts, config, and docs
together. No `scripts/` junk drawer.
