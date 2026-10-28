# astrophotography-operator

`astrophotography-operator` images the targets a person declares, on
a telescope that `observatory-operator` controls. Within a
`Reservation` held by this operator, it chooses a target, slews,
centers with a plate solve, guides through PHD2, and exposes. It moves
each frame to the target's archive and records it in the archive's
catalog.

`deploy/` holds the CRDs of `Target` and `Solver`, the RBAC, and the
operator. [`plans/00-design.md`](plans/00-design.md) is the design,
[plan 08](plans/completed/08-the-component-and-the-first-session.md) gives the
order of the build and the drill of the first session, and [root plan
74](../plans/completed/74-astrophotography.md) holds the two components and the
contract between them. The manual is at
https://liken.sh/astrophotography/.
