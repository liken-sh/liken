# A query service in the read path

Three shapes put a process between a browser and the catalog. All
three were set aside for one reason: a screen reads a file on its own
disk, and nothing has to be up for a screen to draw.

A REST API of the operator's own, in front of an embedded search index
such as Bleve, would keep the operator in the middle of every read. It
would be a smaller Meilisearch. Meilisearch or Typesense would give a
query language and clients in every language. The cost is a process
with its own memory that does not fit a one-gigabyte box, and
Meilisearch's issue tracker shows its indexing memory is not always
capped. Postgres would answer any query for any client, and it is a
dependency a `liken` cluster does not have.

Full-text search is a small part of the use. A browser filters and
sorts by attributes, and a relational file with indexes does that in
under 4 ms. None of the three gives push, and Corrosion's update
stream does.
