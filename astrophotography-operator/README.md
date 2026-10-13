# astrophotography-operator

`astrophotography-operator` will run imaging sessions on an
observatory that `observatory-operator` controls. It will plan each
night from declared targets one exposure at a time, center and guide
through the observatory's INDI server and PHD2, and record each frame
and its grade in the catalog of each target's archive.

It does not image yet. The `Target` and `Solver` resources, the
operator that watches them, and its manifests in `deploy/` are built.
[`plans/00-design.md`](plans/00-design.md) is the design, [plan
08](plans/08-the-component-and-the-first-session.md) gives the order
of the build, and [root plan 74](../plans/74-astrophotography.md)
holds the two components and the contract between them. The manual is
at https://liken.sh/astrophotography/.
