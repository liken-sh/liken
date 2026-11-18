# Messages

A message is the text that a program prints when a person cannot ask
it a question: an error, a log line, the message of a condition or an
`Event`, the failure of a test, and the output of a script. The
person who reads it is in the middle of a problem and often has only
that one line, so the line must hold the facts. Every rule in
[`voice.md`](voice.md) applies to a message. The rules here add what
a message needs.

## What every message holds

- **Say what the program found, and on which object.** Name the
  object by its kind and its name, as the reader types them.
  "no PersistentVolume of git.csi.liken.sh carries the handle
  pvc-7f3a" tells the reader where to look. "volume lookup failed"
  does not.
- **Put the value next to the limit.** "the frame holds 4096 bytes
  and the crop reads 8192" shows both numbers. "crop out of range"
  makes the reader find them.
- **State the rule when a value breaks it.** "a version must match
  `^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`" tells the reader what to
  type. "invalid version" tells the reader only that the program
  refused.
- **Say what the program did next** when the reader cannot tell: it
  stopped, it kept the last good value, or it tries again in 30
  seconds.
- **Give the next step when the program can name one.** Write the
  command or the field: "set `spec.modules` to load `i915` before
  `snd_hda_intel`". Give the step only in a message that a person
  reads directly. Do not guess a step. A wrong step costs the reader
  more than no step.
- **Quote a value that can be empty or can hold a space**, with `%q`
  in Go and `{:?}` in Rust, so the reader can see its limits.
- **Write no word that only reports that something went wrong.**
  "error", "failed", "problem", "unexpected", and "something went
  wrong" add nothing to a line that is already an error. "an error
  occurred while reading the catalog" becomes "reading the catalog:
  the file ends in the middle of a row".
- **Write to the reader plainly.** Do not write "please", "sorry",
  "oops", or an exclamation mark, and do not say the reader made a
  mistake. State what the program needs.
- **Use no markup.** A terminal and `kubectl` print a backtick or an
  asterisk as it is. An identifier keeps its exact spelling and
  case, and that is enough to mark it.

## Error values in Go and Rust

An error value moves up through the callers, and each caller can add
to it. The reader sees the whole chain on one line, so each part is a
fragment that reads well after the part before it.

- **Start with a lowercase letter and end with no period.** The
  exception is a word that is always capitalized, such as a kind:
  "reading the ResourceClaimTemplate gpu: ...".
- **The first error of a chain is a clause that states the fact.**
  "the cluster URL has no host", "the answer names no hostname".
- **A caller adds what it was doing and to which object, as an
  "-ing" phrase.** "reading the placements of films: %w", "watching
  /run/udev: %w". The chain then reads as a sentence: "reading the
  placements of films: opening the catalog: permission denied".
- **Do not write "failed to", "unable to", "could not", or
  "error".** Each caller that adds one makes the chain "failed to
  read: failed to open: failed to ...", and the reader looks for the
  fact among them.
- **Add only what the caller alone can tell.** `os.Open` already
  names the path, so "opening /etc/liken/machine.yaml: open
  /etc/liken/machine.yaml: permission denied" says it twice. When a
  caller has nothing to add, it returns the error as it is.
- **Put `%w` last**, so the newest context comes first and the cause
  comes last.
- **Return an error or log it, not both.** An error that a function
  logs and also returns is printed once by each caller that does the
  same.
- **Compare errors by value or type**, with `errors.Is`, `errors.As`,
  or a Rust `enum`. Text that a test or a caller matches cannot be
  made clearer later.

## Errors that a person reads

A command, a script, and an admission webhook print the top of the
chain to a person. That line is where the next step goes.

- A command prints the error once, on `stderr`, and exits with a
  status that is not zero.
- A script prefixes its name: `snapshot.sh: <sums> has no checksum
  for <list>`. In a pipeline of several scripts, the prefix tells
  the reader which one stopped.
- When the reader can fix the cause, the last sentence is the fix,
  because the end of the output is what a terminal leaves in view.
- When the cause is a defect in `liken`, say so, and name what to
  send with a report.

## Log lines

A log line is a record that a person searches and filters, so the
text that names the event is the same on every occurrence and the
values are in fields.

- **The message is a fixed clause in lowercase with no period.** It
  states what happened: "the metrics listener stopped". It holds no
  formatted value.
- **Each value is a key and a value.** `slog.Warn("the scan pod
  exited", "pod", name, "exitCode", code)`, not
  `slog.Warn(fmt.Sprintf("scan pod %s exited with %d", name, code))`.
- **A key is a whole word in lowerCamelCase, and one thing has one
  key in every component.** The error is `error`. An object is
  under its kind: `pod`, `machine`, `claim`.
- **The level states who must act.** `Error` is for a state that
  the component cannot leave without a person. `Warn` is for a
  fault that the component handles, such as a write that it sends
  again. `Info` is for an action the component took. `Debug` is for
  each attempt and each reading.
- A log line holds the detail that an `Event` leaves out. The root
  `AGENTS.md` says which facts go to a condition, an `Event`, and a
  log line.

## Conditions and Events

The `operators` skill under `.agents/skills` holds the rules for a
reason, for which transitions post an `Event`, and for the code that
writes them. These rules are for the message.

- **A condition's message says what is true now**, in the present
  tense, because `kubectl wait` and a person read it at any later
  time. "the receiver reports input HDMI 2, and the `Player`
  declares HDMI 1".
- **An `Event`'s message says what happened**, in the past tense.
  "the display restarted 2 times: Error (exit code 1)".
- **A message of a condition that needs a person names the field to
  change** and the value that the component read from it: "the
  ResourceClaimTemplate appearances-gpu that
  spec.appearances.gpuResourceClaimTemplate names is not in the
  namespace films".
- A message is a clause in lowercase with no period, like an error.
  It holds the values and repeats nothing that the reason and the
  condition type already state.

## Test failures

A person reads a test failure in CI output, without the test's
source open.

- **Say what the code returned and what the test wanted**, in that
  order, with the word "want": "the record is {Running}, want
  Scanned". For a function, `Parse(%q) = %v, want %v`.
- **Say when**, if the test checks a state more than once: "after
  the retry, 2 scan pods remain, want none".
- **Say why the wanted value is correct** when it is not plain: "want
  Failed because the log could not be read".
- A step of the setup that fails reports like an error value:
  `t.Fatalf("writing the PersistentVolume: %v", err)`.
- [`names.md`](names.md) has the rules for the name of a test.
