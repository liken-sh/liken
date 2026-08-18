# Voice

These rules govern every word the liken project publishes, on its
sites and in its repositories: page copy, guides, reference text,
the field descriptions that generators turn into pages, and the
comments in the source files. Read them before you write. Scan your
text against them before you publish. This file follows its own
rules and stands as an example of them.

Write in Simplified Technical English (ASD-STE100). That standard is
the base, and the rules below add only what it does not carry.

## Comments

The liken repositories are literate: the scripts, manifests, and Go
files are the documentation, and a comment is published writing in
the same sense a page is. Every rule in this file applies inside a
source file. Four more rules apply to comments:

- Teach the domain, not the syntax. The reader knows the tools and
  reads to learn how the system works.
- Explain why, then what. The reason for a choice is worth more
  than a description of the choice. If the project chose against an
  obvious alternative, state the choice and state the reason.
- Describe the system as it is now, never how it got that way. That
  history belongs in the commit message, where a reader can find it
  during a review or a bisect, and skip it any other time.
- Write complete sentences.

## Words

- Do not write "best practices", "leverage", "comprehensive",
  "robust", "seamless", or "root cause".
- Do not use an em-dash. Use a period or a comma.
- Set every technical identifier in the code face, always: a
  Kubernetes kind or API name (`PairingRequest`, `ResourceClaim`,
  `Secret`, `Deployment`), a command, a file name, a field name, a
  label or attribute key, a device class name, and a hostname that
  names software. Write "the `Secret` holds the deploy key", not
  "the secret holds the deploy key".
- Write `liken` in the code face everywhere it appears, because it
  names the code.

## Software is a machine

- Software has no mind. A program reads, writes, starts, refuses,
  and fails. Do not write that it wants, knows, thinks, learns, or
  believes.
- Software has no body. Do not write that code sits on, stands on,
  reaches into, or rides on anything.

## Claims

- State facts that a reader can check. Put the value next to the
  limit that gives it meaning, and the before next to the after.
- Say plainly which parts come from upstream projects and which
  parts this project adds. Do not claim more than the code does.
- Delete a sentence whose only job is to sound good: a slogan, an
  aphorism, or a closing flourish.
- End a section on its last fact, not on a summary of the section.

## Structure

- Give the answer first. Then give the data that supports it.
- Name the subject in a heading. Write "The release channel", not
  "What about releases?" or "Getting your bits".
