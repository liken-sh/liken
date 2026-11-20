---
name: commit-messages
description: Writes a commit message or the description of a pull request for the liken repository - the first line, the reason in the body, the evidence, what the change leaves open, and what to leave out. Use before every git commit and before opening or editing a pull request.
---

# Commit messages

A person reads a commit message in a review, in `git log`, and
during a bisect, with the diff beside it. So the message says what
the diff cannot show: why the change was made, and how the author
learned that it was needed. The rules of `brand/voice.md` apply to
it. The privacy rule of the root `AGENTS.md` applies too, because the
history is public.

## The first line

The first line says what changed in the behavior of the system, as a
command, with no period:

- "Read a pushed image's labels again when the registry answers 404"
- "Keep a Node condition's generation when a pass did not read the
  Node"
- "Test the real flux seed in CI's go job"

Keep it to 72 characters. Name the component or the object. If the
change applies only under one condition, say the condition. Use the
words of the manual, because a reader of `git log --oneline` does not
know the private names in the code. A commit that changes only tests
starts with "Test". A commit that changes only a plan or a document
says what the document now says: "Close plan 96, which the publish
retry completes". "Fix bug", "Update publish.go", and "Improve error
handling" describe no behavior, so a reader of `git log --oneline`
learns nothing from them.

A commit that moves a pin to a newer release starts with "Move", the
pin, and the version: "Move <pin> to <version>". For a snapshot,
give its date: "Move the bases to the Debian snapshot of <date>".
When the bump fixes a known fault, add the reason, as in "Move the
kernel to <version> to fix <CVE id>". Name several pins in one line
when one commit moves them together.

## The body

After one blank line, the body gives the reason in one or two short
paragraphs. A change that needs more than three paragraphs is usually
two changes, and each one gets its own commit. A change whose first
line says everything needs no body. Write each paragraph as one line
with no line breaks inside it, because GitHub shows a hard-wrapped
paragraph as ragged lines.

- **Say what was wrong or missing, and how you learned it.** A date,
  the text of an error, the name of a test that failed, and a
  measurement with its conditions are evidence. "A publish of
  `library-operator` failed on 2026-11-17 after it pushed every
  layer: ghcr.io answered 404 for the digest two seconds after it
  accepted the push." When you did not see the evidence yourself,
  write what was wrong and what the system does after the change. Do
  not write a date, an error, or a measurement that you did not see.
- **Say what the system does now**, in the terms that a user or an
  operator sees: the new limits, and the cases that did not change.
- **Say what the change leaves open**: a cause that is not known, a
  case that it does not cover, a check that was not run. "The
  failure did not repeat in 310 runs on a workstation, so its cause
  is not known."
- **Link what the reader can follow**: "Closes #123", a plan by its
  number, an upstream issue.

The history of the change goes here, not in a comment: the approach
that was replaced, what was tried first, and why it was not enough.

## What to leave out

- A list of the files, or a retelling of the diff. The reader has
  the diff.
- A checklist of tests. If a result is evidence, such as a drill on
  the `lab` fleet, give it in a sentence.
- A statement of quality, such as "improves reliability". Say which
  failure no longer occurs.
- An account of the session: what you were asked, and the steps you
  took.
- A word for time that depends on the day of the commit, such as
  "today" or "this morning". Write the date.

## Make the commit

- Stage every changed file with `git add .`, so that the hooks check
  the same tree that the commit contains.
- Pass the message from a file or a heredoc, as in
  `git commit -F - <<'EOF'`, so that a backtick in the message
  reaches git unchanged.
- The trailers that the harness asks for, such as `Co-Authored-By`,
  go after the body, with one blank line before them.
- A pre-commit hook can change a file, for example to add a missing
  newline at the end, and the commit then fails. Check the commit's
  exit status, and do not pipe the command into a filter that hides
  it. Run `git add .` and the same commit again. Do not amend, and do
  not pass `--no-verify`.
- Check the first line's length after the commit:
  `git log -1 --format=%s | awk 'length > 72'` prints nothing when
  the line fits.

"Branches and landing" in the root `AGENTS.md` says how a commit
gets to `main`.

## Pull requests

Work in this repository goes to `main` directly, with no pull request.
A repository outside it can take one, such as an upstream project that
you contribute to. That pull request is usually one commit, and its
description is that commit's message, with each paragraph on one
line. For a pull request of several commits, write the description
the same way for the whole change, and let each commit give its own
reason.
