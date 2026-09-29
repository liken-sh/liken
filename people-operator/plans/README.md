# Plans

[`00-design.md`](00-design.md) is the design. A plan moves to
`completed/` when it is built.

This directory numbers its own plans. A new plan takes the next number
after the highest one in this directory and its subdirectories. A number
never changes. Open problems have no number.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open. The built part closes, and the part still owed
becomes a new plan or an open problem.
