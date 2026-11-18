# Names

A name is the shortest text in the repository and the one read most
often. A reader meets a function, a field, a flag, or a test by its
name first, and decides from the name whether to open it. So a name
says what the thing does or holds, in the words that the manual uses
for it. Every rule in [`voice.md`](voice.md) applies to a name. The
rules here add what a name needs.

## Every name

- **Name the thing for what it does or holds.** `releaseUnheldPanels`
  says which panels and what happens to them. `cleanup` and
  `processPanels` do not.
- **Use one word for one thing, everywhere.** When the manual says
  "claim", the code says `claim`, the log key is `claim`, and the
  flag is `--claim`. A second word, such as "reservation" or
  "request", tells the reader that there is a second thing.
- **Use the domain's word.** CEC calls it a "logical address", DRA
  calls it a `ResourceSlice`, and the kernel calls it a "connector".
  A reader who searches the specification finds the same word in the
  code.
- **Leave out words that state quality.** `safe`, `smart`, `simple`,
  `robust`, `fast`, `proper`, and `enhanced` say that the author was
  pleased with the code. When the thing has a property that a caller
  depends on, name the property: `writeFileAtomically`, not `safeWrite`.
- **Leave out words that state history.** `new`, `old`, `legacy`,
  `v2`, `temp`, `fixed`, and `refactored` describe a change. After
  the change, the name is wrong. Give the thing the name it would
  have if it had always been there. A constructor named `New` is
  correct, because it states what the function does.
- **Leave out words that state only the kind of code.** `Manager`,
  `Handler`, `Processor`, `Helper`, `Data`, and `Info` fit any code.
  Name the operation: `Recorder` writes `Event`s, and `eventManager`
  could do anything.
- **Put the unit in the name of a number**: `timeoutSeconds`,
  `sizeBytes`. A Go `time.Duration` already holds its unit and needs
  none.
- **A boolean reads as a statement that is true or false**: `muted`,
  `hasCodec`. State it in the positive, so that no reader must
  work out `!notReady`.
- **Spell the name in full when it is far from its use.** A loop
  index is `i` and a receiver is one or two letters, as Go and Rust
  write them. A field, a flag, a metric, a log key, and an exported
  name is a whole word: `address`, not `addr`. Keep an abbreviation
  that the domain uses as its own word: CEC, DRA, EDID, `id`.

## Packages, files, and directories

- **Name a package, a file, or a directory for its subject.**
  `util`, `common`, `shared`, `helpers`, `lib`, and `misc` state
  that the contents have no subject, and they collect code that has
  none. Find the subject that the code belongs to. If it has none of
  its own, put it beside its one caller.
- **Do not repeat the package in its members.** A caller writes
  `events.Recorder` and `conditions.Set`, not `events.EventRecorder`
  and `conditions.SetCondition`.
- **A Go function that returns a value is named for the value**,
  with no `Get`: `Events.List()`, not `Events.GetList()`.

## Tests

- **The name of a test is a sentence that states the behavior.**
  `TestTheCompositorStartsAgainAfterTheOperatorEndsIt` names the
  subject, what it does, and the condition. When it fails, the name
  alone tells the reader what no longer holds. `TestCompositor`,
  `TestRestart2`, and `TestHandleError` tell the reader which
  function to open.
- **State the result that the test checks.** "works", "correctly",
  "properly", and "successfully" state no result.
  `TestParseWorksCorrectly` becomes
  `TestParseRefusesATruncatedStream`.
- **A row of a table has a name that is a phrase for the case**: "a
  refused adapter", "no monitor connected". The name completes the
  sentence that the test's name starts.

## Names in the API

A kind, a field, a condition, a reason, a label key, a flag, a
metric, and the name of a published skill are interfaces. A person
or a manifest depends on each one, so a rename needs the same care as
any other change to an interface.

- **A kind is a noun for the thing that a person declares**:
  `Player`, `Receiver`, `Library`.
- **A field follows the Kubernetes API conventions**: lowerCamelCase,
  no abbreviation, the unit in the name.
- **A condition type is an adjective or a past participle that states
  what was observed**: `Ready`, `Joined`, `Approved`. A type such as
  `Joining` describes work in progress, and a reader cannot tell what
  `False` means for it.
- **A reason is UpperCamelCase and names one cause**:
  `ReleaseRolledBack`, not `Failed`.
- **A published skill is named for the task**, as a verb phrase:
  `declare-a-player`, `pair-a-controller`.
