---
name: open-work
description: Surveys every open plan and open problem in the repository, checks each one against the code and git history for whether it is still open and whether its facts are still true, reports the result as one table per component, and picks a line of work. Covers the completion check, the fact check, the verification of each finding before it is reported, the housekeeping commit that closes built plans and corrects stale facts, and the failures that past surveys made. Use when the maintainer asks what is open, what's left, what's next, for the backlog, to audit or interrogate the plans and problems, or to choose work for the day. To write or close one plan, use the plans skill.
---

Survey the open work, check each document against the code, and
report what is really open.

Additional context from the maintainer: $ARGUMENTS

If that context names components, asks only for a short reminder, or
asks for a pick under a constraint (such as "not the OS" or "nothing
that needs my input"), follow it in place of the defaults below.

**Core principle: a plan's text is a claim, not a fact. Every verdict
and every stale fact in the report cites a commit hash or a
`file:line` that you read yourself, or that an agent read and you
checked.** Past surveys repeated a claim from a plan without checking
it, kept built plans open for days, and reported functions as missing
when they had only moved.

The conventions are in `plans/README.md`, and the procedure for one
plan is in the `plans` skill. Read both first.

## 1. List the open documents

```sh
for d in $(find . -type d -name plans -not -path '*/node_modules/*'); do
  ls "$d" | grep -v -e completed -e rejected -e open-problems \
    -e README.md -e 00-design.md | sed "s|^|$d/|"
done
find . -path '*/plans/open-problems/*.md' -not -path '*/node_modules/*'
```

`00-design.md` is a component's design overview, and `README.md` is an
index, so neither is open work. Name each plan with its component,
as the `plans` skill says, because plan numbers repeat across
components.

For a short reminder ("what's open?"), stop here and give one line per
document from its header. Say that the list comes from the headers and
is not checked.

## 2. Check each document

A survey of one component runs inline, with no agents: check each
document yourself.

For a larger survey, split the list from step 1 across read-only
agents by component, with about 15 to 20 documents for each agent. So
the number of agents depends on the length of the list. Give each
agent `plans/README.md`, its list of paths, the verdicts below, and
the failures in section 4. Tell it that it edits nothing. Tell it
about any work you already know of, such as a plan whose first step
was committed today, so that the agent does not report it as new.

Each document gets one verdict:

| Verdict | Meaning | What follows |
|---|---|---|
| OPEN | Nothing is built. | Nothing. |
| PARTLY BUILT | Some parts are built. Name them. | Check that the header says which parts. |
| BUILT | Everything is built. | Close it in the housekeeping commit. |
| SUPERSEDED | Other work replaced it. Name the work. | Close or delete it, after the maintainer decides. |
| RESOLVED | An open problem no longer happens. | Delete it, citing the fix. |

The completion check looks in the code for the mechanism that the
document proposes, not for its words. `git log --oneline -- <path>`
shows whether anything beyond the document itself touched it. `git log
--grep` with the plan's number and its subject finds a commit that
built it and cited it. A grep for the field, function, flag, or file
that the design names shows whether it exists.

The fact check covers every concrete claim about the present: a
function and its file, a field and where it is in the schema, a
version, a base image, a default, a link. Compare each claim with the
code as it is now.

A drill that has not run never keeps a plan open. The rule is in
"Close a plan in the commit that builds it" in the `plans` skill.

## 3. Verify the findings before you report them

Agents return wrong findings with the same confidence as right ones.
Before a finding goes into the report, check it yourself with one
`grep` or one read:

- "Function X does not exist": `grep -rn "func.*X\b"` across the
  component. A survey once reported `screenOf` missing while it was in
  `media-operator/screen.go`.
- "Field X is read-only" or "the spec field is Y": read the CRD
  schema and print the path from the root of the schema to the field.
  A survey once reported `spec.denon.settings.hdmi.passThrough` as a
  status-only value, and it is a spec field.
- "Section X still proposes what is built": read the whole document.
  A survey once flagged a proposal section while the document already
  had an "as built" section that kept the two apart.
- "Version X is stale": find out which version the document means. A
  version in a dated measurement is evidence and stays. A version in a
  sentence about today's build is a fact, and you correct it.

Drop each finding that fails the check. In the report, say how many
findings you dropped and why, so that the maintainer knows how far to
trust the agents.

## 4. The failures that past surveys made

- **The survey read only titles and headers.** A header goes stale
  first. Read the design, then the code.
- **The work was built under another component's plan.** The steps of
  a root plan are built by component plans, and a problem in one
  component can be fixed in another. Root plan 74's header said "Not
  built" while plans of `observatory-operator` had built its mode 1. Search
  the whole repository for the mechanism, not only the component that
  owns the document.
- **A plan's claim was repeated as a fact.** Plan 66's survey of
  2026-09-11 named a race that the code no longer had, and the
  assistant repeated it to the maintainer as a live bug.
- **A dated measurement was flagged as stale.** Plans record the
  evidence at the time they were written. Keep the measurement and its
  date. When the system has changed since then, add a sentence that
  says what changed and that nobody has measured again.
- **The finding credited the wrong commit.** Check each hash with `git
  show -s`.
- **An unconfirmed fact went into a header.** "The drills ran on
  `liken-1`" needs the maintainer's statement that they ran, not
  "we're good".
- **A move left broken links.** Every link into a moved file breaks,
  and so do the relative links out of it when its depth changes.

## 5. Report

Give the answer first: how many documents, how many are really open,
which can close, and which have wrong facts. Then:

1. **Can close now**, with the evidence and where the unbuilt parts
   go.
2. **Partly built**, one line each, only where the header is wrong
   or incomplete.
3. **Facts that are now wrong**, one line each, with the document,
   the claim, and the truth with its `file:line`.
4. **Overlaps.** Two documents that describe one problem become one
   plan. Propose the merge, and do not narrow each document to what
   the other leaves out.
5. **The pick**, when the maintainer asked for one.

Each line names the component and the thing, in the words that the
maintainer would use. The maintainer did not take part in each design
discussion, so a bare plan number or a coined term tells them nothing.
Give the tradeoff of each candidate with it.

## 6. The pick

Rank the candidates by the features and the stability they give, not
by effort. Prefer a bug that people hit and that has a finished
design, and say what else it unblocks. If a candidate needs a design
decision from the maintainer, say so, and give the question in
user-facing terms, with the options. Give one pick, a rough plan in
five or six steps, and two or three runners-up with one line each.
Then wait for the maintainer to confirm.

## 7. The housekeeping commit

When the maintainer agrees, make one commit for all of the
housekeeping, separate from the work of the day:

- **A built plan closes.** Follow "Close a plan in the commit that
  builds it" in the `plans` skill, including the search for links
  before and after the move.
- **Two documents that describe one problem become one.** Merge them
  into one document, and remove the other with `git rm`. Fix the
  links to the removed document the same way as for a move.
- **Correct the stale facts.** Follow "Change a plan that exists" in
  the `plans` skill.
- **Write a correction that stays true.** Do not replace one specific
  version, count, or file and line with another, because the new
  value goes out of date again. Say what is true in general and where
  to find the current value. Give a number only as an observation
  with its date.
- **Leave the ambiguous ones.** A claim whose meaning is unclear stays
  unchanged, and the report says why.
- **Delete a resolved open problem** with `git rm`, and give the fix
  in the commit message.

The commit message says what the survey found and why each document
changed. Load the `commit-messages` skill before you write it.
