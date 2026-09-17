---
title: Routes
weight: 55
toc: true
---

<!-- Generated from content/docs/reference/openapi.json by apiref. Do not edit. -->

The routes below are generated from the OpenAPI document
`display-api` serves at `/v1/display/openapi.json`. The API renders
that document from its own router, so this page lists every route the
program serves.

[The screen over HTTP](/docs/reference/api/) is the page beside this
one. It says what the API is for, how it chooses a format, how it
reads a region and a time span, who may look at a screen, and what
each error means.

`display-api`, version `dev`, described in OpenAPI 3.1.1.

The screen of every Display in this cluster, as one frame, a clip or a stream.

Identity is in the path, the format is in the extension or in the Accept field, and a region and a time span are W3C Media Fragments 1.0 in the query. Nothing is stored. The manual is at https://display.liken.sh/docs/reference/api/.

## GET /v1/display

The discovery document

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | object | The routes this API serves, as RFC 6570 templates. |
| 304 | none | | The document has not changed since the entity tag the client holds. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | no-cache. A document is revalidated against its entity tag. |
| `ETag` | 200 | The build this document came from. |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## HEAD /v1/display

The discovery document

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | object | The routes this API serves, as RFC 6570 templates. |
| 304 | none | | The document has not changed since the entity tag the client holds. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | no-cache. A document is revalidated against its entity tag. |
| `ETag` | 200 | The build this document came from. |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## OPTIONS /v1/display

The methods this route answers

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods are in the Allow field (RFC 9110 section 10.2.1). |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | The methods this route answers. |

## GET /v1/display/displays/{name}

