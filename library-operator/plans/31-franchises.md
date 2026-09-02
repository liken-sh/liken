# Franchises

Plan 31. The third enrichment build from [plan 27](27-enrichment.md).
A franchise is the films and series of one universe in story order, a
home of its own on the screen, and a file a person writes on the
volume.

## The problem

A set is what a movie sidecar names, one collection per film in
release order, and plan 22 draws it as a strip on the film's page. A
franchise is bigger and crosses kinds: the Marvel films with the
series that run between them, or Alien with its prequels in story
order. No sidecar carries it, the order is a person's opinion, and the
members are in different libraries on different volumes. So no member
library can hold it, and the scanner cannot derive it.

## The contract

- **A library kind.** A `Library` of kind `franchises` names a folder
  with one directory per franchise. Each holds `franchise.yaml` and
  the franchise's art under Kodi's names. Its scanner is small: it
  reads the files and writes a `franchises` table and a
  `franchise_members` table, keyed by library like every other table.
- **The file**, in story order and nothing else, because release order
  comes from the members' own dates:

  ```yaml
  name: Marvel Cinematic Universe
  order:
    - movie: tmdb:1771
    - movie: tmdb:1726
    - series: tvdb:263365
      seasons: [1]
    - episodes: tvdb:263365
      from: S02E01
      to: S02E19
    - movie: tmdb:100402
  ```

- **The join.** A member is a provider id, and the screen resolves it
  through the `aliases` table to a row in whichever library of the
  namespace holds it. The franchise scanner never reads another
  library's volume. A member no library holds draws as a gap in the
  order, and it fills when the title arrives anywhere in the namespace.
- **The page**, from plan 22's primitives: a header over the
  franchise's art, then the order as a wall, with a film and a series
  side by side and a season or an episode range as one slot. A toggle
  shows release order, sorted on the members' dates.
- **The way in.** A strip under a film's set strip names the franchise
  the film belongs to. The home page of plan 26 gets a franchises row.
- **Art** comes from the folder, and where the folder holds none, the
  enrichers of plan 30 fill it from the first member's art.

## Proof

On `liken-1`, one franchise across the lab's movies and series
libraries, written by hand into the `Franchises` share. The page draws
the order with its gaps, a film opens from it, back returns to it, and
a member added to a library fills its gap on the next scan.

## What is set aside

A cluster-owner resource for a franchise, which plan 24 proposed. It
puts a truth in the cluster that the volume does not hold, and a
catalog rebuilt from the volume would lose it.

Two orders in one file. Release order is derived, and a second
hand-written order can be a second franchise directory.

## What is not decided

Which row the page shows when two libraries hold one film. Whether a
franchise may name a book or a game, which waits on those kinds.
Whether a franchise may reach another namespace, which the
one-cluster-per-namespace rule says no to today.
