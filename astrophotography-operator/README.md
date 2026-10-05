# astrophotography-operator

`astrophotography-operator` will run imaging sessions on an observatory
that `observatory-operator` controls. It will plan each night from
declared targets one exposure at a time, center and guide through the
observatory's INDI server and PHD2, and record each frame and its grade
in a `Catalog`.

Nothing is built yet. [`plans/00-design.md`](plans/00-design.md) is the
design, and [root plan 74](../plans/74-astrophotography.md) holds the
architecture and the tests behind it.
