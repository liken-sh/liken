# Plans

This directory holds the design documents for work that crosses the
repositories of the organization. Work inside one repository keeps its
plans in that repository's own `plans/`.

A document states a problem, the design that answers it, and what was
considered and set aside. It separates what was measured from what was
only read, and it names where the measurement ran. A plan closes in
the commit that finishes it. That commit moves the document to
`completed/` and dates its header.

## Completed

* [The polling audit](completed/the-polling-audit.md). Completed on
  2026-09-27. The audit read every repository against the rule "Keep
  state current with events", and measured the API server's load on a
  nine-machine home cluster. The measurement overturned the ranking
  from reading the code and found four causes: a USB sound card's
  serial shared by three machines, a wake storm in library-operator, a
  30 s relist of every `PersistentVolume`, and a 5 s list of
  `PairingRequest` objects. After the fixes, the rate at rest went from
  about 549,000 requests an hour to about 227,000. The audit also
  produced the three guards of a watch loop.
