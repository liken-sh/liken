# The catalog

Plan 03. The catalog and the thing that carries it: a Corrosion
cluster with an agent beside every scanner and every browser. At the
end of this plan a scanner pod's sidecar and a workstation cluster
hold the same schema, a write on one node reaches every other, and
the numbers that decide the pod specs are written down here.

## The problem

Many screens need the same catalog, each on its own box, fresh
within a second of a change, with no service in the read path and
no database to run. One scanner per library writes it. The catalog
is derived, so it needs no durability beyond "rebuild by rescan",
and it needs push, not polling, because a browser that polls a share
once a second per catalog per room is traffic the NAS pays for
nothing.

Two other shapes were built and measured before this one, and both
are in [`rejected/`](rejected/): a Litestream replica on an NFS
export read through its VFS extension, and dqlite. The Litestream
path worked and lost on three counts: reads were polled at a
one-second interval, the reader wedged or died under level-1
compaction in a race the proof of concept hit in six of eleven runs,
and the extension put a Go runtime and 64 MB of resident memory
inside the Rust client.

## The contract

**One cluster, one schema.** Every agent in the cluster loads the
same schema file, and that file ships in this repository and in every
image that runs an agent. Corrosion applies schema changes by diff on
restart, adds tables and columns and indexes, and refuses to drop
any. The rules its cr-sqlite layer imposes are the schema's rules:
only `CREATE TABLE` and `CREATE INDEX`; every non-null column has a
default; no unique indexes beyond the primary key; a primary key on
every table.

**Header and body.** Each item table has the columns every kind
shares and every shelf sorts or filters on: an id, the library it
belongs to, its kind, its path on the share, a title, a sort key, a
year or date, when it was added, the path of its primary art, and a
duration where one exists. The kind's own shape rides in one JSON
column. Films and series each get their structure from plan 04:
films are one table, series are a series table and an episode table.
Indexes cover the sorts and filters the browser does per library.

**Writes go through the agent.** A scanner posts statements to its
own sidecar's `/v1/transactions`, in batches. Five hundred statements
per request worked first time in the proof of concept and seeded
5000 titles in half a second. Nothing writes to the SQLite file
directly, because a write outside the agent corrupts the CRDT clocks
for that table. A screen never writes at all.

**Reads come from the file.** A browser opens its own sidecar's
database read-only with a stock SQLite, no extension, and queries it
like any file. An attribute query with a filter and a sort answered
in under 4 ms at the median from a 105,000-row file while the cluster
was syncing.

**Changes are pushed.** A browser subscribes to its sidecar's
`/v1/updates/{table}` stream, which sends the primary key of each row
that changed and nothing else, and re-reads those rows from its file.
That stream delivered a change 17 ms after the write at the median.
The richer `/v1/subscriptions` stream, which re-evaluates a query and
returns rows, took 290 ms and keeps a SQLite file per subscription;
it is not the browser's path. A client must read the stream unbuffered
or the latency it sees is its own; Corrosion's `corro-client` crate
does this correctly and is the reference.

**The sidecar.** One container spec, shared by the scanner pod and
the idle pod: the Corrosion image, a config that binds the API to
localhost and the gossip port to the pod's address, a schema mount,
and an `emptyDir` for the database. The agent's page cache is set
well below its 1 GiB default; 64 MiB cost nothing measurable in
speed and saved up to 94 MB of resident memory per agent. Agents
find each other through a headless `Service` over the pods that run
one, given to each agent as its bootstrap list.

**The pod specs carry three findings.** An agent that ingests a
large catalog for the first time peaks at 240 to 380 MB resident and
holds that until it restarts; restarted on the same files it sits at
74 MB. So a screen's sidecar restarts once after its first full sync,
or the pod arranges to start with the catalog already on disk. A
busy agent took more than 30 s to answer `SIGTERM`, so pods that run
one set a longer termination grace period. Every agent holds every
table of the cluster, so a films-only screen also carries the series
catalog, and the day photos arrive at a hundred thousand rows this
becomes the question in
[every node holds every table](open-problems/every-node-holds-every-table.md).

**The bus keeps two jobs.** The catalog needs no bus: pushes come
from the agent. The bus carries the scanner's status report to the
operator, and it stays the signal for any client that cannot run an
agent, like a phone app later.

**Patches.** The project builds Corrosion from a pinned commit and
carries whatever patches it needs in this repository. None are known
at the outset.

## The local harness

`local/` gains a script that brings up a three-agent cluster on the
workstation, on localhost ports above 20000, with the schema, and
tears it down without leaving an agent behind. The proof of concept's
scripts are its model. Every later plan's harness starts from this
cluster.

## What was set aside

A query service of any kind, REST, Meilisearch, or Postgres: each
puts a process in the read path and none gives push. Litestream, for
the reasons above. dqlite, which has no client outside Go and makes
every screen a Raft member. Symlink index trees on the share, which
the catalog answers with a query. Each has a note in
[`rejected/`](rejected/).

## Proof

On the workstation: the three-agent cluster holds the schema, a
seed of the lab's films posted through one agent appears in the
other two within a second, and a row changed on one appears in a
subscriber's update stream on another within a tenth of a second. On
`liken-1`: two scanner pods' sidecars form a cluster through the
headless `Service`, and a write through one is read from the other's
file.
