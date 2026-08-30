# dqlite

Canonical's dqlite embeds SQLite and Raft in a C library, with an
elected leader that takes every write and followers that apply its log.
It was weighed for the catalog and set aside without a build.

Every write is a Raft round trip through one node, and followers consume
that node's log. Reads go to the leader by default, and the leader
answers without a majority check, so even those reads can be stale. Its
only bindings are Go. The C library exposes a node and no query API, so
a client in any other language speaks the wire protocol by hand. And
every screen would be a cluster member with a Raft log of thousands of
entries, joining and leaving on every reboot, for a catalog that a
rescan rebuilds. It pays for high availability of writes, which the
catalog does not need.

It fits one later shape: a store that several rooms write to with no
single owner, which is what watch state looks like. Plan 14 names it
there.
