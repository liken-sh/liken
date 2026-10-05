# The plans

This directory holds the plans for the OS. It holds one document for
each milestone. Each document gives the problem, the design, the
reasons for each decision, and what the lab measured when the work
ran. [00-design.md](00-design.md) is the design overview of the OS.

A document's directory states its status:

* [`completed/`](completed/) holds the milestones that are built.
* [`rejected/`](rejected/) holds the milestones that were built and then
  removed. The document stays as the record of what came out and why.
* The markdown files in this directory are the milestones that are not
  built yet.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open, and it does not become an open problem. The
header states which drills have not run. The built part closes, and
any work still unbuilt becomes a new plan or an open problem.

Each `plans/` directory in the repository numbers its own plans. A new
plan takes the next number after the highest one in this directory and
its subdirectories. A number never changes. A plan that spans more than
one component is in [`plans/`](../../plans/) at the top of the
repository.

[`open-problems/`](open-problems/) records unresolved bugs and design
questions. Each document explains the evidence, possible remedies, and
whether the work needs a design decision. These documents have no
number because their implementation scope is not settled.
