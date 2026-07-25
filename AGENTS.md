# Working on liken

liken aims to be a real, public OS distribution that is also written to
be read. This goal shapes how you should write everything here.

## This is a literate project

This repository has very little ordinary program code. It is mostly
shell scripts, configuration, manifests, and build automation, and these
files *are* the documentation. Write them in a literate style. Add
generous comments that give instruction, explanation, and commentary. A
reader who reads the repository from top to bottom should learn how a
Linux system boots and how Kubernetes takes control after that.

Follow these rules for comments:

* **Teach the domain, not the syntax.** Do not explain what `mkdir -p`
  does. Explain why the kernel does not mount `/proc` on its own, why
  k3s needs cgroups, and why an initramfs is a cpio archive. Assume that
  the reader knows the tools already and reads this to learn how
  systems boot.
* **Explain why, then what.** The reason for a choice is more valuable
  than a description of the choice. If the project chose one option
  over an obvious alternative, state the choice and state the reason.
* **Comments are timeless.** A comment describes the system as it is
  now. It never describes how the system got that way. Do not write
  "changed from X" or "used to be Y" in a comment. That history belongs
  in commit messages. A reader can find that history there when it is
  relevant, and skip it when it is not.
* **Prose quality matters.** Comments here are writing for a public
  audience. Use plain language and complete sentences. Do not add
  filler words.

Some explanations are too big for a comment: for example, a design
decision that spans several files, or a survey of alternatives. Put
these explanations in a markdown document next to the thing they
describe, organized by domain.

## Commit messages

A comment says what the system is now. A commit message says what one
change does and why. Write it in ASD-STE100, like the rest of the prose
here. Keep it plain and keep it short. The message is a record for a
person who reads it during review or a bisect, not an essay.

Use this form:

```
Add a mount(8) so the kubelet can run mount helpers

The kubelet runs a program named mount to mount a volume. The image had
none, so the name fell through to busybox, which mounts with the raw
syscall and never runs a helper. An inline nfs volume failed.

/sbin/mount sorts an option list into the flag word and the data string,
then runs /sbin/mount.<fstype> when one exists. A mount with no helper
takes the same path it took before.

The lab mounted an NFS export with no version in its options and got
vers=4.2. Both smoke drills stayed green.

Closes #123
```

Follow these rules for the subject line:

* **Write it in the imperative.** It must complete the sentence "This
  commit will ...". Write "Add nodePortCIDRs to the cluster network
  spec". Do not write "A cluster names the networks its NodePorts
  answer on".
* **Name the change.** A reader of `git log --oneline` must learn what
  the commit does without opening it. A subject that only a person who
  read the diff can decode is wrong.
* **Keep it to 72 characters,** on one line, with no period at the end.

Follow these rules for the body:

* **Give the problem, then the change, then the evidence.** Say what was
  wrong or missing, say what this change does about it, and say what the
  lab measured when a drill ran. Three paragraphs is the target and five
  is the limit. Wrap at 72 columns.
* **Do not personify a program.** Software has no intentions and makes
  no discoveries. A program reads, writes, starts, refuses, and fails.
  It does not find, want, believe, learn, or concede.
* **Do not be clever.** Cut aphorisms, metaphors, and any sentence that
  is there because it sounds good. Cut a sentence that survives only as
  a flourish.
* **Do not narrate the session.** The message describes the change. It
  does not describe the order in which you found things, and it does not
  report how the work felt.
* **Do not restate the diff.** No file lists, no checklists, and no test
  plan. Name a measurement, not the tests you ran.
* **Name the issue** when there is one, with "Closes #1234".
* **Do not call the work "comprehensive"** and do not claim a "root
  cause".

A small change gets a subject line and nothing else. Do not write a body
to make a one-line change look larger.

The commits before 2026-07-25 do not follow these rules. They use
declarative subject lines that read as riddles, they give programs
intentions, and their bodies run long. That style is not the model. Do
not copy a message out of the log, and do not match the tone of the
commit you are building on. The history stays as it is, because a
rewrite would break every link and hash that names it.

## Organization

Organize the repository by domain, not by kind. Name each directory for
the part of the system it is, for example the kernel, the init, or the
image. Each directory must contain everything that domain needs:
scripts, configuration, and documentation together. Do not create one
shared `scripts/` directory for every domain.

## The manual

The docs domain is the website: the front page of liken.sh and the
user manual under /docs/. The manual is written in ASD-STE100, plain
technical English: short sentences, one instruction per sentence, no
metaphor. `docs/README.md` explains the domain.

When you change what an operator sees or does, evaluate whether the
manual must change with it, and make both changes together. The
cases to check:

* A `liken` CLI command or flag changes: update
  `docs/content/docs/reference/cli.md`, and check the guides that
  run the command.
* An operational flow changes (install, adoption, adding machines,
  upgrades, rollback): update the guide in
  `docs/content/docs/guides/`.
* The release channel's layout or artifacts change: update
  `docs/content/docs/reference/release-channel.md`.
* A CRD schema changes: the Machine and Cluster reference pages
  regenerate from the schemas at build time, so the schema's own
  descriptions are the fix. Write them knowing they become the
  manual.

A change that only touches internals needs no manual change. The
repository's comments carry that story.

## Licensing

liken's own code uses the MIT license, but a release also redistributes
other projects' binaries, and several of these use the GPL or LGPL
license. This never changes liken's own license, because the components
are aggregated, not linked. But it does require the release channel to
ship third-party notices with the binaries and to offer each
component's source from the same channel. The licensing domain owns
both tasks: every release bundles its `LICENSES.md` file as an
artifact, and the release workflow publishes its source mirror to
`sources/<component>/<version>/`.

When a vendored pin changes, update `licensing/` at the same time: the
source pins in `licensing/sources.sh` and the notices in
`licensing/NOTICES.md`. Those files explain the reasoning.
