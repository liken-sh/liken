# Plans

This directory holds the driver's design documents. Each one is
numbered in sequence and keeps its number for life.

[`00-design.md`](00-design.md) is the design. The numbered plans build
it, in order. A plan states a problem, states the contracts that answer
it, and states how the work is proved. It leaves the shape of the code
to whoever builds it. Each plan starts at low fidelity, and it is
raised to full fidelity before anyone builds it.

A plan moves to `completed/` when it is built.
A plan that is set aside moves to `rejected/` with the reasons that
decided it. A question the current work cannot answer is written to
`open-problems/`; those documents have no number, because nobody has
decided yet what work they become.

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
