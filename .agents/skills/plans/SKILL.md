---
name: plans
description: Writes, builds, closes, or rejects a plan or an open problem in a plans/ directory of the liken repository - which plans/ directory a document belongs in, its number and its header, what a plan holds and what it leaves out, the move to completed/ in the commit that builds the plan, the links to fix after a move, and when an open problem is written, kept, or deleted. Use before writing a plan or a design document, before the commit that builds a plan or a milestone, when a bug or a design question needs a record, and when a document in plans/ moves, closes, or changes. To survey every open plan and choose work, use the open-work skill.
---

# Plans

A plan records a problem, the design that answers it, the reason for
each decision, and what the lab measured when the work ran. The
directory that a plan is in states its status, so a person who lists
`plans/` reads what is open without opening a file. That only works
when each commit keeps the directories true. A plan that is built and
still at the top of `plans/` tells the next session that the work is
not done, and past surveys reported built plans as open for days.

`plans/README.md` at the top of the repository states the layout for
a person. This skill is the procedure.

## Where a document goes

Each component keeps the plans for work inside it in its own
`plans/`. The root `plans/` holds the plans that cover more than one
component. The plans of the OS are in `liken/plans/`, and the OS
calls its plans milestones.

Inside a `plans/` directory:

- A Markdown file at the top is a plan that is not built yet.
  `00-design.md`, where a component has one, is the design of the
  component as a whole.
- `completed/` holds the plans that are built.
- `rejected/` holds the plans that were built and then removed. The
  document stays as the record of what came out and why.
- `open-problems/` holds bugs and design questions that are not
  resolved.

A root plan stays at the level of the idea: the components, the
contract between them, and a link to the plan of each component that
designs a part. The mechanisms, the evidence, and the tables go in
the plan of the component that owns them. Detail in a root plan went
out of date as the components were built, because the commits that
changed it were in other directories.

## Write a plan

1. Look for a plan or an open problem that already covers the work,
   in the component and in the root. When two documents describe one
   problem, make one plan of them. Do not narrow each one to what the
   other left.
2. Take the next number after the highest one in that `plans/`
   directory and its subdirectories. Each directory numbers its own
   plans, and a number never changes. Name the file
   `<number>-<subject-in-words>.md`.
3. Start with a header that states the date and the status, as in
   "Proposed on 2026-11-10. Nothing is built."
4. State the problem, the design, the alternatives that were set
   aside with the reason for each, and how the work will be proved. A
   plan states contracts, such as a field, a condition, a message, or
   an interface between two components. It leaves the shape of the
   code to the session that builds it.
5. Write in the future tense for what is proposed. `brand/voice.md`
   governs the text.

A component's code exists only where a plan calls for it, so write
the plan before the code. A small fix that a test reproduces and that
changes no design needs no plan.

Plan numbers repeat across components, so name a plan with its
component wherever you cite it: "`library-operator` plan 84", "root
plan 85", "`liken` milestone 89".

## Close a plan in the commit that builds it

The commit that builds a plan also moves it. Do not leave the move
for a later commit or a later drill.

1. Before the move, search the whole repository for the file's name,
   in code comments, YAML, each `AGENTS.md`, each skill, and other
   plans: `grep -rn "<file name>" .`
2. `git mv` the document to `completed/`.
3. Date its header with what was built, as in "Built on 2026-11-20.",
   and state what the lab measured if a drill ran. Keep the proposal,
   the decisions, and the measurements as they were written.
4. Update each hit of step 1 to the new path, and fix each relative
   link inside the moved file for its new depth. Search again, and
   expect no hit on the old path.
5. When part of the plan is not built, the built part still closes.
   Move each unbuilt part to a plan that already holds it, to a new
   plan, or to an open problem in the component that owns it, and
   name that document in the header.

A drill that has not run does not keep a plan open, and it does not
become an open problem. The header lists the drills that have not
run.

When a plan was built and the work was then removed, move the
document to `rejected/` in the commit that removes the work, and
state in its header what came out and why.

## Open problems

An open problem has no number, because its scope is not settled. Its
file name states the problem in words.

- Write one only for a problem that a person or CI can hit. For a
  bug, write the failing test first. When the fix needs no design
  decision, fix the bug and write no document.
- When the fix needs a decision from the maintainer, the document
  holds the evidence: the test's setup, its assertion, and the line
  of its failure, the possible remedies, and the question to decide.
  Do not commit the failing test.
- Delete the document with `git rm` in the commit that fixes the
  problem, and state the fix in the commit message. Do not move it to
  `completed/`.
- When no test or real use shows the problem, delete the document.
  Read the code for the fault first, because one failed attempt to
  reproduce a race does not show that the race is absent.

## Change a plan that exists

A plan records the design and the evidence at the time it was
written. In a plan, correct a path, a function name, a link, or a
sentence about the system today, and add a dated status paragraph for
a part that is now built. Do not rewrite a proposal, a decision, a
date, or a measurement, and do not rewrite a completed plan to
describe today's implementation. "Rewriting" in `brand/voice.md`
holds the rule.
