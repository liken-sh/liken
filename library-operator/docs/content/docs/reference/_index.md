---
title: Reference
weight: 20
---

# Reference

Three pages are generated from the resource schemas, so each field's
description is the documentation for that field:

* [Libraries](/docs/reference/libraries/): one directory of media of
  one kind, and what its scan reports.
* [Catalogs](/docs/reference/catalogs/): a namespace's shared catalog,
  and the claims that hold its copies.
* [Metadata providers](/docs/reference/metadataproviders/): one account
  with one provider, and the facts it can supply.

Three pages are written by hand:

* [Franchises](/docs/reference/franchises/): the file that puts films
  and series in story order, and the schema that checks it.
* [The library bus](/docs/reference/bus/): every topic that this
  operator's pods publish and read on `media-operator`'s message bus,
  with the shape of each payload. That includes the play request,
  which a media browser of any make can publish.
* [Metrics](/docs/reference/metrics/): the Prometheus metrics that the
  operator, the media browser, and the catalog's Corrosion agent
  serve, and how to scrape them.
