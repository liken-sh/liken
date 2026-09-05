-- The catalog's schema, in one file that every agent loads: from the
-- image at /etc/corrosion/schema, and from this directory in the
-- local/catalog harness. Corrosion applies the difference on every
-- start. It adds tables, columns, and indexes, and it refuses to drop
-- any. Its cr-sqlite layer imposes the rest: only CREATE TABLE and
-- CREATE INDEX, a default on every non-null column, no unique index
-- beyond the primary key, and a primary key on every table.
--
-- Every item table starts with the item header: these twelve columns,
-- in this order, with the library first because the library scopes
-- every row, and (library, id) as the primary key. They are the
-- columns every kind shares and every list
-- sorts or filters on. The kind's own shape is the body column, one
-- JSON document. So a new kind is a new item table, and never a new
-- nullable column on a table that other kinds share.
--
-- This file defines the item tables movies, sets, series, and episodes,
-- the runs table at the end,
-- and the shared files, file_items, and aliases tables. An item's id is
-- provider-scoped and derived from the sidecar, movie:tmdb:603, so a
-- re-walk of an unchanged sidecar reads the same id. A folder with no
-- provider id falls back to movie:path:<key>.
--
-- An id is unique inside one library and never across the namespace,
-- so the library and the id together name a row. Two libraries hold
-- the same id, or the same relative path, without touching each other.
--
--   library   the Library that holds the item, as namespace/name
--   id        the item's provider-scoped id, derived from the sidecar
--   kind      the kind that wrote the row
--   path      the item's path on the volume, relative to the library root
--   title     the name a person reads
--   sort_key  the key a list sorts by; "The Matrix" sorts under M
--   released  a year (1999) or an ISO date (2004-09-22); both sort as text
--   added     the time the item was added, in Unix seconds
--   art       the path of the primary art, relative to the library root
--   arts      every art file beside the item, as a JSON list of paths
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
    library TEXT NOT NULL DEFAULT '',
    id TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    sort_key TEXT NOT NULL DEFAULT '',
    released TEXT NOT NULL DEFAULT '',
    added INTEGER NOT NULL DEFAULT 0,
    art TEXT NOT NULL DEFAULT '',
    arts TEXT NOT NULL DEFAULT '[]',
    duration INTEGER NOT NULL DEFAULT 0,
    body TEXT NOT NULL DEFAULT '{}',
    slug TEXT NOT NULL DEFAULT '',
    set_id TEXT NOT NULL DEFAULT '',
    nfo_facts TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (library, id)
);

-- The movie item table adds one column after the header: set_id is the
-- id of the set the movie belongs to, a row of the sets table, or empty
-- where the sidecar names none.
--
-- nfo_facts holds the names of the nfo facts the title's sidecar already
-- answers, each name wrapped in commas, as in ",overview,credits,". The
-- scanner derives it from the elements the sidecar holds, and a gap query
-- reads it with instr(). The list carries the names and not the values,
-- so a new nfo fact needs no new column.
CREATE INDEX movies_library_sort_key ON movies (library, sort_key);
CREATE INDEX movies_library_released ON movies (library, released);
CREATE INDEX movies_library_added ON movies (library, added);

-- The one read a media browser makes of a set: every movie in it, in
-- release order, for the strip on a movie's page.
CREATE INDEX movies_library_set_id ON movies (library, set_id);

-- The sets item table: the item header, and no body of its own. A set
-- is the collection a movie sidecar names, such as a film and its
-- sequels, and the scanner derives every row from the movies that name
-- it. Its id is provider-scoped like a movie's, set:tmdb:<id> from the
-- id the sidecar carries, or set:name:<slug> where it carries a name
-- alone. Its released and art are its earliest member's, so a set sorts
-- and draws by its first film. A set with no member leaves the catalog
-- with the prune, because nothing else holds it.
CREATE TABLE sets (
    library TEXT NOT NULL DEFAULT '',
    id TEXT NOT NULL,
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
    PRIMARY KEY (library, id)
);
CREATE INDEX sets_library_sort_key ON sets (library, sort_key);

