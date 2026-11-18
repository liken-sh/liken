# astrophotography-operator

`astrophotography-operator` images the `Target`s you declare, on a
telescope that `observatory-operator` controls. During a `Reservation`
whose holder is this operator, it picks a target, slews to it, centers
it with a plate solve, guides with PHD2, and exposes. It moves each
frame to the target's archive and records it in the archive's catalog.

`deploy/` holds the CRDs of `Target` and `Solver`, the RBAC, and the
operator. [`plans/00-design.md`](plans/00-design.md) is the design,
[plan 08](plans/completed/08-the-component-and-the-first-session.md) gives the
order of the build and the test of the first imaging session, and [root plan
74](../plans/completed/74-astrophotography.md) explains the split between this
operator and `observatory-operator` and the contract between them. The manual is at
https://liken.sh/astrophotography/.
