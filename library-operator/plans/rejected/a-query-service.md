# A query service in the read path

Three shapes put a process between a media browser and the catalog. All
three were set aside for one reason: a screen reads a file on its own
disk, and nothing has to be up for a screen to draw.

A REST API owned by the operator, with an embedded search index such as
Bleve, would put the operator in every read. It would provide a smaller
version of the service that Meilisearch provides. Meilisearch or
Typesense would add a query language and clients for every language.
Meilisearch and Typesense each need a process with its own memory. That
process does not fit a one-gigabyte box, and Meilisearch's issue tracker
shows that its indexing memory is not always capped. Postgres would
answer any query for any client, but a `liken` cluster does not include
Postgres.

Full-text search is a small part of the use. A media browser filters and
sorts by attributes, and a relational file with indexes does that in
under 4 ms. None of the three gives push, and Corrosion's update stream
does.
