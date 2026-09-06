---
title: Connect Radarr, Sonarr, and Jellyfin
weight: 70
---

# Connect Radarr, Sonarr, and Jellyfin

A full walk runs on the `Library`'s schedule, once an hour by
default. A webhook rescans one folder the moment an import lands, so
a new title is on the wall in seconds.

## The address

Every standing `Library` reports its webhook address:

    $ kubectl -n media get library movies -o jsonpath='{.status.webhook}'
    http://library-operator.liken-system.svc/webhook/media/movies

The address names the operator's own `Service` and the `Library`, so it
holds for the life of the `Library`. The `Service` is a `ClusterIP`,
reachable only inside the cluster, and the endpoint checks nothing but
the method and the path. Give it only to tools that run in the same
cluster.

## What a POST does

The operator reads the body for the path of what changed, in this
order: `movieFile.path`, `episodeFile.path`, `movie.folderPath`,
`movie.path`, `series.path`, then a top-level `Path`. The first one
found names the folder to rescan. A body with none of them, or no
body at all, schedules a full walk. Every POST answers `204 No
Content`.

A rescan is a chain of three `Jobs`: a scan of the folder, an enrich,
and a rescan that reads what the enrich wrote. Past 64 distinct
folders held for one `Library`, the whole set collapses to a full
walk.

## Radarr

Add a connection of type Webhook. Set the URL to the movies
`Library`'s address, and the method to POST. Enable the events that
carry a file: import, upgrade, and rename. An event with no path, such
as the test, schedules a full walk.

## Sonarr

The same, with the series `Library`'s address.

## Jellyfin

Install Jellyfin's Webhook plugin and add a generic destination with
the `Library`'s address. An item-added notification carries a
top-level `Path`, which is what the operator reads.

## By hand

    curl -X POST http://library-operator.liken-system.svc/webhook/media/movies

A body naming a folder narrows the rescan to that folder:

    curl -X POST http://library-operator.liken-system.svc/webhook/media/movies \
      -H 'Content-Type: application/json' \
      -d '{"movie":{"folderPath":"/media/movies/Example Movie (2019)"}}'
