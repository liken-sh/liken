# Clients that cannot run an agent

The catalog reaches a screen because the screen runs a Corrosion
sidecar. A phone, a laptop browser, or a Home Assistant integration
runs none, and today they have no way to the catalog at all. Jellyfin
stays the app for those devices for that reason.

Three ways in exist and none is built. Corrosion's HTTP API on any
agent answers queries over the network, and an agent in the cluster
could be exposed read-only for that. Corrosion also speaks the
PostgreSQL wire protocol as an experimental listener, which would let
any Postgres client read the catalog. And the bus still carries the
scanners' status, so a client that needs only "something changed" has
that today.

Whichever is chosen, it is a service in the read path for those
clients only, and it must not become one for screens. The design's
rule stands: a screen reads its own file.
