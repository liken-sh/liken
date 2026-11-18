---
name: commit-messages
description: Writes a commit message or the description of a pull request for the liken repository - the first line, the reason in the body, the evidence, what the change leaves open, and what to leave out. Use before every git commit and before opening or editing a pull request.
---

# Commit messages

A person reads a commit message in a review, in `git log`, and
during a bisect, with the diff beside it. So the message holds what
the diff cannot show: why the change was made, and how the author
learned that it was needed. The rules of `brand/voice.md` apply to
it, and so does the privacy rule of the root `AGENTS.md`, because
the history is public.

## The first line

The first line states what changed in the behavior of the system, as
a command, with no period:

- "Read a pushed image's labels again when the registry answers 404"
- "Keep a Node condition's generation when a pass did not read the
  Node"
- "Test the real flux seed in CI's go job"

Name the component or the object, and state the condition when the
change applies only under one. "Fix bug", "Update publish.go", and
"Improve error handling" name no behavior, and a reader of
`git log --oneline` learns nothing from them.

## The body

After one blank line, the body gives the reason in one or two short
paragraphs. Write each paragraph as one line with no line breaks
inside it, because GitHub shows a hard-wrapped paragraph as ragged
lines.

- **Say what was wrong or missing, and how you learned it.** A date,
  the text of an error, the name of a test that failed, and a
  measurement with its conditions are evidence. "A publish of
  `library-operator` failed on 2026-11-17 after it pushed every
  layer: ghcr.io answered 404 for the digest two seconds after it
  accepted the push."
- **Say what the system does now**, in the terms a user or an
  operator sees: the new limits, the cases that did not change.
- **Say what the change leaves open**: a cause that is not known, a
  case that it does not cover, a check that was not run. "The
  failure did not repeat in 310 runs on a workstation, so its cause
  is not known."
- **Link what the reader can follow**: "Closes #123", a plan by its
  number, an upstream issue.

The history of the change belongs here and not in a comment: the
approach that was replaced, what was tried first, and why it was not
enough.

## What to leave out

- A list of the files, or a retelling of the diff. The reader has
  the diff.
- A checklist of tests. State a result that is evidence, such as a
  drill on the `lab` fleet, in a sentence.
- A statement of quality, such as "improves reliability". State the
  failure that no longer occurs.
- An account of the session: what you were asked, and the steps you
  took.

## Pull requests

A pull request is usually one commit, and its description is that
commit's message. For a pull request of several commits, write the
description the same way for the whole change, and let each commit
give its own reason.
