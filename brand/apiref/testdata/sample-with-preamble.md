---
title: widget-api
weight: 10
toc: true
---

<!-- Generated from testdata/sample-openapi.json by apiref. Do not edit. -->

The routes below are generated from the document the API serves at
`/v1/widget/openapi.json`. The [API page](/docs/reference/api/) tells
what the API is for and how it conducts itself.

`widget-api`, version `v1alpha1`, described in OpenAPI 3.1.1.

Every Widget in this cluster, and the sound one makes.

Identity is in the path, and the format is in the extension. The manual is at https://widget.example/docs/reference/api/.

## GET /v1/widget/widgets

Every Widget this API serves

The list is the whole of it. There is no paging, because a cluster holds tens of Widgets and not thousands.

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | \[\][Widget](#widget) | The list, in name order. |
| 401 | `application/problem+json` | [Problem](#problem) | No token, or a token the TokenReview refused. |

**Link relations**

| Link | Status | Operation | Description |
| --- | --- | --- | --- |
| `widget` | 200 | `getWidget` | One Widget of the list. |

## POST /v1/widget/widgets

Take a new Widget

This route requires `bearer` and `clientCertificate`.

**Request body**

The Widget to take. The name must be free.

The request body is required.

| Media type | Schema |
| --- | --- |
| `application/json` | [Widget](#widget) |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 201 | `application/json` | [Widget](#widget) | The Widget as it was taken. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Location` | 201 | The path of the new Widget (RFC 9110 section 10.2.2). |

## GET /v1/widget/widgets/{name}/sound

The sound the Widget makes

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Widget. |
| `loudness` | query | yes | string | How loud, in steps this route names. One of: `quiet`, `loud`. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | The sound, in the form the extension or the Accept field names. |
| 200 | `audio/wav` | string (binary) | The sound, in the form the extension or the Accept field names. |
| 404 | `application/problem+json` | [Problem](#problem) | No Widget of that name. |

## OPTIONS /v1/widget/widgets/{name}/sound

The methods this route answers

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Widget. |
| `loudness` | query | no | integer | How loud \| how quiet. Default: `50`. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods are in the Allow field. |

## Schemas

### Widget

One Widget: what it is called, what it is made of, and what it answers with.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="widget--name"></span>`name` | string | yes | The name, unique in the cluster. Pattern: `^[a-z0-9-]+$`. |
| <span id="widget--material"></span>`material` | [Material](#material) | yes | What a Widget is made of. One of: `oak`, `steel`. |
| <span id="widget--home"></span>`home` | string or null | no | Where the Widget stands, and null while it stands nowhere. |
| <span id="widget--sizes"></span>`sizes` | map[string]integer | no | The size of each part, keyed by the part's name. |
| <span id="widget--faces"></span>`faces` | \[\][object](#widgetfaces) | no | Each face of the Widget, in the order they were cut. |
| <span id="widget--manual"></span>`manual` | string (uri) | no | Where the Widget's own manual is. |

#### Widget.faces[]

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="widgetfaces--edges"></span>`edges` | integer | yes | How many edges this face has. |
| <span id="widgetfaces--finish"></span>`finish` | string | no | The finish this face carries. One of: `rough`, `smooth`. |

### Material

What a Widget is made of.

The type is string. One of: `oak`, `steel`.

### Problem

The RFC 9457 problem document every error answers with.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="problem--type"></span>`type` | string (uri) | yes | One of: `about:blank`, `https://liken.sh/problems/no-node`. |
| <span id="problem--title"></span>`title` | string | yes |  |
| <span id="problem--status"></span>`status` | integer | yes |  |

## Security

Every route requires `bearer`, unless its own section states otherwise.

| Scheme | Type | Description |
| --- | --- | --- |
| `bearer` | HTTP bearer, JWT | A ServiceAccount token minted with the audience widget-api. |
| `clientCertificate` | Mutual TLS | A certificate this API's own authority signed. |
