# Clients that cannot run an agent

The catalog reaches a screen because the screen runs a Corrosion
sidecar. A phone, a laptop browser, and a Home Assistant integration
run none, and they have no path to the catalog. Jellyfin stays the
app for those devices for that reason.

Three paths exist and none is built. Corrosion's HTTP API on any
agent answers queries over the network, and one agent in the cluster
could be exposed read-only for that. Corrosion also has an
experimental PostgreSQL wire-protocol listener, which would let any
Postgres client read the catalog. And the scanners' status goes over
the bus, so a client that needs only "something changed" has that.

Whichever is chosen is a service in the read path for those clients
only. The rule for screens stands: a screen reads its own file.
