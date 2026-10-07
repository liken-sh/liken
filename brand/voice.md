# Voice

These rules govern every word the `liken` project publishes, on its
sites and in its repositories: page copy, guides, reference text,
the field descriptions that generators turn into pages, and the
comments in the source files. Read them before you write. Scan your
text against them before you publish. This file follows its own
rules and is an example of them.

Write in Simplified Technical English (ASD-STE100). That standard is
the base, and the rules below add only what it does not state. STE
keeps each word and sentence plain. It does not make a page clear on
its own: a page of correct STE sentences can still be hard to follow.

## Plain explanation

Write the way you would explain the system to a colleague at a
whiteboard. Say what the thing is for, then how it works, in the
words you would use out loud.

- **Start from what the reader wants.** Define a thing by what it
  does for the reader, then explain how it works. "A `Receiver` is
  one A/V receiver on the far end of a `liken` machine's HDMI cable"
  gives its place in the wiring. "A `Receiver` is an A/V receiver
  that a `liken` machine plays through. When a `Player` starts
  playing, `equipment-operator` turns the receiver on and selects
  the machine's input" tells the reader why to declare one.
- **Name things directly.** Do not announce a list before you give
  it. "The storage operators give pods volumes of two kinds that
  Kubernetes does not provide on its own" makes the reader wait for
  the two kinds. "`git-csi-driver` mounts a git repository as a
  volume, and `per-node-csi-driver` gives each node its own
  directory" names them.
- **Do not coin phrases.** A phrase that the reader must decode,
  such as "at the grain that a workload asks for", "what the
  hardware serves", or "the far end of the cable", hides a plain
  fact. Write the fact: "one monitor output, one speaker, or one
  controller". A coined phrase that repeats from page to page is a
  sign that the fact under it was never written down.
- **Skip what the reader already knows.** The reader knows that an
  HDMI cable carries picture and sound, and that a `Deployment`
  runs pods. Spend the words on what `liken` does.
- **Do not build rhythm.** Paired and tripled clauses ("the cable
  carries the picture, and the network carries the control"),
  headings that count ("One image", "Two boot slots"), and closing
  lines with a twist ("it reboots for one reason: because you asked
  it to") make text sound crafted. Write the plain sentence, even
  when it is less symmetrical.
- **Connect the facts.** A paragraph explains one idea, and its
  sentences depend on each other. Use "because", "so", "when", and
  "until" to show how the facts connect. A paragraph of independent
  one-fact sentences leaves the reader to find the connection.
- **Talk to the reader.** Write "you" for the reader, and name the
  component that acts. Contractions are fine.
- **Do not write about the page.** "This page gives the model in one
  read" tells the reader nothing about the model. Start with the
  model.

## Pages

Arrange each page in the order that the reader needs it: what the
thing is for, how to use it, and then the detail.

- A concept page opens with what the thing is and why it matters, in
  a paragraph or two, before any table, list of steps, or field.
- A guide opens with what the reader will have at the end and what
  they need first. Then it gives the steps.
- A reference page opens with one sentence on what the object is for
  and a link to the guide that uses it.
- A manual's front page says what the component lets you do, shows
  a few things you can build with it, and links to the guides to
  start with. A short paragraph on how it works and one sentence that
  places it among the other components can follow. The detail goes in
  the manual's concepts.
- Say a thing once. When several pages need the same explanation,
  such as what an extension operator is, write it on one page and
  link to it from the others.

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

Use a natural, conversational voice. Humor is fine when it fits the
subject and keeps the facts clear. Do not add jokes or turn an
explanation into a slogan to give it personality.

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
