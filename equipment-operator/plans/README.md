# Plans

This directory holds the operator's design documents. A plan that is
not built yet stays here. A plan that is built moves to `completed/`.

Each `plans/` directory numbers its own plans. A new plan takes the
next number after the highest one in this directory and its
subdirectories. A number never changes. Open problems have no number.

The form follows the plans of the OS in `liken/plans/`. A document
states a problem, states the design that answers it, and states what
was considered and set aside. It also states how the work was proved,
and a proof runs on hardware.

The README states what the operator is. These documents state why it
is built the way it is, and what it still owes an answer to.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open, and it does not become an open problem. The
header states which drills have not run. The built part closes, and
any work still unbuilt becomes a new plan or an open problem.

[`completed/`](completed/) holds the plans that are built.

[`open-problems/`](open-problems/) holds the questions this operator
owes an answer to. Those documents have no number, because nobody has
decided yet what work they become.
