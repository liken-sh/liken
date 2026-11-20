# Messages

A message is the text that a program prints when a person cannot ask
it a question: an error, a log line, the message of a condition or an
`Event`, the failure of a test, and the output of a script. The
person who reads it is in the middle of a problem and often has only
that one line, so the line must contain the facts. Every rule in
[`voice.md`](voice.md) applies to a message. The rules here add what
a message needs.

## What every message contains

- **Say what the program found, and on which object.** Name the
  object by its kind and its name, as the reader types them.
  "no PersistentVolume of git.csi.liken.sh carries the handle
  pvc-7f3a" tells the reader where to look. "volume lookup failed"
  does not.
- **Put the value next to the limit.** "the frame holds 4096 bytes
  and the crop reads 8192" shows both numbers. "crop out of range"
  makes the reader find them.
- **Say what the rule is when a value breaks it.** "a version must
  match `^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`" tells the reader what
  to type. "invalid version" tells the reader only that the program
  rejected the value.
- **Say what the program did next** when the reader cannot tell: it
  stopped, it kept the last good value, or it tries again in 30
  seconds.
- **Give the next step when the program can name one.** Write the
  command or the field: "set `spec.modules` to load `i915` before
  `snd_hda_intel`". Give the step only in a message that a person
  reads directly. Do not guess a step. A wrong step costs the reader
  more than no step.
- **Quote a value that can be empty or can contain a space**, with
  `%q` in Go and `{:?}` in Rust, so the reader can see where the
  value starts and ends.
- **Write no word that only reports that something went wrong.**
  "error", "failed", "problem", "unexpected", and "something went
  wrong" add nothing to a line that is already an error. "an error
  occurred while reading the catalog" becomes "reading the catalog:
  the file ends in the middle of a row".
- **Write to the reader plainly.** Do not write "please", "sorry",
  "oops", or an exclamation mark, and do not say the reader made a
  mistake. Say what the program needs.
- **Use no markup.** A terminal and `kubectl` print a backtick or an
  asterisk as it is. Write an identifier with its exact spelling and
  case. That is enough to mark it.

## Capital letters and periods

A message follows the practice of the system that prints it, so that
it looks like the lines around it.

- **A fragment starts in lowercase and ends with no period.** An
  error value is a fragment, because a caller adds to it. So is a
  line that follows the name of a program, such as
  `liken: reading the channel document: ...`. Go, Rust, the Unix
  tools, and the kernel all print these in lowercase.
- **A message that is a field of its own starts with a capital
  letter and ends with no period.** The message of a condition, the
  message of an `Event`, and the message of a structured log line
  are each printed alone in a column or a field. Kubernetes writes
  its `Event`s and its log messages this way: "Created pod",
  "Successfully assigned".
- A word that is always lowercase, such as a field path or a
  command, stays lowercase at the start of a message. A kind keeps
  its capital anywhere.

## Error values in Go and Rust

An error value moves up through the callers, and each caller can add
to it. The reader sees the whole chain on one line, so each part is a
fragment that reads well after the part before it.

- **The first error of a chain is a clause that says what is
  wrong.** "the cluster URL has no host", "the answer names no
  hostname".
- **A caller adds what it was doing and to which object, as an
  "-ing" phrase.** "reading the placements of films: %w", "watching
  /run/udev: %w". The chain then reads as a sentence: "reading the
  placements of films: opening the catalog: permission denied".
- **Do not write "failed to", "unable to", "could not", or
  "error".** When each caller adds one, the chain becomes "failed to
  read: failed to open: failed to ...", and the reader has to look
  for the fact among them.
- **Add only what the caller alone can tell.** `os.Open` already
  puts the path in its error, so "opening /etc/liken/machine.yaml:
  open /etc/liken/machine.yaml: permission denied" says it twice.
  When a caller has nothing to add, it returns the error as it is.
