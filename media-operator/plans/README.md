# Plans

This directory holds the operator's design documents.
[`00-design.md`](00-design.md) is the founding design.

This directory numbers its own plans. A new plan takes the next number
after the highest one in this directory and its subdirectories. A number
never changes. Open problems have no number.

The form follows liken's own `plans/`. A document states a problem,
states the design that answers it, and states what was considered and
set aside. It also states how the work was proved, and a proof runs on
hardware. The pattern is documented in liken's repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md).

The README states what the operator is. These documents state why it
is built this way and which questions still need an answer.
[`completed/`](completed/) holds the plans that are built.
[`open-problems/`](open-problems/) holds the questions the design still
owes an answer to. [`rejected/`](rejected/) holds what was weighed and
set aside. A plan that is not built or set aside stays in this
directory.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open. The built part closes, and the part still owed
becomes a new plan or an open problem.
