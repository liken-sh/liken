# 94, Letterboxd

This plan, written on 2026-11-10, records an investigation for later:
what `library-operator` can exchange with Letterboxd, the service
where a person keeps a diary of the films they watched, their ratings,
and a watchlist.

Status on 2026-11-10: a stub, not designed. Nothing is built, and
nothing below is verified against Letterboxd's current documentation.
The investigation answers the questions first, and a design follows
only if the answers allow one.

## What a person gains

A person who keeps a Letterboxd diary has a record of films that is
older and wider than the catalog's progress store, because it holds
films they watched in a cinema or on another service. Three exchanges
are possible, and each is useful without the others:

- **Letterboxd to the catalog.** A film in the person's diary counts
  as seen on the screens, with the date of the diary entry. A rating
  could show on the film's page.
- **The catalog to Letterboxd.** A film that a `Player` plays to its
  end becomes a diary entry, so the person does not log it by hand.
- **The watchlist.** The films of the person's watchlist that the
  catalog holds become a row of the browser, and the ones it does not
  hold become a list that another tool can read.

## What `liken` has today

The progress store holds each `Person`'s plays, and
[plan 86](completed/86-who-has-seen-a-film.md) shows on a film's page
who has seen it. `spec.jellyfin` on the `Catalog` copies progress in
both directions with a Jellyfin server, and its backfill is the
nearest existing design to an import of a diary. A catalog film has a
TMDB id, which [plan 85](completed/85-a-tmdb-id-from-the-ids-a-title-has.md)
derives when the `.nfo` file names another id.

## The questions

1. **Which interface can a self-hosted operator use?** Letterboxd has
   an API that it grants by application. The investigation finds the
   terms: whether a personal, self-hosted, open-source client
   qualifies, whether each person who installs `liken` needs their
   own key, and whether the API can write a diary entry. If the API
   is closed to this use, the fallbacks read only: the RSS feed of a
   member's public diary, and the CSV files of an account export.
2. **How does a Letterboxd film match a catalog film?** The
   investigation finds which interfaces give a TMDB id for each film,
   and what the match does for a film with no id.
3. **Which `Person` is which member?** Jellyfin's mapping of a user
   to a `Person` is the model. The investigation states where the
   member name and any credential are declared, and where a secret is
   stored.
4. **What does a diary entry mean to the progress store?** A play in
   the store has a position and a time, and a diary entry has a date,
   a rating, and a rewatch mark. The investigation states how an
   entry for a film that the catalog does not hold is kept, so that
   the film counts as seen when it arrives later.
5. **How does a change arrive?** The root `AGENTS.md` permits a timer
   only as a clock. If Letterboxd offers no subscription, the plan
   must state the interval of a feed read and the reason for it, or
   make the import an action that a person starts.
6. **What stops a loop?** With writes in both directions and Jellyfin
   in the same store, one play could become a diary entry, return as
   an import, and be written again. Jellyfin's sync has the same
   hazard, and its answer is the starting point.
7. **Series.** Letterboxd is a service for films. The investigation
   states what it holds for a series or a miniseries, and whether the
   integration ignores them.

## What the investigation delivers

A short report against each question, with the pages of Letterboxd's
documentation and terms that it read, and one of three results: a
design for an exchange in both directions, a design for an import
alone, or a reason to build nothing.
