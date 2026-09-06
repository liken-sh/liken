---
title: Watches
weight: 40
toc: true
---

<!-- Generated from deploy/watches-crd.yaml by crdref. Do not edit. -->

A `Watch` is a set of people on one item: three people on a series
together, or one person alone. Progress belongs to the set, so two
`Watch`es on the same series with different people are two records,
and both are right. The people are `Person` names from
`people.liken.sh`, and the item is a movie or a series in a `Library`
of the same namespace.

    apiVersion: library.liken.sh/v1alpha1
    kind: Watch
    metadata:
      name: the-office-together
      namespace: media
    spec:
      people: [chris, thora, io]
      item:
        library: series
        slug: the-office

Write one by hand, or let the browser create one when it asks who is
watching. The operator puts an owner reference on the `Watch` for
each `Person` it names, so the `Watch` goes when the last of them
goes. The status is the operator's: it says which `Play` was recorded
last against this `Watch` and where that `Play` reached, and it is
rewritten as the namespace's progress store records rows. What comes
next in the series is the browser's to work out from its catalog.

A set of people on one item, and where that set reached. Create one for each set that watches together, in the namespace of the `Library` that holds the item.

## spec

The people who share this record, and the item they watch.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--people"></span>`people` | []string | yes | The people who share this Watch, as `Person` names. |
| <span id="spec--item"></span>`item` | [object](#specitem) | yes | The item this Watch is on, as the catalog names it. |

### spec.item

The item this Watch is on, as the catalog names it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitem--library"></span>`library` | string | yes | The `Library` in this namespace that holds the item. Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`. |
| <span id="specitem--slug"></span>`slug` | string | yes | The catalog's slug for the item: a movie's, or a series'. |

## status

Where this set of people reached, written only by the library operator. It is a projection of the progress store: the store publishes each row it writes, and the operator writes the projection here.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--play"></span>`play` | string | no | The last `Play` recorded against this Watch. |
| <span id="status--item"></span>`item` | integer | no | Which of that Play's items was playing, counting from 1. |
| <span id="status--position"></span>`position` | string | no | The playhead inside that item, as H:MM:SS. |
| <span id="status--duration"></span>`duration` | string | no | The length of that item, as H:MM:SS. |
| <span id="status--season"></span>`season` | integer | no | The season and episode numbers of that item, or 0 for a work that has none. |
| <span id="status--episode"></span>`episode` | integer | no | The season and episode numbers of that item, or 0 for a work that has none. |
| <span id="status--ended"></span>`ended` | boolean | no | True once that Play ended. |
| <span id="status--lastrecorded"></span>`lastRecorded` | string | no | When the store last wrote a row for this Watch. |
