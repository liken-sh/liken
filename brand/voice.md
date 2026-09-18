# Voice

These rules govern every word the `liken` project publishes, on its
sites and in its repositories: page copy, guides, reference text,
the field descriptions that generators turn into pages, and the
comments in the source files. Read them before you write. Scan your
text against them before you publish. This file follows its own
rules and is an example of them.

Write in Simplified Technical English (ASD-STE100). That standard is
the base, and the rules below add only what it does not state.

## Clarity

Write for someone who understands software but did not take part in
the design discussion. Give them enough context to understand each
operation and the reason for it.

- Name the component, what it does, and what it acts on. State the
  trigger, timing, or restriction when that information matters.
- Use enough words to make the relationship explicit. Short words
  and short sentences can still conceal a complicated explanation.
  Add context before you shorten the text.
- Use established technical terms. A precise word such as
  "reconnects", "retains", or "deletes" often explains more than
  "stands", "holds", or "goes". Choose the verb for the operation;
  do not apply a list of replacements without reading the passage.
- Explain the behavior behind a metaphor. "The promise has to live
  in a file" leaves the reader to infer what is recorded and why.
  "The operator records which panels to power down when their claims
  are released. The file preserves this record across operator
  container restarts" states both facts.
- Keep useful examples and design reasoning. A longer paragraph is
  appropriate when it saves the reader from guessing.

Review the paragraph as a whole. Replacing a figurative verb does not
help if the reader still has to infer how its sentences connect.
Introduce the component or state before describing what changes it.
Explain a technical term when the intended reader needs a definition;
keep the term when it names the concept precisely.

Use a natural, conversational voice. Contractions and humor are fine
when they fit the subject and keep the facts clear. Do not add jokes
or turn an explanation into a slogan to give it personality.

## Comments

The `liken` repositories are literate: the scripts, manifests, and Go
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
- Distinguish a measured result from a guarantee. Name the conditions
  under which a result was measured. Do not turn one successful test
  into a claim that an operation always succeeds.
- Delete a sentence whose only job is to sound good: a slogan, an
  aphorism, or a closing flourish.
- End a section on its last fact, not on a summary of the section.

## Structure

- Give the answer first. Then give the data that supports it.
- Name the subject in a heading. Write "The release channel", not
  "What about releases?" or "Getting your bits".

## Rewriting

Every fact must survive a style edit. Read the surrounding text and
check the implementation when the meaning is unclear.

- Before editing, identify the conditions, negations, quantities,
  defaults, units, ordering, and failure behavior. Check each of them
  against the result. Words such as "only", "unless", "until", and
  "may" often express a requirement or a limit.
- Preserve the difference between an absent value and zero, declared
  and observed state, and proposed and implemented behavior.
- If a claim appears wrong or unsupported, report it separately.
  Do not quietly strengthen, weaken, or correct it during a style
  edit. Leave an ambiguous passage unchanged until its meaning is
  established.
- Plans record the design and evidence at the time they were
  written. Keep their dates, status, measurements, constraints, and
  reasons for rejecting alternatives. Preserve future tense in a
  proposal and do not rewrite a completed plan to describe today's
  implementation. The rule about timeless comments does not remove
  historical context from plans.
- Give unclear headings and filenames concrete names. When renaming
  them, update the links that refer to them and preserve published
  URLs or provide redirects. A clearer guide title does not require
  a new URL or skill name.
- Edit the authored source of generated prose, then regenerate the
  output. Guide text, schema descriptions, and API documentation
  strings must remain consistent with the pages and skills they
  produce.

Read the revised passage without the original beside it. Check that
the reader can identify the operation and its conditions without
decoding a phrase or supplying missing context.
