---
title: Connect Radarr, Sonarr, and Jellyfin
weight: 70
description: "Connect Radarr, Sonarr, and Jellyfin to a Library's webhook so an import rescans one folder at once. Use when new titles should appear on the wall in seconds instead of on the hourly walk, or to rescan one folder by hand."
---

# Connect Radarr, Sonarr, and Jellyfin

A full walk runs on the `Library`'s schedule, once an hour by
default. A webhook rescans one folder as soon as the `Library` has no
other `Job` running, so a new title reaches the wall without waiting
for the next walk.

## The address

Every existing `Library` reports its webhook address:

    $ kubectl -n media get library movies -o jsonpath='{.status.webhook}'
    http://library-operator.liken-system.svc/webhook/media/movies

The address names the operator's own `Service` and the `Library`, so it
holds for as long as the `Library` exists. The `Service` is a
`ClusterIP`, reachable only inside the cluster, and the endpoint checks
nothing but the method and the path. Give it only to tools that run in
the same cluster.

## What a POST does

The operator reads the body for the path of what changed, in this
order: `movieFile.path`, `episodeFile.path`, `movie.folderPath`,
`movie.path`, `series.path`, then a top-level `Path`. The first one
found names the folder to rescan. A body with none of them, or no
body at all, schedules a full walk. Every POST answers `204 No
Content`.

The operator holds the folder and starts one walk `Job` for it on its
next pass. That `Job` walks the folder and runs every phase the
`Library`'s sources serve over it, so the title gets its id, its `.nfo`
file, its art, and its tiles in the same `Job`, as each phase before
them finishes the title.

A `Library` runs one `Job` at a time. A webhook that arrives while a
`Job` runs waits for it, and every folder the webhooks named in the
meantime goes into the next `Job`. So five imports during one run start
one `Job`, not five. Trickplay and the trailer files each stop starting
new titles fifteen minutes into a run, so a `Job` that works through a
backlog of them ends within about fifteen minutes and one title, and the
folders that waited go next. Past 64 distinct folders held for one
`Library`, the whole set collapses to a full walk.

## Radarr

Add a connection of type Webhook. Set the URL to the movies
`Library`'s address, and the method to POST. Enable the events that
name a file: import, upgrade, and rename. An event with no path, such
as the test, schedules a full walk. Saving a new connection runs the
test on its own, and the Test button runs it again, so a new
connection costs one full walk for each. Make the connection once and
let the walks finish before you test it by hand.

## Sonarr

The same, with the series `Library`'s address.

## Jellyfin

Install Jellyfin's Webhook plugin and add a generic destination with
the `Library`'s address. An item-added notification has a
top-level `Path`, which is what the operator reads.

## One writer beside the media

Radarr and Sonarr write beside the media too. This operator reads the
`uniqueid` in the Kodi `.nfo` they write, so keep that metadata
option on. Turn off their image writes and their import of extra
files, so that the art and subtitle facts are the only writers of
those. The [Jellyfin guide](/docs/guides/jellyfin/#5-share-the-volume-with-jellyfin)
lists the Jellyfin switches.

## By hand

    curl -X POST http://library-operator.liken-system.svc/webhook/media/movies

A body naming a folder narrows the walk to that folder:

    curl -X POST http://library-operator.liken-system.svc/webhook/media/movies \
      -H 'Content-Type: application/json' \
      -d '{"movie":{"folderPath":"/media/movies/Example Movie (2019)"}}'
