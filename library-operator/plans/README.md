# Plans

This directory holds the operator's design documents.
[`00-design.md`](00-design.md) is the design.

This directory numbers its own plans. A new plan takes the next number
after the highest one in this directory and its subdirectories. A number
never changes. Open problems have no number.

The form follows `liken`'s own `plans/` and `media-operator`'s. A
document states a problem, states the contract that answers it, and
states what was considered and set aside. It also states how the work is
proved, and a proof runs on hardware.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open. The built part closes, and the part still owed
becomes a new plan or an open problem. The pattern is documented in
`liken`'s repository: [milestone 56, device
operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md).

These plans state contracts. Each one leaves the code's shape, the file
layout, and the names to whoever builds it, and expects that person to
plan the build first. Where a plan needs a change in a lower operator,
it describes that change here and names the component.

[`completed/`](completed/) holds the plans that are built. A plan there
records its drill in its own proof section.
[`open-problems/`](open-problems/) holds what the design still owes an
answer to. [`rejected/`](rejected/) holds what was tried or weighed and
set aside, with the measurements that decided it. A plan that is not
built or set aside stays in this directory.
