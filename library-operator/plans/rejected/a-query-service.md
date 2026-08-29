# A query service in the read path

Three shapes put a process between a browser and the catalog, and
all three were set aside for the same reason: a screen should read a
file on its own disk, and nothing should be up for a screen to draw.

A REST API of the operator's own, in front of an embedded search
index such as Bleve, would keep the operator in the middle of every
read on a LAN where the bytes take the short path, and it would have
been a worse Meilisearch. Meilisearch or Typesense would give a query
language and clients in every language, at the cost of a process
with its own memory that cannot run on a one-gigabyte box and whose
indexing memory its own issue tracker shows it does not always cap.
Postgres would answer any query for any client, with `psql` at two in
the morning, and it is a dependency a `liken` cluster does not have.

Full-text search is a small part of the use: filtering and sorting by
attributes is what a browser does, and a relational file with indexes
does that in under 4 ms. The one thing none of these give and the
design needs is push, which Corrosion's update stream delivers.