-- The series item table: the same item header as movies, with the series body.
-- series carries the same nfo_facts column as movies, for the same
-- reason: a tvshow.nfo answers the same nfo facts a movie.nfo does.
CREATE TABLE series (
    library TEXT NOT NULL DEFAULT '',
    id TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    sort_key TEXT NOT NULL DEFAULT '',
    released TEXT NOT NULL DEFAULT '',
    added INTEGER NOT NULL DEFAULT 0,
    art TEXT NOT NULL DEFAULT '',
    arts TEXT NOT NULL DEFAULT '[]',
    duration INTEGER NOT NULL DEFAULT 0,
    body TEXT NOT NULL DEFAULT '{}',
    slug TEXT NOT NULL DEFAULT '',
    nfo_facts TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (library, id)
);

-- The three sorts a media browser offers over one series library, each led by
-- the library column, as the movies indexes are.
CREATE INDEX series_library_sort_key ON series (library, sort_key);
CREATE INDEX series_library_released ON series (library, released);
CREATE INDEX series_library_added ON series (library, added);

-- The episode item table: the item header, then the three columns that place
-- the episode under its series. series is the parent series id, and season and
-- episode are its aired numbers.
--
-- A join from the series column to a series id must match the library
-- as well, because an id names one row only inside its own library.
CREATE TABLE episodes (
    library TEXT NOT NULL DEFAULT '',
    id TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    sort_key TEXT NOT NULL DEFAULT '',
    released TEXT NOT NULL DEFAULT '',
    added INTEGER NOT NULL DEFAULT 0,
    art TEXT NOT NULL DEFAULT '',
    arts TEXT NOT NULL DEFAULT '[]',
    duration INTEGER NOT NULL DEFAULT 0,
    body TEXT NOT NULL DEFAULT '{}',
    slug TEXT NOT NULL DEFAULT '',
    series TEXT NOT NULL DEFAULT '',
    season INTEGER NOT NULL DEFAULT 0,
    episode INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (library, id)
);

-- The one order a media browser draws episodes in: a series, then its seasons,
-- then their episodes, within one library.
CREATE INDEX episodes_library_series_season_episode ON episodes (library, series, season, episode);

-- The two reads the home page's recency strips make of episodes, newest
-- release first and newest arrival first, each led by the library column
-- the way the movies and series indexes are.
CREATE INDEX episodes_library_released ON episodes (library, released);
CREATE INDEX episodes_library_added ON episodes (library, added);

-- A file is one physical file on the volume, named by the library that
-- holds it and its path relative to the library root, which together
-- are the primary key. The row carries the file's technical attributes
-- and its own trickplay path. Every file a title folder holds is a row
-- here, and not the video files alone, so a media
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
--
-- arrived is the time the arrival ledger beside the file holds for it, in
-- Unix seconds, and 0 where the ledger holds no entry. The walk writes it
-- from the ledger alone and never from the change time, so the arrival
-- fact's gap is every video with 0 here. The column is last because
-- Corrosion adds a column to a table that exists at its end.
CREATE TABLE files (
    library TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
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
    modified INTEGER NOT NULL DEFAULT 0,
    arrived INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (library, path)
);

-- The one read the arrival fact and the reporter make of files: every
-- video of one library with no arrival, led by the library column as every
-- index in this schema is.
CREATE INDEX files_library_arrived ON files (library, arrived);

-- The many-to-many link between a file and the items it holds, within
-- one library: a multi-episode file names more than one item, and an
-- item with a second encoding holds more than one file. All three
-- columns are the primary key, so the row carries nothing to update
-- and a repeat write is a no-op.
--
-- A join from the item column to an item id must match the library as
-- well, because an id names one row only inside its own library.
CREATE TABLE file_items (
    library TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    item TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (library, path, item)
);

-- The reverse lookup, from one library's item to its files. It leads
-- with the library, as every index in this schema does.
CREATE INDEX file_items_library_item ON file_items (library, item);

-- An alias maps one of an item's names to the item, inside one
-- library: every provider id the sidecar carries, and the folder-name
-- key. So several names resolve to one work, and a lost sidecar still
-- resolves the folder. The library and the alias together are the
-- primary key, and source records how the name was learned.
--
-- A join from the item column to an item id must match the library as
-- well, because an id names one row only inside its own library.
CREATE TABLE aliases (
    library TEXT NOT NULL DEFAULT '',
    alias TEXT NOT NULL DEFAULT '',
    item TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (library, alias)
);

