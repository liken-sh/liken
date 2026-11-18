---
title: Layouts
weight: 30
toc: true
---

<!-- Generated from deploy/layouts.yaml by crdref. Do not edit. -->

# `Layout`

A `Layout` divides one screen into regions, so that pods from several
namespaces can share a monitor. Each region has a rectangle, given as
fractions of the screen, and a label selector. The region shows the
windows of the first claim to arrive whose pods' labels match the
selector and that no earlier region shows. A `Display` uses a `Layout`
when `spec.layout` names it. A `Display` with no `spec.layout` shows
every window fullscreen, with the newest on top. The
[guide](/docs/guides/layout/) shows a screen with two regions and
pods from two namespaces.

The order of `spec.regions` is the stacking order, and the last region
is on top. Only the `Layout` holds the arrangement, and the pods do
not know where their windows are drawn. So when you change a region,
every `Display` that uses the `Layout` changes, and no pod changes.

The regions of a screen. Each region is a rectangle, given as fractions of the screen, and a label selector that chooses the claim whose windows the region shows. A `Display` names a `Layout` in `spec.layout`, and any number of screens can use the same `Layout`.

## spec

The regions, in stacking order.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--regions"></span>`regions` | [\[\]object](#specregions) | yes | The screen's regions in stacking order. A region written after another draws over it where they overlap. A region shows the windows of one claim: the first claim to arrive whose pods' labels match the region's `selector`. The newest window of that claim is on top. `status.layout` of the `Display` reports the window on top. |

### spec.regions[]

The screen's regions in stacking order. A region written after another draws over it where they overlap. A region shows the windows of one claim: the first claim to arrive whose pods' labels match the region's `selector`. The newest window of that claim is on top. `status.layout` of the `Display` reports the window on top.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specregions--name"></span>`name` | string | yes | The region's name, unique in this `Layout`. `status.layout` of the `Display` lists it beside the window that the region shows, and `status.surfaces` gives it on that window. Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`. |
| <span id="specregions--rect"></span>`rect` | [object](#specregionsrect) | yes | The region's rectangle, as fractions of the screen, so the same `Layout` fits a 1080p panel and a 4K panel. The compositor tells each window the rectangle's size in pixels, and the program redraws at that size. |
| <span id="specregions--selector"></span>`selector` | [object](#specregionsselector) | yes | A label selector, written like the selector of a `Service`, that chooses the claim whose windows the region shows. It matches against the labels that every pod holding the claim has, with the same value on each pod. Only claims on this screen can match, so a pod with matching labels and no claim on this screen does not appear. A window that a program opened on the compositor's own socket, `wayland-0`, and not on its claim's socket, belongs to no claim and matches no selector. |
| <span id="specregions--transition"></span>`transition` | [object](#specregionstransition) | no | How a window enters and leaves the region. The compositor draws both transitions, because the program is not told its region's position. `enter` and `exit` are both optional, and without one the window enters or leaves at once. |

#### spec.regions[].rect

The region's rectangle, as fractions of the screen, so the same `Layout` fits a 1080p panel and a 4K panel. The compositor tells each window the rectangle's size in pixels, and the program redraws at that size.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specregionsrect--left"></span>`left` | number | yes | The left edge, as a fraction of the screen's width. `0` is the left edge of the screen. |
| <span id="specregionsrect--top"></span>`top` | number | yes | The top edge, as a fraction of the screen's height. `0` is the top of the screen. |
| <span id="specregionsrect--width"></span>`width` | number | yes | The width, as a fraction of the screen's width. `left` plus `width` is at most 1. |
| <span id="specregionsrect--height"></span>`height` | number | yes | The height, as a fraction of the screen's height. `top` plus `height` is at most 1. |

#### spec.regions[].selector

A label selector, written like the selector of a `Service`, that chooses the claim whose windows the region shows. It matches against the labels that every pod holding the claim has, with the same value on each pod. Only claims on this screen can match, so a pod with matching labels and no claim on this screen does not appear. A window that a program opened on the compositor's own socket, `wayland-0`, and not on its claim's socket, belongs to no claim and matches no selector.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specregionsselector--matchlabels"></span>`matchLabels` | map[string]string | no | Labels that the pod must have, with exactly these values. Every entry must match. |
| <span id="specregionsselector--matchexpressions"></span>`matchExpressions` | [\[\]object](#specregionsselectormatchexpressions) | no | Requirements on the pod's labels. Every requirement must hold, together with `matchLabels`. |

#### spec.regions[].selector.matchExpressions[]

Requirements on the pod's labels. Every requirement must hold, together with `matchLabels`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specregionsselectormatchexpressions--key"></span>`key` | string | yes | The label key that the requirement tests. |
| <span id="specregionsselectormatchexpressions--operator"></span>`operator` | string | yes | How the requirement tests `key`. `In` and `NotIn` compare the label's value with `values`, and `Exists` and `DoesNotExist` test only whether the key is present. One of: `In`, `NotIn`, `Exists`, `DoesNotExist`. |
| <span id="specregionsselectormatchexpressions--values"></span>`values` | []string | no | The values that `In` and `NotIn` compare with. `Exists` and `DoesNotExist` take none. |

#### spec.regions[].transition

How a window enters and leaves the region. The compositor draws both transitions, because the program is not told its region's position. `enter` and `exit` are both optional, and without one the window enters or leaves at once.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specregionstransition--enter"></span>`enter` | [object](#specregionstransitionenter) | no | How a window enters the region. It runs when a window arrives in the region, whether the pod is new or its labels began to match. |
| <span id="specregionstransition--exit"></span>`exit` | [object](#specregionstransitionexit) | no | How a window leaves the region. It runs when a window that is still drawing stops matching the region, for example after a controller removes a label. When a program ends or closes its window, the window leaves at once, whatever this field sets. |

#### spec.regions[].transition.enter

How a window enters the region. It runs when a window arrives in the region, whether the pod is new or its labels began to match.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specregionstransitionenter--kind"></span>`kind` | string | yes | `none` shows the window at once. `fade` takes it from transparent to opaque over `milliseconds`. One of: `none`, `fade`. |
| <span id="specregionstransitionenter--milliseconds"></span>`milliseconds` | integer | no | How long the entrance runs, in milliseconds. `0` is the same as `none`. |

#### spec.regions[].transition.exit

How a window leaves the region. It runs when a window that is still drawing stops matching the region, for example after a controller removes a label. When a program ends or closes its window, the window leaves at once, whatever this field sets.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specregionstransitionexit--kind"></span>`kind` | string | yes | `none` removes the window at once. `fade` takes it from opaque to transparent over `milliseconds`, and the program keeps drawing until the fade ends. One of: `none`, `fade`. |
| <span id="specregionstransitionexit--milliseconds"></span>`milliseconds` | integer | no | How long the exit runs, in milliseconds. `0` is the same as `none`. |
