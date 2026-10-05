# Plans

This directory contains the driver's design documents.

This directory numbers its own plans. A new plan takes the next number
after the highest one in this directory and its subdirectories. A number
never changes. Open problems have no number.

[`00-design.md`](00-design.md) is the design. The numbered plans build
it, in order. Each plan states a problem, the contracts that address it,
and how the work is proved. It leaves the shape of the code to whoever
builds it. Each plan starts at low fidelity and reaches full fidelity
before implementation.

A plan moves to [`completed/`](completed/) when it is built. A plan that is set aside moves to [`rejected/`](rejected/)
with the reasons that decided it. A question the current work cannot
answer is written to [`open-problems/`](open-problems/). Those documents have no number
because no work item exists for them yet.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open, and it does not become an open problem. The
header states which drills have not run. The built part closes, and
any work still unbuilt becomes a new plan or an open problem.