-- The reverse lookup, from one library's item to the names that
-- resolve to it. It leads with the library, as every index in this
-- schema does.
CREATE INDEX aliases_library_item ON aliases (library, item);

-- One row per library and worker, holding the Job that ran, when
-- it started and finished, and what it left behind; a Job writes it last
-- and waits for the reporter to echo it.
CREATE TABLE runs (
    library TEXT NOT NULL DEFAULT '',
    worker TEXT NOT NULL DEFAULT '',
    job TEXT NOT NULL DEFAULT '',
    started INTEGER NOT NULL DEFAULT 0,
    finished INTEGER NOT NULL DEFAULT 0,
    unidentified INTEGER NOT NULL DEFAULT 0,
    removed INTEGER NOT NULL DEFAULT 0,
    commit_id TEXT NOT NULL DEFAULT '',
    failure TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (library, worker)
);

-- One row per library, item, and fact: the last attempt an enricher
-- made and how it ended. The scanner lifts these from the ledger files
-- in each folder's .liken/ directory, so a gap query can exclude an
-- item that was tried, and a lost catalog gets them back from the
-- volume. For a file fact the item column holds the file's path. The
-- fact's column is named concern: Corrosion refuses to remove a column,
-- and this one is part of the primary key, which it refuses to add to a
-- table that exists. attempts.go maps the two names.
--
-- provider names the provider block that answered, as in "tmdb", or the
-- blocks joined by commas where a set fact took the union of several. It
-- is empty for a fact that asks no provider, such as probe.
CREATE TABLE attempts (
    library TEXT NOT NULL DEFAULT '',
    item TEXT NOT NULL DEFAULT '',
    concern TEXT NOT NULL DEFAULT '',
    at INTEGER NOT NULL DEFAULT 0,
    result TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (library, item, concern)
);

-- One row per person a library's .contributors/ store holds. The path is the
-- person's own directory, relative to the library root, which is the key
-- credits.yaml names them by, so the join is one column against one column.
-- born and died are the dates the contributor.ids fact wrote. biography and
-- headshot are 1 where the file is beside the entry, and the gap query of each
-- of those two facts reads its own column. Each library holds its own copy of
-- a person, because two libraries may be separate volumes, and the ids below
-- are what join the two copies.
CREATE TABLE contributors (
    library TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    born TEXT NOT NULL DEFAULT '',
    died TEXT NOT NULL DEFAULT '',
    biography INTEGER NOT NULL DEFAULT 0,
    headshot INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (library, path)
);

-- The one order a media browser draws a library's people in.
CREATE INDEX contributors_library_name ON contributors (library, name);

-- One id of one person, in the shape the item aliases take: the scheme and the
-- id name the person inside one library, and the path resolves to the row. One
-- person joins across libraries by an id two of them share, which is the one
-- thing a name cannot do.
CREATE TABLE contributor_aliases (
    library TEXT NOT NULL DEFAULT '',
    scheme TEXT NOT NULL DEFAULT '',
    id TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (library, scheme, id)
);

-- The reverse lookup, from one library's person to the ids that resolve to
-- them. The contributor.ids gap query reads it.
CREATE INDEX contributor_aliases_library_path ON contributor_aliases (library, path);

-- A person joins across libraries by a shared id, with no library known
-- before the lookup.
CREATE INDEX contributor_aliases_scheme_id ON contributor_aliases (scheme, id);

-- One credited person on one title, lifted from the title's own credits.yaml.
-- billing is the billing order, and it is the key beside the item because a
-- title gives each of its people one slot; the column is not named order,
-- which is a word SQL keeps for itself. contributor is the person's directory,
-- empty for a person the store has no entry for.
-- part is actor, director, or writer, and tells the cast of a title from its
-- crew. role is the character an actor played, and the crew carry none. The
-- crew hold the billing after the cast, because the billing is the key.
CREATE TABLE credits (
    library TEXT NOT NULL DEFAULT '',
    item TEXT NOT NULL DEFAULT '',
    billing INTEGER NOT NULL DEFAULT 0,
    contributor TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    part TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (library, item, billing)
);

-- The read a person's page makes: every title in this library that credits one
-- person.
CREATE INDEX credits_library_contributor ON credits (library, contributor);

