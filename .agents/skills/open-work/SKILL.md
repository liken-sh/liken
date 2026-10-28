---
name: open-work
description: Survey every open plan and open problem in the repository, check each one against the code and git history for whether it is still open and whether its facts are still true, report the result as one table per component, and pick a line of work. Covers the completion check, the fact check, the verification of each finding before it is reported, the housekeeping commit that closes built plans and corrects stale facts, and the failures that past surveys made. Use when I ask what is open, what's left, what's next, for the backlog, to audit or interrogate the plans and problems, or to choose work for the day.
---

Survey the open work, check each document against the code, and
report what is really open.

Additional context from me: $ARGUMENTS

If that context names components, asks for a short reminder only, or
asks for a pick under a constraint (such as "not the OS" or "nothing
that needs my input"), it wins over the defaults below.

**Core principle: a plan's text is a claim, not a fact. Every verdict
and every stale fact in the report cites a commit hash or a
`file:line` that you read yourself, or that an agent read and you
checked.** Past surveys repeated a claim from a plan without checking
it, kept built plans open for days, and reported missing functions
that had only moved.

The conventions are in `plans/README.md`: a Markdown file at the top
of a `plans/` directory is not built, `open-problems/` holds unresolved
bugs and design questions, `completed/` holds built plans, and
`rejected/` holds plans that were built and removed. Read it first.

## 1. List the open documents

```sh
for d in $(find . -type d -name plans -not -path '*/node_modules/*'); do
  ls "$d" | grep -v -e completed -e rejected -e open-problems \
    -e README.md -e 00-design.md | sed "s|^|$d/|"
done
find . -path '*/plans/open-problems/*.md' -not -path '*/node_modules/*'
```

`00-design.md` is a component's design overview and `README.md` is an
index, so neither is open work. Plan numbers repeat across
components, and `liken/plans/` calls its plans milestones, so always
name a plan with its component: "library-operator plan 84", "root
plan 85", "liken milestone 89".

For a short reminder ("what's open?"), stop here and give one line per
document from its header. Say that the list is from the headers and
is not checked.

## 2. Check each document

A full survey is about 70 documents. Split them across about four
read-only agents by component, with about 15 to 20 documents each.
Give each agent `plans/README.md`, the list of paths, the verdicts
below, and the failure modes in section 4. Tell it that it edits
nothing. Name any work you already know about, such as a plan whose
first step landed today, so the agent does not report it as new.

Each document gets one verdict:

| Verdict | Meaning | What follows |
|---|---|---|
| OPEN | Nothing is built. | Nothing. |
| PARTLY BUILT | Some parts are built. Name them. | Check that the header says which parts. |
| BUILT | Everything is built. | Close it in the housekeeping commit. |
| SUPERSEDED | Other work replaced it. Name the work. | Close or delete it, after my decision. |
| RESOLVED | An open problem no longer happens. | Delete it, citing the fix. |

The completion check reads the code for the mechanism the document
proposes, not for its words. `git log --oneline -- <path>` shows
whether anything beyond the document itself touched it. `git log
--grep` with the plan's number and its subject finds a build that
cited it. A grep for the field, function, flag, or file that the
design names shows whether it exists.

The fact check reads every concrete claim about the present: a
function and its file, a field and where it lives in the schema, a
version, a base image, a default, a link. Compare each claim with the
code as it is now.

A drill that has not run never keeps a plan open, and it never
becomes an open problem. The plan closes and its header lists the
drills that have not run.

## 3. Verify the findings before you report them

Agents return wrong findings with the same confidence as right ones.
Before a finding goes into the report, check it yourself with one
`grep` or one read:

- "Function X does not exist": `grep -rn "func.*X\b"` across the
  component. A survey once reported `screenOf` missing while it was at
  `media-operator/screen.go:307`.
- "Field X is read-only" or "the spec field is Y": read the CRD
  schema and print the path from the root of the schema to the field.
  A survey once reported `spec.denon.settings.hdmi.passThrough` as a
  status-only value, and it is a spec field.
- "Section X still proposes what is built": read the whole document.
  A survey once flagged a proposal section while the document already
  had an "as built" section that separated the two.
- "Version X is stale": confirm the version the document means. A
  version in a dated measurement is evidence and stays. A version in a
  sentence about today's build is a fact and gets corrected.

Drop each finding that fails the check. In the report, say how many
findings you dropped and why, so that I know how far to trust the
agents.

## 4. The failures that past surveys made

- **The survey read only titles and headers.** A header goes stale
  first. Read the design, then the code.
- **The work landed under another component's plan.** A root plan's
  steps are built by component plans, and a problem in one component
  can be fixed in another. Root plan 74's header said "Not built"
  while 17 observatory-operator plans had built mode 1. Search the
  whole repository for the mechanism, not only the component that
  owns the document.
- **A plan's claim was repeated as a fact.** Plan 66's survey of
  2026-09-11 named a race that the code no longer had, and the
  assistant repeated it to me as a live bug.
- **A dated measurement was flagged as stale.** Plans record the
  evidence at the time they were written. Keep the measurement and its
  date. When the system has changed under it, add a sentence that says
  what changed and that nobody has measured again.
- **The finding credited the wrong commit.** Check each hash with `git
  show -s`.
- **An unconfirmed fact went into a header.** "The drills ran on
  `liken-1`" needs my statement that they ran, not "we're good".
- **A move left broken links.** Every link into a moved file breaks,
  and so do the relative links out of it when its depth changes.

## 5. Report

Give the answer first: how many documents, how many are really open,
which can close, and which have wrong facts. Then:

1. **Can close now**, with the evidence and where the unbuilt parts
   go.
2. **Partly built**, one line each, only where the header is wrong
   or incomplete.
3. **Facts that are now wrong**, one line each with the document,
   the claim, and the truth with its `file:line`.
4. **Overlaps.** Two documents that describe one problem fold into
   one plan. Propose the fold and do not narrow each document to its
   remainder.
5. **The pick**, when I asked for one.

Each line names the component and the thing in words I would use. I
did not take part in each design discussion, so a bare plan number or
a coined term tells me nothing. State the tradeoff of each candidate
with it.

## 6. The pick

Rank the candidates by the features and the stability they give, not
by effort. Prefer a bug that people hit and that has a finished
design, and say what else it unblocks. A candidate that needs a design
decision from me says so, and gives the question in user-facing terms
with the options. Give one pick, a rough plan in five or six steps,
and two or three runners-up with one line each. Then wait for my
confirmation.

## 7. The housekeeping commit

When I agree, make one commit for all of the housekeeping, separate
from the work of the day:

- **A built plan closes.** `git mv` it to `completed/`, and date its
  header with what it built and what continues where. Each unbuilt
  part goes to a plan that already holds it, or to a new open problem
  in the component that owns it.
- **Fix the links.** Before the move, `grep -rn "<file name>"` across
  the whole repository, including code comments, YAML, `AGENTS.md`,
  and other plans, and update each hit. In the moved file, fix every
  relative link for its new depth. After the move, grep again and
  expect no hit on the old path.
- **Correct the stale facts.** In a plan, correct a path, a function,
  a link, or a sentence about the system today, and add a dated
  status paragraph for a part that is built. Do not rewrite a
  proposal, a decision, or a measurement. Follow the rewriting rules
  of `brand/voice.md`.
- **Leave the ambiguous ones.** A claim whose meaning is unclear stays
  unchanged, and the report says why.
- **Delete a resolved open problem** with `git rm`, and give the fix
  in the commit message.

The commit message says what the survey found and why each document
changed. It does not list the files.