- **An error that wraps another program's failure includes that
  program's own text, word for word.** For a tool, that is its
  `stderr`. For a daemon socket, a message bus, or a provider, it is
  the response body or the error string. The status field or the
  record that the failure writes includes the text too, so a person
  can read the cause from the log or the status without opening a
  shell.
- **Put the cause last**, so the newest context comes first:
  "reading the catalog: %w". A sentinel that a caller compares with
  `errors.Is` can come first, with the detail after it:
  "%w: no monitor is connected to HDMI-A-1". A sentinel is a clause
  that reads well when it is printed alone.
- **Return an error or log it, not both.** When a function logs an
  error and also returns it, and each caller does the same, the
  error is printed once for each of them.
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
  because the end of the output is the part that stays visible in
  the terminal.
- When the cause is a defect in `liken`, say so, and say what to
  include in a report.

## Log lines

A person searches and filters log lines, so the text that describes
an event must be the same each time the event occurs.

- **An operation that a person causes writes one line.** The line
  says what started the operation, what the program sent, and what
  came back. These operations include a press, a playback request, a
  change of volume, and an edit that an operator acts on. A loop
  that a person does not see, such as a poll, a repeat, or a status
  write that changes nothing, writes no line.
- **The message says what happened, and names the object by its
  kind**: "The scan pod exited", not "exited" or "no events". Write
  it in the past tense for an action, and in the present tense for a
  state that continues.
- **With a logger that takes fields, the message is fixed text and
  each value is a key and a value.**
  `slog.Warn("The scan pod exited", "pod", name, "exitCode", code)`,
  not `slog.Warn(fmt.Sprintf("scan pod %s exited with %d", name,
  code))`.
- **A key is a whole word in lowerCamelCase.** Each thing has the
  same key in every component. The error is `error`. An object is
  under its kind: `pod`, `machine`, `claim`.
- **A component that prints formatted lines puts the parts in the
  same order in each line**: its prefix, what it was doing or what
  happened, the object, and the cause last. `liken: machine node-3
  sent no heartbeat for 90 seconds, and is now Lost`.
- A log line contains the detail that an `Event` leaves out. The
  root `AGENTS.md` says which facts go to a condition, an `Event`,
  and a log line.

## Conditions and Events

The rules for a reason, for which transitions post an `Event`, and
for the code that writes them are in the `operators` skill under
`.agents/skills`. The rules here are for the message.

- **A condition's message says what is true now**, in the present
  tense, because `kubectl wait` and a person can read it at any time
  after it is written. "The receiver reports input HDMI 2, and the
  Player declares HDMI 1".
- **The `Event` of a transition has the condition's message**,
  because `Recorder.SetCondition` posts it. So write the message so
  that it is correct in both places.
- **The `Event` of an action that changes no condition says what the
  component did**, in the past tense: "Created the scan pod
  scan-films-7f3a again after it was deleted".
- **When a condition needs a person to act, its message names the
  field to change** and the value that the component read from it:
  "The ResourceClaimTemplate appearances-gpu that
  spec.appearances.gpuResourceClaimTemplate names is not in the
  namespace films".
- A message that reports an error says what is true first, and the
  cause after it: "The Node does not have the declared labels: the
  API server answered 403". An error chain alone says what the
  operator was doing, and not what is true.
- A message contains the values. It does not repeat what the reason
  and the condition type already say.

## Test failures

A person reads a test failure in CI output, without the test's
source open.

- **Say what the code returned and what the test wanted**, in that
  order, with the word "want": "the record is {Running}, want
  Scanned". For a function, `Parse(%q) = %v, want %v`.
- **Say when**, if the test checks a state more than once: "after
  the retry, 2 scan pods remain, want none".
- **Say why the wanted value is correct** when that is not obvious:
  "want Failed because the log could not be read".
- A step of the setup that fails reports like an error value:
  `t.Fatalf("writing the PersistentVolume: %v", err)`.
- [`names.md`](names.md) has the rules for the name of a test.
