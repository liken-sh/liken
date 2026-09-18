# Plans

This directory contains the driver's design documents. Each document is
numbered in sequence, and its number never changes.

[`00-design.md`](00-design.md) is the design. The numbered plans build
it, in order. Each plan states a problem, the contracts that address it,
and how the work is proved. It leaves the shape of the code to whoever
builds it. Each plan starts at low fidelity and reaches full fidelity
before implementation.

A plan moves to `completed/` when it is built.
A plan that is set aside moves to `rejected/` with the reasons that
decided it. A question the current work cannot answer is written to
`open-problems/`. Those documents have no number because no work item
exists for them yet.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open. The built part closes, and the part still owed
becomes a new plan or an open problem.

## Designs

* [00, Design](00-design.md). The volume as a set of copies, one per
  node, the six invariants that hold them, the store on each node, and
  what the driver reports.
* [01, Prometheus metrics](completed/01-prometheus-metrics.md). Built and drilled on liken-1 on 2026-09-10. The
  driver serves Prometheus metrics on port 9200 under liken's shared
  contract: CSI operations as the reconcile layer, volumes mounted, and
  mount failures.
