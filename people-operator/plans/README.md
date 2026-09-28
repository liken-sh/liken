# Plans

[`00-design.md`](00-design.md) is the design. Plans are numbered in
one sequence, and a plan moves to `completed/` when it is built.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open. The built part closes, and the part still owed
becomes a new plan or an open problem.

* [00, The people-operator design](00-design.md). What a `Person`
  holds, what refers to it, and what a controller would add later.

## Completed

None yet. The first plan is the CRD itself, and it ships with the
design.
