# Plans

This directory holds the operator's design documents.

Each `plans/` directory numbers its own plans. A new plan takes the
next number after the highest one in this directory and its
subdirectories. A number never changes. Open problems have no number.

The form follows the plans of the OS in `liken/plans/`. A document
states a problem, states the design that answers it, and states what
was considered and set aside. It separates what was measured from what
was only read, and it names where the measurement ran.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open. The built part closes, and the part still owed
becomes a new plan or an open problem.

The pattern these documents follow is documented in the top-level
`plans/` of the repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md),
and this operator's own instance,
[milestone 57](https://github.com/liken-sh/liken/blob/main/plans/completed/57-the-display-operator.md).

The manual at [display.liken.sh](https://display.liken.sh) says how
to deploy the operator and how to claim an output. These documents
say why it is built the way it is.

[`completed/`](completed/) holds the plans that are built.

[`open-problems/`](open-problems/) holds the questions this operator
owes an answer to. Those documents have no number, because nobody has
decided yet what work they become.