-- The people pool counts distinct titles before reading each person's name.
-- These columns cover both the grouping and the distinct title count.
CREATE INDEX credits_library_contributor_item ON credits (library, contributor, item);

-- One genre of one title, lifted from the sidecar in its order. The rank is
-- the key beside the item because the first genre is the title's main genre,
-- and a genre strip puts the titles that lead with it first. This is a table
-- and not an array column because Corrosion indexes nothing inside JSON. The
-- walk writes it, and the prune sweeps it the way it sweeps credits.
CREATE TABLE genres (
    library TEXT NOT NULL DEFAULT '',
    item TEXT NOT NULL DEFAULT '',
    rank INTEGER NOT NULL DEFAULT 0,
    genre TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (library, item, rank)
);

-- The two reads the home page makes: the titles of one genre as one range,
-- and every genre with its count as one grouped read.
CREATE INDEX genres_library_genre ON genres (library, genre);

-- The franchises item table: the item header, with the franchise's
-- clock in the body. A franchise is one story across films and series
-- in story order, read from a franchise.yaml in a git repository that a
-- Library of kind franchises names, so a row's library is that Library
-- and never the library of any member. The id is
-- franchise:name:<slug of the directory name>, because the file carries
-- no provider id. path is the directory inside the repository. released
-- is empty, because the scanner reads no member's date, and a wall of
-- franchises sorts on sort_key. art and arts are the files beside the
-- yaml under Kodi's names, and the enrichers fill art from the first
-- member where the directory holds none.
--
-- body holds what the page draws around the wall, as JSON with the
-- file's own names: universe (the home universe), calendar (unit, zero,
-- before, after), eras (name, from, to), and sources. Eras are JSON and
-- not a table because the page reads every era of one franchise at
-- once and never one era across franchises.
CREATE TABLE franchises (
    library TEXT NOT NULL DEFAULT '',
    id TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    sort_key TEXT NOT NULL DEFAULT '',
    released TEXT NOT NULL DEFAULT '',
    added INTEGER NOT NULL DEFAULT 0,
    art TEXT NOT NULL DEFAULT '',
    arts TEXT NOT NULL DEFAULT '[]',
    duration INTEGER NOT NULL DEFAULT 0,
    body TEXT NOT NULL DEFAULT '{}',
    slug TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (library, id)
);

-- The one order a wall of franchises draws.
CREATE INDEX franchises_library_sort_key ON franchises (library, sort_key);

-- One entry of one franchise's order: a film, or one run of a series.
-- position is the entry's place in story order, first to last, and it
-- is the key beside the franchise because the order is the whole point
-- of the row. A series that the story cuts into runs is several rows
-- with the same alias at different positions.
--
--   kind       movie or series
--   alias      the member as the provider alias the member's own
--              library writes: movie:tmdb:1893 or series:tvdb:83268.
--              The join to the catalog is this column against
--              aliases.alias, across every library of the namespace.
--   title      the name from the file, drawn only when no library
--              holds the member
--   released   the real-world date the file gives, as much of it as
--              the author knows: 1999, 1999-05, or 1999-05-19, and
--              empty where the file gives none. On a film it is the
--              first public release, and on a series the day the
--              first episode aired.
--   release_year
--              the year of that date, or 0 where the file gives
--              none. It is derived from released, so the wall reads
--              one integer for its year label and never parses a
--              string. A gap whose release_year is after this year
--              draws as coming, and any other gap draws as missing.
--   timed      1 where the file gives the entry a time, else 0
--   time_from  the entry's span in the calendar's unit; both 0 when
--   time_to    timed is 0
--   universes  the universes the entry belongs to, as a JSON list of
--              names, and [] where it is in the home universe alone.
--              This is JSON and not a table because the page reads
--              every universe of one wall at once.
--
-- A series run's seasons and episodes are the franchise_runs rows
-- below, and a series row with no runs is the whole show.
CREATE TABLE franchise_members (
    library TEXT NOT NULL DEFAULT '',
    franchise TEXT NOT NULL DEFAULT '',
    position INTEGER NOT NULL DEFAULT 0,
    kind TEXT NOT NULL DEFAULT '',
    alias TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    released TEXT NOT NULL DEFAULT '',
    release_year INTEGER NOT NULL DEFAULT 0,
    timed INTEGER NOT NULL DEFAULT 0,
    time_from REAL NOT NULL DEFAULT 0,
    time_to REAL NOT NULL DEFAULT 0,
    universes TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (library, franchise, position)
);

