---
title: Guides
weight: 10
---

# Guides

The guides run in the order a cluster takes them: the
[install](/docs/guides/install/), a [library](/docs/guides/libraries/)
over a volume, what a [scan](/docs/guides/scanning/) reads, the
[catalog](/docs/guides/catalog/) it writes, the
[browser](/docs/guides/browser/) on a screen,
[enrichment](/docs/guides/enrichment/) from the providers, the
[webhooks](/docs/guides/webhooks/) that rescan an import,
[franchises](/docs/guides/franchises/) as a library of their own, and
what to expect on a [small machine](/docs/guides/small-machines/).

## How the pieces fit

Three resources, all namespaced, are the whole API.

A `MetadataProvider` is one account with one provider, declared once.
A `Catalog` is declared once per namespace. It stands the catalog pod,
the standing member of the namespace's replicated SQLite catalog, and
it sizes every catalog claim. A `Library` is declared once per root
directory on a volume, and it names the kind of media there.

The operator reconciles a `Library` into a `CronJob`. Each of its
`Jobs` walks the volume with a catalog agent beside it, writes rows
into the namespace's catalog, and exits when the catalog pod echoes
its run back. A webhook from Radarr, Sonarr, or Jellyfin runs the same
walk over one folder. An enrich `Job` asks the providers a `Library`
names and writes the answers beside the media, as the sidecars and
art Kodi and Jellyfin read. The volume stays the source of truth, and
the catalog is derived from it.

A screen is a `Player` that `media-operator` owns. When its idle
controller names this operator, the operator stands a pod on the
`Player`'s display with the same catalog agent and the media browser.
The browser reads its own copy of the catalog, takes the remote's
presses over the bus, and publishes what a person chose. The operator
turns that into a `Play`.

A namespace is the boundary. Every `Library` in it writes one catalog,
and every screen in it shows that catalog.
