# Instructions for agents

An agent does most of the work in this repository from an
`AGENTS.md` file, a skill, or the brief that another agent wrote
for it. The reader of each is a language model. It reads every
word in every session. It has no memory of the session that wrote
the text, and it applies what it reads closely. A rule that is
wrong, too wide, or out of date changes the work of every session
that loads it. Every rule in [`voice.md`](voice.md) applies to these
texts. The rules here add what they need.

Anthropic and OpenAI each publish guidance on how their current
models read instructions:
[Claude prompting best practices](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/claude-prompting-best-practices),
[how Claude Code loads `CLAUDE.md`](https://code.claude.com/docs/en/memory),
[skill authoring](https://platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices),
[OpenAI's prompt guidance](https://developers.openai.com/api/docs/guides/prompt-guidance),
and [Codex's `AGENTS.md` guide](https://developers.openai.com/codex/guides/agents-md).
Read them again when a new model generation arrives, because the
advice changes with the models.

## Every instruction

- **Give the reason with the rule.** When a rule has a reason, a
  model applies it to the cases that the reason covers. When a rule
  has no reason, the model applies it only to the cases that its
  words name. "Start a watch from a version" is a
  rule. "A watch with no `resourceVersion` first sends every object
  as it is now, so each open costs as much as a list" lets the
  reader decide a case that the rule did not name.
- **State what the rule covers.** A current model does not extend a
  rule from one item to the next. Write "every `fetch.sh` and
  `latest.sh`", not "the fetch scripts". Write the exception beside
  the rule.
- **Write "never" and "always" only for a rule with no exception.**
  A current model reads both as absolute. When a rule has a
  condition, write the condition: "ask before you delete a file that
  the session did not create", not "never delete files".
- **Write in plain sentences, with no capitals for emphasis.**
  "IMPORTANT", "MUST", and "CRITICAL" make a current model apply a
  rule to cases it was not written for. When a rule is ignored, the
  file is too long or the rule is unclear, and a louder word fixes
  neither.
- **Put a rule that allows no exception in a check.** `ci pins`
  fails on an `apk add`, and the instruction says which check
  enforces the rule. An instruction alone is a request.
- **Say what to do.** "Write the reason in the commit message" tells
  the reader where the text goes. "Do not write history in a
  comment" leaves the reader to find the place. Use the negative
  form when the wrong action is the likely one, and give the right
  action with it.
- **Write from the reader's side.** Write each instruction as the
  step that the reader takes, and give its reason in a sentence of
  its own. A description of what a program does leaves the reader to
  work out the step.
- **Give several examples that differ, or none.** A model copies one
  example closely, with its length and its wording. Say that an
  example is an example.
- **Write each rule in one place.** Two copies drift apart. When a
  model reads two rules that disagree, it stops to ask, or it follows
  one of them and you have no way to tell which. Other files link
  to the one copy. When two files can apply to the same work, say
  which one decides.
- **Remove a rule when its cause is gone.** An instruction that
  corrected an older model, such as "check your work twice", "keep
  going until you are done", or "think hard", makes a current model
  do too much of that thing. A caution about a defect that is fixed
  stops work that is now correct.

## AGENTS.md

Every session in a directory loads that directory's `AGENTS.md` and
each file it imports, before the first request. Each line uses
context in every session, and a model follows a long file less
closely than a short one. In November 2026, Anthropic's guidance set
a target of 200 lines for one file, and Codex stopped reading
project instructions after 32 KiB. The guides linked above have the
present values.

- **Write what the tree does not show.** The reader can list a
  directory and read a file. It cannot see a decision, the reason
  for it, a failure that led to a rule, or a command whose name it
  would not guess.
- **Keep what every session in the directory needs.** A procedure
  that one kind of task needs is a skill, and the `AGENTS.md` names
  the skill and says when to load it.
- **Add a rule after a session makes the mistake**, not before. A
  rule for a mistake that no session made takes space from the
  rules that are needed.

## Skills

- **The description says what the skill does and when to load it**,
  in the third person, and starts with the words that a request
  uses. A host selects a skill from the description alone, and it
  can shorten a long description.
- **A skill covers one task.** The body contains what a session
  needs on every run. Put reference material that only some runs
  need in a file beside `SKILL.md`, and link it from the body with a
  sentence that says when to read it. In November 2026, Anthropic's
  guidance set a target of 500 lines for the body.
- **Write only what the reader does not have.** A current model was
  trained on `kubectl`, Go, and git. The skill contains the
  project's commands, its order of steps, and the reasons.
- **Write the steps that have one correct order as a numbered
  list**, with the input and the result of each step. For the rest,
  give the goal and the limits, and let the session choose the
  steps.

## Briefs for another agent

An agent that receives a brief has none of the conversation that led
to it. `editing-prompt.md` is a brief for a prose edit, and an
example of these rules.

- **State the goal and what the result is for**, so the agent can
  decide a case that the brief did not name.
- **State what is already known**, and what was tried and did not
  work, so the agent does not do it again.
- **State the scope**: the files it may change, and the files it must
  not.
- **State what it must not do without a person**, such as a commit,
  a push, or a write to a cluster.
- **Repeat each rule that the agent needs** when the rule is in a
  file that the agent does not load.
- **State what to return**, and ask the agent to separate what it
  checked from what it assumed.

The person who reviews the session also reads each message from one
agent to another, so write it in the same voice as every other text.
