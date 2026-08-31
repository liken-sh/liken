-- The catalog's schema, in one file that every agent loads: from the
-- image at /etc/corrosion/schema, and from this directory in the
-- local/catalog harness. Corrosion applies the difference on every
-- start. It adds tables, columns, and indexes, and it refuses to drop
-- any. Its cr-sqlite layer imposes the rest: only CREATE TABLE and
-- CREATE INDEX, a default on every non-null column, no unique index
-- beyond the primary key, and a primary key on every table.
--
-- Every item table starts with the item header: these twelve columns,
-- in this order. They are the columns every kind shares and every list
-- sorts or filters on. The kind's own shape is the body column, one
-- JSON document. So a new kind is a new item table, and never a new
-- nullable column on a table that other kinds share.
--
-- This file defines the item tables movies, series, and episodes, and
-- the shared files, file_items, and aliases tables. An item's id is
-- provider-scoped and derived from the sidecar, movie:tmdb:603, so a
-- re-walk of an unchanged sidecar reads the same id. A folder with no
-- provider id falls back to movie:path:<key>.
--
--   id        the item's provider-scoped id, derived from the sidecar
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
--   slug      the legible display name, the-matrix-1999, for a URL or a screen
--
-- The seen table the scanner marks and sweeps against is not in this file.
-- Every table this file names becomes a replicated table that gossips, and
-- a mark on every row of every walk would flood the readers. The scanner
-- creates seen through the write API instead, so it stays a local table
-- the agent never replicates. See prune.go.

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
    body TEXT NOT NULL DEFAULT '{}',
    slug TEXT NOT NULL DEFAULT ''
);

-- The three sorts a media browser offers over one library: by title,
-- by release, and by the time of arrival. Each index leads with the
-- library column, because every list a screen draws is for one
-- library first.
CREATE INDEX movies_library_sort_key ON movies (library, sort_key);
CREATE INDEX movies_library_released ON movies (library, released);
CREATE INDEX movies_library_added ON movies (library, added);

-- The series item table: the same item header as movies, with the series body.
CREATE TABLE series (
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
    body TEXT NOT NULL DEFAULT '{}',
    slug TEXT NOT NULL DEFAULT ''
);

-- The three sorts a media browser offers over one series library, each led by
-- the library column, as the movies indexes are.
CREATE INDEX series_library_sort_key ON series (library, sort_key);
CREATE INDEX series_library_released ON series (library, released);
CREATE INDEX series_library_added ON series (library, added);

-- The episode item table: the item header, then the three columns that place
-- the episode under its series. series is the parent series id, and season and
-- episode are its aired numbers.
CREATE TABLE episodes (
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
    body TEXT NOT NULL DEFAULT '{}',
    slug TEXT NOT NULL DEFAULT '',
    series TEXT NOT NULL DEFAULT '',
    season INTEGER NOT NULL DEFAULT 0,
    episode INTEGER NOT NULL DEFAULT 0
);

-- The one order a media browser draws episodes in: a series, then its seasons,
-- then their episodes, within one library.
CREATE INDEX episodes_library_series_season_episode ON episodes (library, series, season, episode);

-- A file is one physical file on the volume: its path, the library it belongs
-- to, its technical attributes, and its own trickplay path. Every file a
-- title folder holds is a row here, and not the video files alone, so a media
-- browser draws a title's art, subtitles, and extras from the catalog and
-- never from the volume.
--
-- present is 1 on every row the scanner writes. The mark-and-sweep pass
-- in prune.go deletes a file that left the volume rather than marking it
-- absent, so nothing ever writes 0.
--
-- Four columns say what a file is. The scanner reads all four off the file's
-- name, the directory that holds it, and one stat, and it opens no file.
--
--   type      the category, one word from the closed set in files.go
--   role      which one of its kind the file is, in Jellyfin's and Kodi's words
--   language  the two-letter or three-letter tag the file name carries
--   modified  the time the file was last written, in Unix seconds
CREATE TABLE files (
    path TEXT NOT NULL PRIMARY KEY DEFAULT '',
    library TEXT NOT NULL DEFAULT '',
    container TEXT NOT NULL DEFAULT '',
    video_codec TEXT NOT NULL DEFAULT '',
    audio_codec TEXT NOT NULL DEFAULT '',
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    trickplay TEXT NOT NULL DEFAULT '',
    present INTEGER NOT NULL DEFAULT 1,
    type TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT '',
    modified INTEGER NOT NULL DEFAULT 0
);

-- The many-to-many link between a file and the items it holds. A multi-episode
-- file names more than one item, and an item with a second encoding holds more
-- than one file.
CREATE TABLE file_items (
    path TEXT NOT NULL DEFAULT '',
    item TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (path, item)
);

-- The reverse lookup, from an item to its files.
CREATE INDEX file_items_item ON file_items (item);

-- An alias maps one of an item's names to the item: every provider id the
-- sidecar carries, and the folder-name key. So several names resolve to one
-- work, and a lost sidecar still resolves the folder. source records how the
-- name was learned.
CREATE TABLE aliases (
    alias TEXT NOT NULL PRIMARY KEY DEFAULT '',
    item TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT ''
);

-- The reverse lookup, from an item to the names that resolve to it.
CREATE INDEX aliases_item ON aliases (item);
