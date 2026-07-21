---
title: Widget
weight: 10
toc: true
---

<!-- Generated from testdata/sample-crd.yaml by docs/crdref. Do not edit. -->

A Widget describes one widget.

## spec

What the widget should be.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | The widget's name. Pattern: `^[a-z]+$`. |
| `mode` | string | no | How the widget behaves. Folded across lines. One of: `simple`, `fancy`. |
| `replicas` | integer | no | How many copies run. Default: `1`. |
| `tags` | []string | no | Labels \| with a pipe. |
| `parts` | [\[\]object](#specparts) | no | The widget's parts. |
| `mirrors` | map[string][]string | no | Endpoint lists by host. |
| `shape` | [object](#specshape) | no | The widget's shape. |

### spec.parts[]

The widget's parts.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string | yes | The part's identity. |
| `size` | integer | no | The part's size. |

### spec.shape

The widget's shape.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `kind` | string | no | The shape's kind. |

## status

What the widget reports.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `phase` | string | no | One word for the state. One of: `Ready`, `Broken`. |
