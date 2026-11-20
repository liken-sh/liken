# The plans

This directory has the plans for work that covers more than one
component. Each component has its own `plans/` directory for work
inside it. The plans of the OS, and its design overview
[00-design.md](../liken/plans/00-design.md), are in
[`liken/plans/`](../liken/plans/).

A plan describes the problem, the design, the reason for each
decision, and what the lab measured when the work ran. The directory
that a plan is in shows its status:

* A Markdown file at the top of a `plans/` directory is a plan that
  is not built yet.
* `completed/` has the plans that are built.
* `rejected/` has the plans that were built and then removed, and the
  designs that were considered and never built. Each one stays as a
  record of what was decided and why.
* `open-problems/` has bugs and design questions that are not
  resolved. Each document gives the evidence, the possible fixes, and
  whether the work needs a design decision.

A plan closes in the commit that builds it. That commit moves the
plan to `completed/`, adds the date to its header, and says what the
lab measured if a drill ran.

A drill that has not run yet is not a reason to leave a plan open,
and it does not become an open problem. The header lists the drills
that have not run. If part of a plan is not built, the built part
still closes, and the rest becomes a new plan or an open problem.

Each `plans/` directory numbers its own plans. A new plan gets the
next number after the highest one in that directory and its
subdirectories. A number never changes. An open problem has no
number, because nobody has decided yet how much work it is.

The `plans` skill in `.agents/skills` has the full procedure.
