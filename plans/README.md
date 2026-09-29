# The plans

This directory holds the plans whose work spans more than one
component. Each component keeps the plans for work inside it in its
own `plans/` directory. The plans of the OS, and its design overview
[00-design.md](../liken/plans/00-design.md), are in
[`liken/plans/`](../liken/plans/).

Each plan gives the problem, the design, the reasons for each
decision, and what the lab measured when the work ran. A plan's
directory states its status:

* `completed/` holds the plans that are built.
* `rejected/` holds the plans that were built and then removed. The
  plan stays as the record of what came out and why.
* `open-problems/` holds unresolved bugs and design questions. Each
  document explains the evidence, possible remedies, and whether the
  work needs a design decision.
* The Markdown files at the top of a `plans/` directory are the plans
  that are not built yet.

A plan closes in the commit that builds it. That commit moves the
plan to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open. The built part closes, and the part still owed
becomes a new plan or an open problem.

Each `plans/` directory numbers its own plans. A new plan takes the
next number after the highest one in that directory and its
subdirectories. A number never changes. An open problem has no number,
because its implementation scope is not settled.
