---
title: Widget
weight: 10
toc: true
---

<!-- Generated from testdata/sample-crd.yaml by docs/crdref. Do not edit. -->

A Widget page can open with hand-written prose, with a
[link](/docs/guides/install/) the schema itself cannot hold.

A Widget describes one widget.

## spec

What the widget should be.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--name"></span>`name` | string | yes | The widget's name. Pattern: `^[a-z]+$`. |
| <span id="spec--mode"></span>`mode` | string | no | How the widget behaves. Folded across lines. One of: `simple`, `fancy`. |
| <span id="spec--replicas"></span>`replicas` | integer | no | How many copies run. Default: `1`. |
| <span id="spec--tags"></span>`tags` | []string | no | Labels \| with a pipe. |
| <span id="spec--parts"></span>`parts` | [\[\]object](#specparts) | no | The widget's parts. |
| <span id="spec--mirrors"></span>`mirrors` | map[string][]string | no | Endpoint lists by host. |
| <span id="spec--shape"></span>`shape` | [object](#specshape) | no | The widget's shape. |

### spec.parts[]

The widget's parts.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specparts--id"></span>`id` | string | yes | The part's identity. |
| <span id="specparts--size"></span>`size` | integer | no | The part's size. |

### spec.shape

The widget's shape.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specshape--kind"></span>`kind` | string | no | The shape's kind. |

## status

What the widget reports.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--phase"></span>`phase` | string | no | One word for the state. One of: `Ready`, `Broken`. |
