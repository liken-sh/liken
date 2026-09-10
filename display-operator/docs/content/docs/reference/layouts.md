---
title: Layouts
weight: 30
toc: true
---

<!-- Generated from deploy/layouts.yaml by crdref. Do not edit. -->

A `Layout` divides a screen into regions. Each region is a rectangle
in fractions of the screen and a label selector, and it shows the
window of the first pod whose labels match. A `Display` names the
`Layout` it shows in `spec.layout`, and a `Display` that names none
shows every window fullscreen with the newest on top. The
[guide](/docs/guides/layout/) walks through a two-region screen with
pods from two namespaces.

The order of `spec.regions` is the stacking order, last on top. The
pods never learn where they are drawn: the `Layout` is the only place
the arrangement lives, so a moved region moves every screen that
names the `Layout` and changes no pod.

The regions of a screen, each a rectangle in fractions of the screen and a label selector that picks the pod whose window it shows. A Display names a Layout in spec.layout, and one Layout serves any number of screens.

## spec

The regions, in stacking order.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--regions"></span>`regions` | [\[\]object](#specregions) | yes | The regions of the screen, in stacking order: a region written after another draws over it where the two overlap. Each region shows one window, the first to arrive from a pod whose labels match the selector, and reports the surface it shows on the Display's status.layout. |

### spec.regions[]

The regions of the screen, in stacking order: a region written after another draws over it where the two overlap. Each region shows one window, the first to arrive from a pod whose labels match the selector, and reports the surface it shows on the Display's status.layout.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specregions--name"></span>`name` | string | yes | The region's name, unique in this Layout. The Display's status.layout reports it beside the surface the region shows, and status.surfaces names it on the surface. Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`. |
| <span id="specregions--rect"></span>`rect` | [object](#specregionsrect) | yes | The region's rectangle, as four fractions of the screen, so one Layout fits a 1080p panel and a 4K one. The compositor tells the window its rectangle's size in pixels, and the program redraws at that size. |
| <span id="specregions--selector"></span>`selector` | [object](#specregionsselector) | yes | Which pod's window the region shows, as a label selector over the pods that hold a claim on this screen, the way a Service selects pods. Any label counts. The candidates are only the pods with a claim on the screen, so a matching label elsewhere in the cluster matches nothing here. A window that arrived on the shared socket holds no claim and matches no selector. |
| <span id="specregions--transition"></span>`transition` | [object](#specregionstransition) | no | How a window enters the region. The compositor draws the entrance, because the program never knows where it is. The compositor cannot draw an exit: a program that ends takes its window with it, so a program that wants a soft exit fades its own picture before it ends. Absent, a window appears at once. |

#### spec.regions[].rect

The region's rectangle, as four fractions of the screen, so one Layout fits a 1080p panel and a 4K one. The compositor tells the window its rectangle's size in pixels, and the program redraws at that size.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specregionsrect--left"></span>`left` | number | yes | The left edge, as a fraction of the screen's width. 0 is the left edge of the screen. |
| <span id="specregionsrect--top"></span>`top` | number | yes | The top edge, as a fraction of the screen's height. 0 is the top of the screen. |
| <span id="specregionsrect--width"></span>`width` | number | yes | The width, as a fraction of the screen's width. left plus width is at most 1. |
| <span id="specregionsrect--height"></span>`height` | number | yes | The height, as a fraction of the screen's height. top plus height is at most 1. |

#### spec.regions[].selector

Which pod's window the region shows, as a label selector over the pods that hold a claim on this screen, the way a Service selects pods. Any label counts. The candidates are only the pods with a claim on the screen, so a matching label elsewhere in the cluster matches nothing here. A window that arrived on the shared socket holds no claim and matches no selector.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specregionsselector--matchlabels"></span>`matchLabels` | map[string]string | no | Labels the pod carries with exactly these values. Every entry must match. |
| <span id="specregionsselector--matchexpressions"></span>`matchExpressions` | [\[\]object](#specregionsselectormatchexpressions) | no | Requirements on the pod's labels. Every requirement must hold, and they combine with matchLabels. |

#### spec.regions[].selector.matchExpressions[]

Requirements on the pod's labels. Every requirement must hold, and they combine with matchLabels.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specregionsselectormatchexpressions--key"></span>`key` | string | yes | The label key the requirement reads. |
| <span id="specregionsselectormatchexpressions--operator"></span>`operator` | string | yes | How the key is judged: In and NotIn compare its value against values, and Exists and DoesNotExist ask only whether the key is present. One of: `In`, `NotIn`, `Exists`, `DoesNotExist`. |
| <span id="specregionsselectormatchexpressions--values"></span>`values` | []string | no | The values In and NotIn compare against. Exists and DoesNotExist take none. |

#### spec.regions[].transition

How a window enters the region. The compositor draws the entrance, because the program never knows where it is. The compositor cannot draw an exit: a program that ends takes its window with it, so a program that wants a soft exit fades its own picture before it ends. Absent, a window appears at once.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specregionstransition--kind"></span>`kind` | string | yes | none shows the window at once. fade brings it from transparent to opaque over milliseconds. One of: `none`, `fade`. |
| <span id="specregionstransition--milliseconds"></span>`milliseconds` | integer | no | How long the transition runs. 0 is the same as none. |
