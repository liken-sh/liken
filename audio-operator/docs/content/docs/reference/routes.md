---
title: Routes
weight: 55
toc: true
---

<!-- Generated from static/v1/audio/openapi.json by apiref. Do not edit. -->

The routes below are generated from the OpenAPI document `audio-api`
serves at `/v1/audio/openapi.json`. The API renders that document
from its router table, so this page lists every route the program
serves.

[The API page](/docs/reference/api/) is the page beside this one. It
says what the API is for, how it chooses a format, how it reads a
time span, who may tap a `Sink` or a `Source`, and what each error
means.

`audio.liken.sh capture API`, version `v1`, described in OpenAPI 3.1.1.

Taps what a Sink plays and what a Source hears, and streams it as WAV, FLAC, or Ogg Opus.

The path names the Sink or Source, the extension or Accept chooses the format, and a W3C Media Fragments t= in the query sets the span. Authenticate with a client certificate signed by the cluster's authority, or with a ServiceAccount token for the audience audio-api. A tap needs get on sinks/audio or sources/audio in the group audio.liken.sh. Nothing is stored. Every response streams from the node the endpoint is on.

## `GET` `/v1/audio` {data-method=GET}

The discovery document: every resource this API serves, with an RFC 6570 template for each aspect.

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | [discovery](#discovery) | The discovery document: every resource this API serves, with an RFC 6570 template for each aspect. |
| 304 | none | | The If-None-Match field matches this document's ETag. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always no-cache. A client revalidates with If-None-Match and gets a 304. |
| `ETag` | 200 | The build this API was made from. The router table is compiled in, so the build is the whole of a document's identity. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `HEAD` `/v1/audio` {data-method=HEAD}

The headers of the discovery document: every resource this API serves, with an RFC 6570 template for each aspect. HEAD takes no sample and makes no call to the capture container (RFC 9110 section 9.3.2).

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | [discovery](#discovery) | The discovery document: every resource this API serves, with an RFC 6570 template for each aspect. |
| 304 | none | | The If-None-Match field matches this document's ETag. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always no-cache. A client revalidates with If-None-Match and gets a 304. |
| `ETag` | 200 | The build this API was made from. The router table is compiled in, so the build is the whole of a document's identity. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `OPTIONS` `/v1/audio` {data-method=OPTIONS}

The methods this route allows, as a 204 with Allow (RFC 9110 section 10.2.1).

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods this route allows. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | GET, HEAD, OPTIONS. |

## `GET` `/v1/audio/openapi.json` {data-method=GET}

This document.

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/openapi+json` | string (binary) | This document. |
| 304 | none | | The If-None-Match field matches this document's ETag. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always no-cache. A client revalidates with If-None-Match and gets a 304. |
| `ETag` | 200 | The build this API was made from. The router table is compiled in, so the build is the whole of a document's identity. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `HEAD` `/v1/audio/openapi.json` {data-method=HEAD}

The headers of this document. HEAD takes no sample and makes no call to the capture container (RFC 9110 section 9.3.2).

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/openapi+json` | string (binary) | This document. |
| 304 | none | | The If-None-Match field matches this document's ETag. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always no-cache. A client revalidates with If-None-Match and gets a 304. |
| `ETag` | 200 | The build this API was made from. The router table is compiled in, so the build is the whole of a document's identity. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `OPTIONS` `/v1/audio/openapi.json` {data-method=OPTIONS}

The methods this route allows, as a 204 with Allow (RFC 9110 section 10.2.1).

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods this route allows. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | GET, HEAD, OPTIONS. |

## `GET` `/v1/audio/sinks/{name}` {data-method=GET}

The sink's format and the routes that tap it: the node a tap targets, the rate and channel count it would use, and the forms served.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | [endpoint](#endpoint) | The sink's format and the routes that tap it: the node a tap targets, the rate and channel count it would use, and the forms served. |
| 304 | none | | The If-None-Match field matches this document's ETag. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always no-cache. A client revalidates with If-None-Match and gets a 304. |
| `ETag` | 200 | The build this API was made from. The router table is compiled in, so the build is the whole of a document's identity. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `HEAD` `/v1/audio/sinks/{name}` {data-method=HEAD}

The headers of the sink's format and the routes that tap it: the node a tap targets, the rate and channel count it would use, and the forms served. HEAD takes no sample and makes no call to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | [endpoint](#endpoint) | The sink's format and the routes that tap it: the node a tap targets, the rate and channel count it would use, and the forms served. |
| 304 | none | | The If-None-Match field matches this document's ETag. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always no-cache. A client revalidates with If-None-Match and gets a 304. |
| `ETag` | 200 | The build this API was made from. The router table is compiled in, so the build is the whole of a document's identity. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `OPTIONS` `/v1/audio/sinks/{name}` {data-method=OPTIONS}

The methods this route allows, as a 204 with Allow (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods this route allows. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | GET, HEAD, OPTIONS. |

## `GET` `/v1/audio/sinks/{name}/audio` {data-method=GET}

What the speakers play now, in the format chosen by Accept.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, 6 to 256. opusenc chooses one from the sample rate when this is absent. A bitrate on WAV or FLAC is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | What the speakers play now, in the format chosen by Accept. |
| 200 | `audio/ogg; codecs=opus` | string (binary) | What the speakers play now, in the format chosen by Accept. |
| 200 | `audio/wav` | string (binary) | What the speakers play now, in the format chosen by Accept. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Content-Location` | 200 | The extension route that serves the representation the negotiation chose. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `HEAD` `/v1/audio/sinks/{name}/audio` {data-method=HEAD}

The headers of what the speakers play now, in the format chosen by Accept. HEAD takes no sample and makes no call to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, 6 to 256. opusenc chooses one from the sample rate when this is absent. A bitrate on WAV or FLAC is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | What the speakers play now, in the format chosen by Accept. |
| 200 | `audio/ogg; codecs=opus` | string (binary) | What the speakers play now, in the format chosen by Accept. |
| 200 | `audio/wav` | string (binary) | What the speakers play now, in the format chosen by Accept. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Content-Location` | 200 | The extension route that serves the representation the negotiation chose. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `OPTIONS` `/v1/audio/sinks/{name}/audio` {data-method=OPTIONS}

The methods this route allows, as a 204 with Allow (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods this route allows. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | GET, HEAD, OPTIONS. |

## `GET` `/v1/audio/sinks/{name}/audio.flac` {data-method=GET}

What the speakers play now, as FLAC.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | What the speakers play now, as FLAC. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `HEAD` `/v1/audio/sinks/{name}/audio.flac` {data-method=HEAD}

The headers of what the speakers play now, as FLAC. HEAD takes no sample and makes no call to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | What the speakers play now, as FLAC. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `OPTIONS` `/v1/audio/sinks/{name}/audio.flac` {data-method=OPTIONS}

The methods this route allows, as a 204 with Allow (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods this route allows. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | GET, HEAD, OPTIONS. |

## `GET` `/v1/audio/sinks/{name}/audio.opus` {data-method=GET}

What the speakers play now, as Ogg Opus.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, 6 to 256. opusenc chooses one from the sample rate when this is absent. A bitrate on WAV or FLAC is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/ogg; codecs=opus` | string (binary) | What the speakers play now, as Ogg Opus. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `HEAD` `/v1/audio/sinks/{name}/audio.opus` {data-method=HEAD}

The headers of what the speakers play now, as Ogg Opus. HEAD takes no sample and makes no call to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, 6 to 256. opusenc chooses one from the sample rate when this is absent. A bitrate on WAV or FLAC is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/ogg; codecs=opus` | string (binary) | What the speakers play now, as Ogg Opus. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `OPTIONS` `/v1/audio/sinks/{name}/audio.opus` {data-method=OPTIONS}

The methods this route allows, as a 204 with Allow (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods this route allows. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | GET, HEAD, OPTIONS. |

## `GET` `/v1/audio/sinks/{name}/audio.wav` {data-method=GET}

What the speakers play now, as PCM in a RIFF WAVE stream.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/wav` | string (binary) | What the speakers play now, as PCM in a RIFF WAVE stream. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `HEAD` `/v1/audio/sinks/{name}/audio.wav` {data-method=HEAD}

The headers of what the speakers play now, as PCM in a RIFF WAVE stream. HEAD takes no sample and makes no call to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/wav` | string (binary) | What the speakers play now, as PCM in a RIFF WAVE stream. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `OPTIONS` `/v1/audio/sinks/{name}/audio.wav` {data-method=OPTIONS}

The methods this route allows, as a 204 with Allow (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods this route allows. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | GET, HEAD, OPTIONS. |

## `GET` `/v1/audio/sources/{name}` {data-method=GET}

The source's format and the routes that tap it: the node a tap targets, the rate and channel count it would use, and the forms served.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | [endpoint](#endpoint) | The source's format and the routes that tap it: the node a tap targets, the rate and channel count it would use, and the forms served. |
| 304 | none | | The If-None-Match field matches this document's ETag. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always no-cache. A client revalidates with If-None-Match and gets a 304. |
| `ETag` | 200 | The build this API was made from. The router table is compiled in, so the build is the whole of a document's identity. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `HEAD` `/v1/audio/sources/{name}` {data-method=HEAD}

The headers of the source's format and the routes that tap it: the node a tap targets, the rate and channel count it would use, and the forms served. HEAD takes no sample and makes no call to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | [endpoint](#endpoint) | The source's format and the routes that tap it: the node a tap targets, the rate and channel count it would use, and the forms served. |
| 304 | none | | The If-None-Match field matches this document's ETag. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always no-cache. A client revalidates with If-None-Match and gets a 304. |
| `ETag` | 200 | The build this API was made from. The router table is compiled in, so the build is the whole of a document's identity. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `OPTIONS` `/v1/audio/sources/{name}` {data-method=OPTIONS}

The methods this route allows, as a 204 with Allow (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods this route allows. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | GET, HEAD, OPTIONS. |

## `GET` `/v1/audio/sources/{name}/audio` {data-method=GET}

What the microphone hears now, in the format chosen by Accept.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, 6 to 256. opusenc chooses one from the sample rate when this is absent. A bitrate on WAV or FLAC is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | What the microphone hears now, in the format chosen by Accept. |
| 200 | `audio/ogg; codecs=opus` | string (binary) | What the microphone hears now, in the format chosen by Accept. |
| 200 | `audio/wav` | string (binary) | What the microphone hears now, in the format chosen by Accept. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Content-Location` | 200 | The extension route that serves the representation the negotiation chose. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `HEAD` `/v1/audio/sources/{name}/audio` {data-method=HEAD}

The headers of what the microphone hears now, in the format chosen by Accept. HEAD takes no sample and makes no call to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, 6 to 256. opusenc chooses one from the sample rate when this is absent. A bitrate on WAV or FLAC is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | What the microphone hears now, in the format chosen by Accept. |
| 200 | `audio/ogg; codecs=opus` | string (binary) | What the microphone hears now, in the format chosen by Accept. |
| 200 | `audio/wav` | string (binary) | What the microphone hears now, in the format chosen by Accept. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Content-Location` | 200 | The extension route that serves the representation the negotiation chose. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `OPTIONS` `/v1/audio/sources/{name}/audio` {data-method=OPTIONS}

The methods this route allows, as a 204 with Allow (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods this route allows. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | GET, HEAD, OPTIONS. |

## `GET` `/v1/audio/sources/{name}/audio.flac` {data-method=GET}

What the microphone hears now, as FLAC.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | What the microphone hears now, as FLAC. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `HEAD` `/v1/audio/sources/{name}/audio.flac` {data-method=HEAD}

The headers of what the microphone hears now, as FLAC. HEAD takes no sample and makes no call to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | What the microphone hears now, as FLAC. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `OPTIONS` `/v1/audio/sources/{name}/audio.flac` {data-method=OPTIONS}

The methods this route allows, as a 204 with Allow (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods this route allows. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | GET, HEAD, OPTIONS. |

## `GET` `/v1/audio/sources/{name}/audio.opus` {data-method=GET}

What the microphone hears now, as Ogg Opus.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, 6 to 256. opusenc chooses one from the sample rate when this is absent. A bitrate on WAV or FLAC is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/ogg; codecs=opus` | string (binary) | What the microphone hears now, as Ogg Opus. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `HEAD` `/v1/audio/sources/{name}/audio.opus` {data-method=HEAD}

The headers of what the microphone hears now, as Ogg Opus. HEAD takes no sample and makes no call to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, 6 to 256. opusenc chooses one from the sample rate when this is absent. A bitrate on WAV or FLAC is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/ogg; codecs=opus` | string (binary) | What the microphone hears now, as Ogg Opus. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `OPTIONS` `/v1/audio/sources/{name}/audio.opus` {data-method=OPTIONS}

The methods this route allows, as a 204 with Allow (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods this route allows. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | GET, HEAD, OPTIONS. |

## `GET` `/v1/audio/sources/{name}/audio.wav` {data-method=GET}

What the microphone hears now, as PCM in a RIFF WAVE stream.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/wav` | string (binary) | What the microphone hears now, as PCM in a RIFF WAVE stream. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `HEAD` `/v1/audio/sources/{name}/audio.wav` {data-method=HEAD}

The headers of what the microphone hears now, as PCM in a RIFF WAVE stream. HEAD takes no sample and makes no call to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |
| `t` | query | no | string | The W3C Media Fragments temporal dimension in NPT: t=begin,end, t=begin, or t=,end. The interval is half-open, and its zero is the instant the capture container accepts the request. A begin over 60 seconds is a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/wav` | string (binary) | What the microphone hears now, as PCM in a RIFF WAVE stream. |
| 400 | `application/problem+json` | [problem](#problem) | A t= the grammar refuses, a t=a,b with a at or after b, a begin over 60 seconds, a repeated dimension, an unknown query parameter, or a knob the format does not take. |
| 401 | `application/problem+json` | [problem](#problem) | No client certificate and no token, or a token the TokenReview refuses. The WWW-Authenticate header carries the review's own words. |
| 403 | `application/problem+json` | [problem](#problem) | The SubjectAccessReview said no. The WWW-Authenticate header names the scope the caller would need. |
| 404 | `application/problem+json` | [problem](#problem) | No Sink or Source of that name, or PipeWire holds no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | A method other than GET, HEAD, and OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | Accept excludes every representation the route serves. The acceptable member lists what it does serve, with the URI of each. |
| 409 | `application/problem+json` | [problem](#problem) | The endpoint has no status.node. The detail says to power the device on. |
| 500 | `application/problem+json` | [problem](#problem) | The tap's link landed on a node other than the one asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container answered something that is not HTTP or not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The capture container is at its tap limit, refused the connection, is not ready, has no certificate this API trusts, or PipeWire refused pw-record. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always none. A live capture has no byte identity: offset 44 of two requests is two different moments. |
| `Cache-Control` | 200 | Always no-store. A capture is never cacheable. |
| `Content-Disposition` | 200 | The save name a browser gets, with the RFC 3339 time's colons replaced by dashes. |
| `Link` | 200 | The service-desc and service-doc relations on every answer, describedby on every route that names an object, alternate on the extensionless route, and related from an info document to its capture routes. |
| `Vary` | 200 | Always Accept. RFC 9110 section 12.5.5's second purpose: this answer was subject to negotiation, and an Accept could have made it a 406. |

## `OPTIONS` `/v1/audio/sources/{name}/audio.wav` {data-method=OPTIONS}

The methods this route allows, as a 204 with Allow (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The Sink's or Source's own name, which the operator builds from the hardware's identity. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | The methods this route allows. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | GET, HEAD, OPTIONS. |

## Schemas

### discovery

The discovery document the three capture APIs share.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="discovery--openapi"></span>`openapi` | string | yes |  |
| <span id="discovery--resources"></span>`resources` | object | yes |  |

### endpoint

One endpoint's own document: the node a tap targets, the rate and channel count it would use, the format the node reports now, and the forms served.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="endpoint--channels"></span>`channels` | integer | no |  |
| <span id="endpoint--connectiontype"></span>`connectionType` | string | no |  |
| <span id="endpoint--extensions"></span>`extensions` | []string | yes |  |
| <span id="endpoint--format"></span>`format` | object | no |  |
| <span id="endpoint--kind"></span>`kind` | string | yes |  |
| <span id="endpoint--mediatypes"></span>`mediaTypes` | []string | yes |  |
| <span id="endpoint--name"></span>`name` | string | yes |  |
| <span id="endpoint--node"></span>`node` | string | yes |  |
| <span id="endpoint--nodename"></span>`nodeName` | string | no |  |
| <span id="endpoint--rate"></span>`rate` | integer | no |  |

### problem

The RFC 9457 problem document every error answers with. detail carries the source's own words: pw-record's stderr, an encoder's stderr, or the API server's status message. instance is the request path, a number sign, and the request id the log line carries.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="problem--acceptable"></span>`acceptable` | \[\][object](#problemacceptable) | no |  |
| <span id="problem--detail"></span>`detail` | string | no |  |
| <span id="problem--instance"></span>`instance` | string | yes |  |
| <span id="problem--status"></span>`status` | integer | yes |  |
| <span id="problem--title"></span>`title` | string | yes |  |
| <span id="problem--type"></span>`type` | string (uri) | yes |  |

#### problem.acceptable[]

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="problemacceptable--href"></span>`href` | string | yes |  |
| <span id="problemacceptable--type"></span>`type` | string | yes |  |

## Security

Every route requires `mutualTLS`, or `bearerToken`, unless its own section states otherwise.

| Scheme | Type | Description |
| --- | --- | --- |
| `bearerToken` | HTTP bearer, JWT | A ServiceAccount token minted with the audience audio-api, which this API checks with a TokenReview. |
| `mutualTLS` | Mutual TLS | A client certificate the cluster's own authority signed. The subject's common name is the user and its organization values are the groups, which is how the API server reads one. |

## The document itself

`audio-api` serves the document this page is made from, at
`/v1/audio/openapi.json` on the API's own host. This site publishes
the same copy at [/v1/audio/openapi.json](/v1/audio/openapi.json),
where the `servers` member is a placeholder: the served copy names
the origin the request reached.