-- The read a film's or a series' page makes to find its franchises:
-- the member rows whose alias is one of the item's own aliases.
CREATE INDEX franchise_members_library_alias ON franchise_members (library, alias);

-- One season, or one episode, of one series run. A row with episode 0
-- is the whole season. An episodes range in the file, S03E03-S03E22,
-- is one row per episode here, because the file expands with no
-- catalog and a held-episode count is then one join. A run with no rows
-- is the whole show, and it counts every episode the catalog holds, so
-- an airing show fills in with no edit to the file.
CREATE TABLE franchise_runs (
    library TEXT NOT NULL DEFAULT '',
    franchise TEXT NOT NULL DEFAULT '',
    position INTEGER NOT NULL DEFAULT 0,
    season INTEGER NOT NULL DEFAULT 0,
    episode INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (library, franchise, position, season, episode)
);

-- The franchise join is the first read that resolves an alias across
-- every library of the namespace, with no library in hand, so the
-- alias needs an index of its own beside the primary key.
CREATE INDEX aliases_alias ON aliases (alias);

-- The two reads a media browser makes of a franchise. Both are one
-- statement, and they differ in one join.
--
-- The strip on a film's or a series' page: the whole franchise in
-- story order, with only the members some library holds, so the strip
-- draws what a person can play. The item's own aliases find the
-- franchises, and an INNER JOIN through aliases to the item tables
-- keeps the held members. Where two libraries hold one member, the
-- read keeps the first library by name, which is the undecided rule
-- from plan 31 made deterministic.
--
--   WITH mine AS (
--     SELECT alias FROM aliases WHERE library = ?1 AND item = ?2
--   ), found AS (
--     SELECT DISTINCT library, franchise FROM franchise_members
--     WHERE alias IN (SELECT alias FROM mine)
--   ), items AS (
--     SELECT library, id, title, art, released, slug, 'movies' AS kind FROM movies
--     UNION ALL
--     SELECT library, id, title, art, released, slug, 'series' AS kind FROM series
--   )
--   SELECT m.library, m.franchise, m.position, m.kind, m.alias, m.title,
--          m.released, m.release_year, m.timed, m.time_from, m.time_to,
--          m.universes,
--          MIN(i.library) AS held_library, i.id, i.title, i.art, i.released, i.slug
--   FROM franchise_members m
--   JOIN found f ON f.library = m.library AND f.franchise = m.franchise
--   JOIN aliases a ON a.alias = m.alias
--   JOIN items i ON i.library = a.library AND i.id = a.item
--   GROUP BY m.library, m.franchise, m.position
--   ORDER BY m.library, m.franchise, m.position;
--
-- The wall on the franchise's own page: every entry in story order,
-- held or not, so a gap draws with the file's title as missing or as
-- coming. The same statement with LEFT JOIN on aliases and items, found
-- replaced by the one franchise, and one more column: the episodes the
-- catalog holds for a series run, which is the count over episodes
-- joined through the same alias and filtered by franchise_runs, or
-- every episode of the show where the run has no rows.
--
--   SELECT m.position, ..., MIN(i.library) AS held_library, i.id, ...,
--          (SELECT count(*) FROM episodes e
--           JOIN aliases sa ON sa.library = e.library AND sa.item = e.series
--           WHERE sa.alias = m.alias
--             AND (NOT EXISTS (SELECT 1 FROM franchise_runs r
--                              WHERE r.library = m.library AND r.franchise = m.franchise
--                                AND r.position = m.position)
--                  OR EXISTS (SELECT 1 FROM franchise_runs r
--                             WHERE r.library = m.library AND r.franchise = m.franchise
--                               AND r.position = m.position AND r.season = e.season
--                               AND r.episode IN (0, e.episode)))) AS held_episodes
--   FROM franchise_members m
--   LEFT JOIN aliases a ON a.alias = m.alias
--   LEFT JOIN items i ON i.library = a.library AND i.id = a.item
--   WHERE m.library = ?1 AND m.franchise = ?2
--   GROUP BY m.position
--   ORDER BY m.position;