The screen's size, scale, refresh and formats

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | object | The size, scale, refresh, formats and clip codecs of one screen, read from the node. A screen whose compositor is not serving, or whose node this API cannot reach, answers the name, the node and the mode from the Display object, with compositor or sidecar naming what is wrong and the rest left out. |
| 304 | none | | The document has not changed since the entity tag the client holds. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 403 | `application/problem+json` | [Problem](#problem) | The SubjectAccessReview refused the subject. |
| 404 | `application/problem+json` | [Problem](#problem) | No Display of that name. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |
| 503 | `application/problem+json` | [Problem](#problem) | The screen has no node (no-node), the compositor is not serving it (compositor-down), the output is already being captured (capture-busy), or the sidecar is absent, not ready, or refused the connection (upstream-failed). |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | no-cache. A document is revalidated against its entity tag. |
| `ETag` | 200 | The build this document came from. |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## HEAD /v1/display/displays/{name}

The screen's size, scale, refresh and formats

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | object | The size, scale, refresh, formats and clip codecs of one screen, read from the node. A screen whose compositor is not serving, or whose node this API cannot reach, answers the name, the node and the mode from the Display object, with compositor or sidecar naming what is wrong and the rest left out. |
| 304 | none | | The document has not changed since the entity tag the client holds. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 403 | `application/problem+json` | [Problem](#problem) | The SubjectAccessReview refused the subject. |
| 404 | `application/problem+json` | [Problem](#problem) | No Display of that name. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |
| 503 | `application/problem+json` | [Problem](#problem) | The screen has no node (no-node), the compositor is not serving it (compositor-down), the output is already being captured (capture-busy), or the sidecar is absent, not ready, or refused the connection (upstream-failed). |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | no-cache. A document is revalidated against its entity tag. |
| `ETag` | 200 | The build this document came from. |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## OPTIONS /v1/display/displays/{name}

The methods this route answers

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods are in the Allow field (RFC 9110 section 10.2.1). |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | The methods this route answers. |

## GET /v1/display/displays/{name}/screen

The screen in the type Accept selects

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |
| `framerate` | query | no | integer | Frames per second of a clip or a stream, 15 by default, at most the output's refresh. |
| `quality` | query | no | integer | JPEG quality from 1 to 100, 85 by default. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `image/jpeg` | string (binary) | The screen in the type Accept selected, image/png by default. |
| 200 | `image/png` | string (binary) | The screen in the type Accept selected, image/png by default. |
| 200 | `multipart/x-mixed-replace` | string (binary) | The screen in the type Accept selected, image/png by default. |
| 200 | `video/mp4` | string (binary) | The screen in the type Accept selected, image/png by default. |
| 400 | `application/problem+json` | [Problem](#problem) | A query the grammar refuses. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 403 | `application/problem+json` | [Problem](#problem) | The SubjectAccessReview refused the subject. |
| 404 | `application/problem+json` | [Problem](#problem) | No Display of that name. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |
| 500 | `application/problem+json` | [Problem](#problem) | The compositor denied the capture (capture-denied), or the encoder wrote no picture (encoder-failed). |
| 502 | `application/problem+json` | [Problem](#problem) | The sidecar answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [Problem](#problem) | The screen has no node (no-node), the compositor is not serving it (compositor-down), the output is already being captured (capture-busy), or the sidecar is absent, not ready, or refused the connection (upstream-failed). |
| 504 | `application/problem+json` | [Problem](#problem) | The sidecar sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | none. A live capture has no byte identity (RFC 9110 section 14.3). |
| `Cache-Control` | 200 | no-store. A capture is never stored (RFC 9111 section 5.2.2.5). |
| `Content-Disposition` | 200 | inline, with the file name a browser save gets (RFC 6266 section 4). |
| `Content-Location` | 200 | The absolute path of the extension form served (RFC 9110 section 8.7). |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## HEAD /v1/display/displays/{name}/screen

The screen in the type Accept selects

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |
| `framerate` | query | no | integer | Frames per second of a clip or a stream, 15 by default, at most the output's refresh. |
| `quality` | query | no | integer | JPEG quality from 1 to 100, 85 by default. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `image/jpeg` | string (binary) | The screen in the type Accept selected, image/png by default. |
| 200 | `image/png` | string (binary) | The screen in the type Accept selected, image/png by default. |
| 200 | `multipart/x-mixed-replace` | string (binary) | The screen in the type Accept selected, image/png by default. |
| 200 | `video/mp4` | string (binary) | The screen in the type Accept selected, image/png by default. |
| 400 | `application/problem+json` | [Problem](#problem) | A query the grammar refuses. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 403 | `application/problem+json` | [Problem](#problem) | The SubjectAccessReview refused the subject. |
| 404 | `application/problem+json` | [Problem](#problem) | No Display of that name. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |
| 500 | `application/problem+json` | [Problem](#problem) | The compositor denied the capture (capture-denied), or the encoder wrote no picture (encoder-failed). |
| 502 | `application/problem+json` | [Problem](#problem) | The sidecar answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [Problem](#problem) | The screen has no node (no-node), the compositor is not serving it (compositor-down), the output is already being captured (capture-busy), or the sidecar is absent, not ready, or refused the connection (upstream-failed). |
| 504 | `application/problem+json` | [Problem](#problem) | The sidecar sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | none. A live capture has no byte identity (RFC 9110 section 14.3). |
| `Cache-Control` | 200 | no-store. A capture is never stored (RFC 9111 section 5.2.2.5). |
| `Content-Disposition` | 200 | inline, with the file name a browser save gets (RFC 6266 section 4). |
| `Content-Location` | 200 | The absolute path of the extension form served (RFC 9110 section 8.7). |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## OPTIONS /v1/display/displays/{name}/screen

The methods this route answers

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |
| `framerate` | query | no | integer | Frames per second of a clip or a stream, 15 by default, at most the output's refresh. |
| `quality` | query | no | integer | JPEG quality from 1 to 100, 85 by default. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods are in the Allow field (RFC 9110 section 10.2.1). |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | The methods this route answers. |

## GET /v1/display/displays/{name}/screen.jpg

One frame of the screen as JPEG

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |
| `quality` | query | no | integer | JPEG quality from 1 to 100, 85 by default. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `image/jpeg` | string (binary) | One frame of the screen, encoded as JPEG. |
| 400 | `application/problem+json` | [Problem](#problem) | A query the grammar refuses. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 403 | `application/problem+json` | [Problem](#problem) | The SubjectAccessReview refused the subject. |
| 404 | `application/problem+json` | [Problem](#problem) | No Display of that name. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |
| 500 | `application/problem+json` | [Problem](#problem) | The compositor denied the capture (capture-denied), or the encoder wrote no picture (encoder-failed). |
| 502 | `application/problem+json` | [Problem](#problem) | The sidecar answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [Problem](#problem) | The screen has no node (no-node), the compositor is not serving it (compositor-down), the output is already being captured (capture-busy), or the sidecar is absent, not ready, or refused the connection (upstream-failed). |
| 504 | `application/problem+json` | [Problem](#problem) | The sidecar sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | none. A live capture has no byte identity (RFC 9110 section 14.3). |
| `Cache-Control` | 200 | no-store. A capture is never stored (RFC 9111 section 5.2.2.5). |
| `Content-Disposition` | 200 | inline, with the file name a browser save gets (RFC 6266 section 4). |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## HEAD /v1/display/displays/{name}/screen.jpg

One frame of the screen as JPEG

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |
| `quality` | query | no | integer | JPEG quality from 1 to 100, 85 by default. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `image/jpeg` | string (binary) | One frame of the screen, encoded as JPEG. |
| 400 | `application/problem+json` | [Problem](#problem) | A query the grammar refuses. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 403 | `application/problem+json` | [Problem](#problem) | The SubjectAccessReview refused the subject. |
| 404 | `application/problem+json` | [Problem](#problem) | No Display of that name. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |
| 500 | `application/problem+json` | [Problem](#problem) | The compositor denied the capture (capture-denied), or the encoder wrote no picture (encoder-failed). |
| 502 | `application/problem+json` | [Problem](#problem) | The sidecar answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [Problem](#problem) | The screen has no node (no-node), the compositor is not serving it (compositor-down), the output is already being captured (capture-busy), or the sidecar is absent, not ready, or refused the connection (upstream-failed). |
| 504 | `application/problem+json` | [Problem](#problem) | The sidecar sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | none. A live capture has no byte identity (RFC 9110 section 14.3). |
| `Cache-Control` | 200 | no-store. A capture is never stored (RFC 9111 section 5.2.2.5). |
| `Content-Disposition` | 200 | inline, with the file name a browser save gets (RFC 6266 section 4). |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## OPTIONS /v1/display/displays/{name}/screen.jpg

The methods this route answers

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |
| `quality` | query | no | integer | JPEG quality from 1 to 100, 85 by default. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods are in the Allow field (RFC 9110 section 10.2.1). |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | The methods this route answers. |

## GET /v1/display/displays/{name}/screen.mjpeg

A stream of the screen, one JPEG per frame

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |
| `framerate` | query | no | integer | Frames per second of a clip or a stream, 15 by default, at most the output's refresh. |
| `quality` | query | no | integer | JPEG quality from 1 to 100, 85 by default. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `multipart/x-mixed-replace` | string (binary) | A stream of the screen, one image/jpeg part per frame, until the t= end or the client hangs up. |
| 400 | `application/problem+json` | [Problem](#problem) | A query the grammar refuses. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 403 | `application/problem+json` | [Problem](#problem) | The SubjectAccessReview refused the subject. |
| 404 | `application/problem+json` | [Problem](#problem) | No Display of that name. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |
| 500 | `application/problem+json` | [Problem](#problem) | The compositor denied the capture (capture-denied), or the encoder wrote no picture (encoder-failed). |
| 502 | `application/problem+json` | [Problem](#problem) | The sidecar answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [Problem](#problem) | The screen has no node (no-node), the compositor is not serving it (compositor-down), the output is already being captured (capture-busy), or the sidecar is absent, not ready, or refused the connection (upstream-failed). |
| 504 | `application/problem+json` | [Problem](#problem) | The sidecar sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | none. A live capture has no byte identity (RFC 9110 section 14.3). |
| `Cache-Control` | 200 | no-store. A capture is never stored (RFC 9111 section 5.2.2.5). |
| `Content-Disposition` | 200 | inline, with the file name a browser save gets (RFC 6266 section 4). |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## HEAD /v1/display/displays/{name}/screen.mjpeg

A stream of the screen, one JPEG per frame

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |
| `framerate` | query | no | integer | Frames per second of a clip or a stream, 15 by default, at most the output's refresh. |
| `quality` | query | no | integer | JPEG quality from 1 to 100, 85 by default. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `multipart/x-mixed-replace` | string (binary) | A stream of the screen, one image/jpeg part per frame, until the t= end or the client hangs up. |
| 400 | `application/problem+json` | [Problem](#problem) | A query the grammar refuses. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 403 | `application/problem+json` | [Problem](#problem) | The SubjectAccessReview refused the subject. |
| 404 | `application/problem+json` | [Problem](#problem) | No Display of that name. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |
| 500 | `application/problem+json` | [Problem](#problem) | The compositor denied the capture (capture-denied), or the encoder wrote no picture (encoder-failed). |
| 502 | `application/problem+json` | [Problem](#problem) | The sidecar answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [Problem](#problem) | The screen has no node (no-node), the compositor is not serving it (compositor-down), the output is already being captured (capture-busy), or the sidecar is absent, not ready, or refused the connection (upstream-failed). |
| 504 | `application/problem+json` | [Problem](#problem) | The sidecar sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | none. A live capture has no byte identity (RFC 9110 section 14.3). |
| `Cache-Control` | 200 | no-store. A capture is never stored (RFC 9111 section 5.2.2.5). |
| `Content-Disposition` | 200 | inline, with the file name a browser save gets (RFC 6266 section 4). |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## OPTIONS /v1/display/displays/{name}/screen.mjpeg

The methods this route answers

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |
| `framerate` | query | no | integer | Frames per second of a clip or a stream, 15 by default, at most the output's refresh. |
| `quality` | query | no | integer | JPEG quality from 1 to 100, 85 by default. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods are in the Allow field (RFC 9110 section 10.2.1). |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | The methods this route answers. |

## GET /v1/display/displays/{name}/screen.mp4

A clip of the screen as H.264 in fragmented MP4

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |
| `framerate` | query | no | integer | Frames per second of a clip or a stream, 15 by default, at most the output's refresh. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `video/mp4` | string (binary) | A clip of the screen, H.264 in fragmented MP4, until the t= end or the client hangs up. The Content-Type carries the codecs parameter RFC 6381 defines: avc1.640029, High profile at level 4.1, up to 1920x1080 at 60 fps, and avc1.640033, level 5.1, above that. |
| 400 | `application/problem+json` | [Problem](#problem) | A query the grammar refuses. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 403 | `application/problem+json` | [Problem](#problem) | The SubjectAccessReview refused the subject. |
| 404 | `application/problem+json` | [Problem](#problem) | No Display of that name. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |
| 500 | `application/problem+json` | [Problem](#problem) | The compositor denied the capture (capture-denied), or the encoder wrote no picture (encoder-failed). |
| 502 | `application/problem+json` | [Problem](#problem) | The sidecar answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [Problem](#problem) | The screen has no node (no-node), the compositor is not serving it (compositor-down), the output is already being captured (capture-busy), or the sidecar is absent, not ready, or refused the connection (upstream-failed). |
| 504 | `application/problem+json` | [Problem](#problem) | The sidecar sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | none. A live capture has no byte identity (RFC 9110 section 14.3). |
| `Cache-Control` | 200 | no-store. A capture is never stored (RFC 9111 section 5.2.2.5). |
| `Content-Disposition` | 200 | inline, with the file name a browser save gets (RFC 6266 section 4). |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## HEAD /v1/display/displays/{name}/screen.mp4

A clip of the screen as H.264 in fragmented MP4

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |
| `framerate` | query | no | integer | Frames per second of a clip or a stream, 15 by default, at most the output's refresh. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `video/mp4` | string (binary) | A clip of the screen, H.264 in fragmented MP4, until the t= end or the client hangs up. The Content-Type carries the codecs parameter RFC 6381 defines: avc1.640029, High profile at level 4.1, up to 1920x1080 at 60 fps, and avc1.640033, level 5.1, above that. |
| 400 | `application/problem+json` | [Problem](#problem) | A query the grammar refuses. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 403 | `application/problem+json` | [Problem](#problem) | The SubjectAccessReview refused the subject. |
| 404 | `application/problem+json` | [Problem](#problem) | No Display of that name. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |
| 500 | `application/problem+json` | [Problem](#problem) | The compositor denied the capture (capture-denied), or the encoder wrote no picture (encoder-failed). |
| 502 | `application/problem+json` | [Problem](#problem) | The sidecar answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [Problem](#problem) | The screen has no node (no-node), the compositor is not serving it (compositor-down), the output is already being captured (capture-busy), or the sidecar is absent, not ready, or refused the connection (upstream-failed). |
| 504 | `application/problem+json` | [Problem](#problem) | The sidecar sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | none. A live capture has no byte identity (RFC 9110 section 14.3). |
| `Cache-Control` | 200 | no-store. A capture is never stored (RFC 9111 section 5.2.2.5). |
| `Content-Disposition` | 200 | inline, with the file name a browser save gets (RFC 6266 section 4). |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## OPTIONS /v1/display/displays/{name}/screen.mp4

The methods this route answers

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |
| `framerate` | query | no | integer | Frames per second of a clip or a stream, 15 by default, at most the output's refresh. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods are in the Allow field (RFC 9110 section 10.2.1). |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | The methods this route answers. |

## GET /v1/display/displays/{name}/screen.png

One frame of the screen as PNG

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `image/png` | string (binary) | One frame of the screen, encoded as PNG. |
| 400 | `application/problem+json` | [Problem](#problem) | A query the grammar refuses. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 403 | `application/problem+json` | [Problem](#problem) | The SubjectAccessReview refused the subject. |
| 404 | `application/problem+json` | [Problem](#problem) | No Display of that name. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |
| 500 | `application/problem+json` | [Problem](#problem) | The compositor denied the capture (capture-denied), or the encoder wrote no picture (encoder-failed). |
| 502 | `application/problem+json` | [Problem](#problem) | The sidecar answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [Problem](#problem) | The screen has no node (no-node), the compositor is not serving it (compositor-down), the output is already being captured (capture-busy), or the sidecar is absent, not ready, or refused the connection (upstream-failed). |
| 504 | `application/problem+json` | [Problem](#problem) | The sidecar sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | none. A live capture has no byte identity (RFC 9110 section 14.3). |
| `Cache-Control` | 200 | no-store. A capture is never stored (RFC 9111 section 5.2.2.5). |
| `Content-Disposition` | 200 | inline, with the file name a browser save gets (RFC 6266 section 4). |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## HEAD /v1/display/displays/{name}/screen.png

One frame of the screen as PNG

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `image/png` | string (binary) | One frame of the screen, encoded as PNG. |
| 400 | `application/problem+json` | [Problem](#problem) | A query the grammar refuses. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 403 | `application/problem+json` | [Problem](#problem) | The SubjectAccessReview refused the subject. |
| 404 | `application/problem+json` | [Problem](#problem) | No Display of that name. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |
| 500 | `application/problem+json` | [Problem](#problem) | The compositor denied the capture (capture-denied), or the encoder wrote no picture (encoder-failed). |
| 502 | `application/problem+json` | [Problem](#problem) | The sidecar answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [Problem](#problem) | The screen has no node (no-node), the compositor is not serving it (compositor-down), the output is already being captured (capture-busy), or the sidecar is absent, not ready, or refused the connection (upstream-failed). |
| 504 | `application/problem+json` | [Problem](#problem) | The sidecar sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | none. A live capture has no byte identity (RFC 9110 section 14.3). |
| `Cache-Control` | 200 | no-store. A capture is never stored (RFC 9111 section 5.2.2.5). |
| `Content-Disposition` | 200 | inline, with the file name a browser save gets (RFC 6266 section 4). |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## OPTIONS /v1/display/displays/{name}/screen.png

The methods this route answers

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the Display. |
| `t` | query | no | string | A W3C Media Fragments 1.0 time range in NPT, whose zero is the instant the sidecar accepts the request. t=5,7 discards five seconds and then records two. |
| `xywh` | query | no | string | A W3C Media Fragments 1.0 region of the frame, pixel: by default and percent: on request, in the frame's own physical pixels. |
| `width` | query | no | integer | Scale the region down to this width, keeping the aspect. A value above the source is refused. |
| `height` | query | no | integer | Scale the region down to this height, keeping the aspect. Given together with width it is refused. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods are in the Allow field (RFC 9110 section 10.2.1). |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | The methods this route answers. |

## GET /v1/display/openapi.json

The OpenAPI 3.1 description of this API

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/openapi+json` | object | This document. |
| 304 | none | | The document has not changed since the entity tag the client holds. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | no-cache. A document is revalidated against its entity tag. |
| `ETag` | 200 | The build this document came from. |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## HEAD /v1/display/openapi.json

The OpenAPI 3.1 description of this API

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/openapi+json` | object | This document. |
| 304 | none | | The document has not changed since the entity tag the client holds. |
| 401 | `application/problem+json` | [Problem](#problem) | No client certificate and no token, or a token the TokenReview refused. |
| 405 | `application/problem+json` | [Problem](#problem) | A method other than GET, HEAD and OPTIONS. |
| 406 | `application/problem+json` | [Problem](#problem) | The Accept field excludes every form this route serves. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | no-cache. A document is revalidated against its entity tag. |
| `ETag` | 200 | The build this document came from. |
| `Link` | 200 | The service-desc, service-doc and describedby relations (RFC 8288). |
| `Vary` | 200 | Accept. The response was subject to negotiation (RFC 9110 section 12.5.5). |

## OPTIONS /v1/display/openapi.json

The methods this route answers

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods are in the Allow field (RFC 9110 section 10.2.1). |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | The methods this route answers. |

## Schemas

### Problem

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="problem--acceptable"></span>`acceptable` | \[\][object](#problemacceptable) | no |  |
| <span id="problem--detail"></span>`detail` | string | no |  |
| <span id="problem--instance"></span>`instance` | string | yes |  |
| <span id="problem--status"></span>`status` | integer | yes |  |
| <span id="problem--title"></span>`title` | string | yes |  |
| <span id="problem--type"></span>`type` | string (uri) | yes | One of: `about:blank`, `https://liken.sh/problems/no-node`, `https://liken.sh/problems/not-acceptable`, `https://liken.sh/problems/capture-busy`, `https://liken.sh/problems/upstream-failed`, `https://display.liken.sh/problems/capture-denied`, `https://display.liken.sh/problems/compositor-down`, `https://display.liken.sh/problems/encoder-failed`. |

#### Problem.acceptable[]

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="problemacceptable--href"></span>`href` | string | no |  |
| <span id="problemacceptable--type"></span>`type` | string | no |  |

## Security

Every route requires `mutualTLS`, or `bearer`, unless its own section states otherwise.

| Scheme | Type | Description |
| --- | --- | --- |
| `bearer` | HTTP bearer, JWT | A Kubernetes ServiceAccount token whose audience is display-api. |
| `mutualTLS` | Mutual TLS | A client certificate the cluster's own authority signed. The subject's common name is the user and its organization values are the groups, which is how the API server reads one. |

## The document itself

`display-api` serves the document this page is made from at
`/v1/display/openapi.json`, and this site publishes the same copy at
[/docs/reference/openapi.json](/docs/reference/openapi.json). A
client generator reads either one.
