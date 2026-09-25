# Clients that cannot run an agent

The catalog reaches a screen because the screen runs a Corrosion
sidecar. A phone, a web browser on a laptop, and a Home Assistant
integration run no sidecar, so they cannot read the catalog. For that
reason, Jellyfin stays the app for those devices.

There are three possible paths, and none is built. Corrosion's HTTP API
on any agent answers queries over the network, so one agent in the
cluster could be exposed read-only for these clients. Corrosion also
has an experimental PostgreSQL wire-protocol listener, which would let
any Postgres client read the catalog. Also, the scanners publish their
status on the bus, so a client that needs to know only that something
changed can read that already.

The chosen path adds a service to the read path for those clients
only. A screen still reads its own copy of the catalog file.
