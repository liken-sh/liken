-- The progress store's schema, in one file that every agent of the
-- progress cluster loads: from the image at
-- /etc/corrosion/progress-schema, and from this directory in a local
-- harness. Corrosion applies the difference on every start. It adds
-- tables, columns, and indexes, and it refuses to drop any. Its
-- cr-sqlite layer imposes the rest: only CREATE TABLE and CREATE INDEX,
-- a default on every non-null column, no unique index beyond the
-- primary key, and a primary key on every table.
--
-- The store records what each Play reached, and it holds no catalog id.
-- A work is named by its aliases and a person by their Person name, so
-- a reader joins progress to the catalog at read time, alias to alias,
-- and the store never reads the catalog.

-- One row per Play. The Play's name is the whole key, because the store
-- is per namespace and a name is unique inside one.
--
-- The times are Unix seconds: started is the first write of the row,
-- recorded is the last, and ended is 0 while the Play still runs. The
-- position and the duration are seconds, so a reader sorts and
-- subtracts them without parsing.
--
-- player is the Player the Play ran on, so a Play that names no person
-- still has a row a screen can show. season and episode are 0 for a
-- work that has neither.
-- watch is empty on every row. No writer fills it and no reader takes
-- it, and it stays because Corrosion cannot drop a column.
CREATE TABLE plays (
    play TEXT NOT NULL DEFAULT '',
    player TEXT NOT NULL DEFAULT '',
    library TEXT NOT NULL DEFAULT '',
    watch TEXT NOT NULL DEFAULT '',
    started INTEGER NOT NULL DEFAULT 0,
    ended INTEGER NOT NULL DEFAULT 0,
    item INTEGER NOT NULL DEFAULT 0,
    position INTEGER NOT NULL DEFAULT 0,
    duration INTEGER NOT NULL DEFAULT 0,
    phase TEXT NOT NULL DEFAULT '',
    season INTEGER NOT NULL DEFAULT 0,
    episode INTEGER NOT NULL DEFAULT 0,
    recorded INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (play)
);

-- One row per person in one Play. The audience of a Play is a query
-- over this table and never a column, because a household watches in
-- subsets and a set of people is what a record belongs to.
CREATE TABLE play_people (
    play TEXT NOT NULL DEFAULT '',
    person TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (play, person)
);

-- One row per provider id on one Play. The aliases are the work's
-- identity: a 4K upgrade or a rename by the organizer is a new file and
-- the same position.
CREATE TABLE play_aliases (
    play TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    id TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (play, provider)
);

-- "Continue watching for chris" reads every Play one person was in, so
-- the person leads this index while the primary key leads with the Play.
CREATE INDEX play_people_person ON play_people (person);

-- No read takes this index. It stays because Corrosion cannot drop one.
CREATE INDEX plays_watch ON plays (watch);

-- One Player's Plays in the order they were recorded, which is a
-- screen's history page read backward.
CREATE INDEX plays_player_recorded ON plays (player, recorded);
