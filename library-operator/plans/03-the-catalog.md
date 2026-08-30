# The catalog

Plan 03. The catalog and the cluster that replicates it: a Corrosion
agent beside every scanner and every media browser. At the end of this
plan a scanner pod's sidecar and a workstation cluster load the same
schema, and a write on one agent reaches every other. The settings the
proof of concept decided are recorded here.

## The problem

Many screens need the same catalog, each on its own box, fresh within a
second of a change, with no service in the read path and no database to
run. One scanner per library writes it. The catalog is derived, so it
needs no durability beyond a rescan. It needs push rather than polling,
because a screen that polls once a second per catalog is traffic every
second for nothing.

Two other transports were built and measured before this one, and both
are in [`rejected/`](rejected/): a Litestream replica on a second
volume, read through its VFS extension, and dqlite. Litestream worked
and lost on three counts. Reads polled at a one-second interval. The
reader wedged or died under level-1 compaction, in a race that six of
eleven runs hit. And the extension put a Go runtime and 64 MB of
resident memory inside the Rust client.

## The contract

**One cluster, one schema.** Every agent loads the same schema file.
That file is in this repository and in every image that runs an agent.
Corrosion applies schema changes by diff on restart: it adds tables,
columns, and indexes, and refuses to drop any. Its cr-sqlite layer
imposes the rest: only `CREATE TABLE` and `CREATE INDEX`; a default on
every non-null column; no unique index beyond the primary key; a primary
key on every table.

**Header and body.** Each item table has the columns every kind shares
and every list sorts or filters on. Those are an id, the library it
belongs to, its kind, its path on the volume, a title, a sort key, a
year or date, the time it was added, the path of its primary art, and a
duration where one exists. The kind's own shape is one JSON column. Plan
04 gives films and series their tables. Indexes cover the sorts and
filters the media browser runs per library.

**Writes go through the agent.** A scanner posts statements to its own
sidecar's `/v1/transactions`, in batches. Five hundred statements per
request seeded 5000 titles in 0.45 s in the proof of concept. Nothing
writes to the SQLite file directly, because a write outside the agent
corrupts the CRDT clocks for that table. A screen never writes.

**Reads come from the file.** A media browser opens its sidecar's
database read-only with a stock SQLite, no extension, and queries it as
a file. A query with a filter and a sort answered in under 4 ms at the
median from a 105,000-row file while the cluster was syncing.

**Changes are pushed.** A media browser subscribes to its sidecar's
`/v1/updates/{table}` stream, which sends the primary key of each row
that changed, and re-reads those rows from its file. That stream
delivered a change 17 ms after the write at the median. The
`/v1/subscriptions` stream, which re-evaluates a query and returns rows,
took 290 ms and writes a SQLite file per subscription, so the media
browser does not use it. A client must read the stream unbuffered, or
the latency it measures is its own; Corrosion's `corro-client` crate is
the reference.

**The sidecar.** One container spec, shared by the scanner pod and the
idle pod. It has the Corrosion image, a config that binds the API to
localhost and the gossip port to the pod's address, a schema mount, and
an `emptyDir` for the database. The agent's page cache is set to 64 MiB,
below its 1 GiB default. That setting cost nothing measurable in speed
and saved up to 94 MB of resident memory per agent. Agents find each
other through a headless `Service` over the pods that run one, given to
each agent as its bootstrap list.

**Three findings shape the pod specs.** An agent that ingests a large
catalog for the first time peaks at 240 to 380 MB resident and keeps
that until it restarts. Restarted on the same files, it uses 74 MB. So a
screen's sidecar restarts once after its first full sync, or the pod
starts with the catalog already on disk. A busy agent took more than 30
s to exit on `SIGTERM`, so pods that run one set a longer termination
grace period. Every agent stores every table of the cluster, so a
films-only screen also stores the series catalog; the first large kind
makes that a budget question, in [every node stores every
table](open-problems/every-node-stores-every-table.md).

**The bus has two jobs.** The catalog needs no bus, because the agent
pushes changes. The scanner's status report goes over the bus to the
operator, and the bus is the signal for a client that cannot run an
agent.

**Patches.** The project builds Corrosion from a pinned commit and keeps
its patches in this repository. None are known at the outset.

## The local harness

`local/` gains a script that starts a three-agent cluster on the
workstation, on localhost ports above 20000, with the schema, and stops
it without leaving an agent running. Every later plan's harness starts
from this cluster.

## What was set aside

A query service of any kind, REST, Meilisearch, or Postgres: each puts a
process in the read path, and none gives push. Litestream, for the
reasons above. dqlite, which has no client outside Go and makes every
screen a Raft member. Symlink index trees on the volume, which a catalog
query answers. Each has a note in [`rejected/`](rejected/).

## Proof

On the workstation: the three-agent cluster loads the schema. A seed of
the lab's films posted through one agent appears in the other two within
a second. A row changed on one agent appears in a subscriber's update
stream on another within a tenth of a second. On `liken-1`: two scanner
pods' sidecars form a cluster through the headless `Service`, and a
write through one is read from the other's file.