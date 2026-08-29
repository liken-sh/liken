# dqlite

Canonical's dqlite embeds SQLite and Raft in a C library, with an
elected leader that takes every write and followers that apply its
log. It was weighed for the catalog and set aside without a build.

It is leader-centric, not peer-to-peer: every write is a Raft round
trip through one node, and followers consume that node's log. Reads
go to the leader by default and can be stale even there, since the
leader answers without confirming a majority. Its only bindings are
Go; the C library gives a node, not a query API, and a client in any
other language speaks its wire protocol by hand. And every screen
would be a cluster member with a Raft log of thousands of entries,
joining and leaving on every reboot, for a catalog that is derived
and rebuildable. It pays for high availability of writes, which the
catalog does not need.

It fits one later shape well: a store that several rooms write to
with no single owner, which is what watch state looks like. Plan 14
names it there.
