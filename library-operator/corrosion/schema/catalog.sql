-- The catalog's schema, in one file that every agent loads: from the
-- image at /etc/corrosion/schema, and from this directory in the
-- local/catalog harness. Corrosion applies the difference on every
-- start. It adds tables, columns, and indexes, and it refuses to drop
-- any. Its cr-sqlite layer imposes the rest: only CREATE TABLE and
-- CREATE INDEX, a default on every non-null column, no unique index
-- beyond the primary key, and a primary key on every table.
--
-- Every kind's table starts with the item header: these eleven
-- columns, in this order. They are the columns every kind shares and
-- every list sorts or filters on. The kind's own shape is the body
-- column, one JSON document. So a new kind is a new table, and never
-- a new nullable column on a table that other kinds share. This file
-- defines the movies table; plan 04 fills the movie body and adds the
-- series tables.
--
--   id        the scanner's own stable id for the item
--   library   the Library that holds the item, as namespace/name
--   kind      the kind that wrote the row
--   path      the item's path on the volume, relative to the library root
--   title     the name a person reads
--   sort_key  the key a list sorts by; "The Matrix" sorts under M
--   released  a year (1999) or an ISO date (2004-09-22); both sort as text
--   added     the time the item was added, in Unix seconds
--   art       the path of the primary art, relative to the library root
--   duration  seconds, or 0 where none exists
--   body      the kind's own shape, as JSON

CREATE TABLE movies (
    id TEXT NOT NULL PRIMARY KEY,
    library TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    sort_key TEXT NOT NULL DEFAULT '',
    released TEXT NOT NULL DEFAULT '',
    added INTEGER NOT NULL DEFAULT 0,
    art TEXT NOT NULL DEFAULT '',
    duration INTEGER NOT NULL DEFAULT 0,
    body TEXT NOT NULL DEFAULT '{}'
);

-- The three sorts a media browser offers over one library: by title,
-- by release, and by the time of arrival. Each index leads with the
-- library column, because every list a screen draws is for one
-- library first.
CREATE INDEX movies_library_sort_key ON movies (library, sort_key);
CREATE INDEX movies_library_released ON movies (library, released);
CREATE INDEX movies_library_added ON movies (library, added);
