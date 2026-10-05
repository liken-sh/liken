# Plans

This directory contains the operator's design documents.

Each `plans/` directory numbers its own plans. A new plan takes the
next number after the highest one in this directory and its
subdirectories. A number never changes. Open problems have no number.

The form follows the plans of the OS in `liken/plans/`. A document
states a problem, the design that addresses it, and the alternatives
that were considered and set aside. It separates measurements from
source readings and names where each measurement ran.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open, and it does not become an open problem. The
header states which drills have not run. The built part closes, and
any work still unbuilt becomes a new plan or an open problem.

The pattern these documents follow is documented in the top-level
`plans/` of the repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md),
and this operator's own instance,
[milestone 58](https://github.com/liken-sh/liken/blob/main/plans/completed/58-the-bluetooth-operator.md).

[`completed/`](completed/) contains the plans that are built.

[`open-problems/`](open-problems/) contains the questions this
operator still needs to answer. Those documents have no number because
nobody has decided yet what work they become.

[`rejected/`](rejected/) contains the designs that were set aside,
superseded or removed. Each document stays as the record of what was
considered and why.
