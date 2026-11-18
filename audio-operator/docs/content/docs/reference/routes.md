---
title: Routes
weight: 55
toc: true
---

<!-- Generated from static/v1/audio/openapi.json by apiref. Do not edit. -->

# Routes

The routes below are generated from the OpenAPI document that
`audio-api` serves at `/v1/audio/openapi.json`. The API renders that
document from its route table, so this page lists every route that the
API serves.

The [API page](/docs/reference/api/) explains what the API is for, how
it chooses a format, how it reads a time span, who may tap a `Sink` or
a `Source`, and what each error means.

`audio.liken.sh capture API`, version `v1`, described in OpenAPI 3.1.1.

Streams the sound of a `Sink` or a `Source` as WAV, FLAC, or Ogg Opus.

The path names the `Sink` or `Source` to tap. You choose the format with the extension of the path or with the `Accept` header, and a W3C Media Fragments `t=` query parameter limits the time span. Authenticate with a client certificate that the cluster's certificate authority signed, or with a `ServiceAccount` token that has the audience `audio-api`. A tap requires `get` on `sinks/audio` or `sources/audio` in the `audio.liken.sh` API group. The capture container on the node that has the `Sink` or `Source` records the sound, and nothing stores it.

## `GET` `/v1/audio` {data-method=GET}

The discovery document. It lists each resource that this API serves, with an RFC 6570 URI template for its tap routes.

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | [discovery](#discovery) | The discovery document. It lists each resource that this API serves, with an RFC 6570 URI template for its tap routes. |
| 304 | none | | `If-None-Match` matches the `ETag` of this document. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always `no-cache`. A client revalidates with `If-None-Match` and gets a 304. |
| `ETag` | 200 | The version of the build of this API. The route table is compiled in, so the build version identifies the document. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |

## `HEAD` `/v1/audio` {data-method=HEAD}

The headers that GET returns for this route, with no body. HEAD records no sound and makes no request to the capture container (RFC 9110 section 9.3.2).

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | [discovery](#discovery) | The discovery document. It lists each resource that this API serves, with an RFC 6570 URI template for its tap routes. |
| 304 | none | | `If-None-Match` matches the `ETag` of this document. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always `no-cache`. A client revalidates with `If-None-Match` and gets a 304. |
| `ETag` | 200 | The version of the build of this API. The route table is compiled in, so the build version identifies the document. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |

## `OPTIONS` `/v1/audio` {data-method=OPTIONS}

Returns 204 with an `Allow` header that lists the methods this route accepts (RFC 9110 section 10.2.1).

**Answers**

| Status | Description |
| --- | --- |
| 204 | No body. The `Allow` header lists the methods. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | Always `GET, HEAD, OPTIONS`. |

## `GET` `/v1/audio/openapi.json` {data-method=GET}

The OpenAPI 3.1 document that describes this API.

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/openapi+json` | string (binary) | The OpenAPI 3.1 document that describes this API. |
| 304 | none | | `If-None-Match` matches the `ETag` of this document. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always `no-cache`. A client revalidates with `If-None-Match` and gets a 304. |
| `ETag` | 200 | The version of the build of this API. The route table is compiled in, so the build version identifies the document. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |

## `HEAD` `/v1/audio/openapi.json` {data-method=HEAD}

The headers that GET returns for this route, with no body. HEAD records no sound and makes no request to the capture container (RFC 9110 section 9.3.2).

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/openapi+json` | string (binary) | The OpenAPI 3.1 document that describes this API. |
| 304 | none | | `If-None-Match` matches the `ETag` of this document. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always `no-cache`. A client revalidates with `If-None-Match` and gets a 304. |
| `ETag` | 200 | The version of the build of this API. The route table is compiled in, so the build version identifies the document. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |

## `OPTIONS` `/v1/audio/openapi.json` {data-method=OPTIONS}

Returns 204 with an `Allow` header that lists the methods this route accepts (RFC 9110 section 10.2.1).

**Answers**

| Status | Description |
| --- | --- |
| 204 | No body. The `Allow` header lists the methods. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | Always `GET, HEAD, OPTIONS`. |

## `GET` `/v1/audio/sinks/{name}` {data-method=GET}

The info document of the `Sink`: the node that a tap runs on, the rate and channel count that a tap uses, and the formats that the tap routes return.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | [endpoint](#endpoint) | The info document of the `Sink`: the node that a tap runs on, the rate and channel count that a tap uses, and the formats that the tap routes return. |
| 304 | none | | `If-None-Match` matches the `ETag` of this document. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always `no-cache`. A client revalidates with `If-None-Match` and gets a 304. |
| `ETag` | 200 | The version of the build of this API. The route table is compiled in, so the build version identifies the document. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |

## `HEAD` `/v1/audio/sinks/{name}` {data-method=HEAD}

The headers that GET returns for this route, with no body. HEAD records no sound and makes no request to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | [endpoint](#endpoint) | The info document of the `Sink`: the node that a tap runs on, the rate and channel count that a tap uses, and the formats that the tap routes return. |
| 304 | none | | `If-None-Match` matches the `ETag` of this document. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always `no-cache`. A client revalidates with `If-None-Match` and gets a 304. |
| `ETag` | 200 | The version of the build of this API. The route table is compiled in, so the build version identifies the document. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |

## `OPTIONS` `/v1/audio/sinks/{name}` {data-method=OPTIONS}

Returns 204 with an `Allow` header that lists the methods this route accepts (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | No body. The `Allow` header lists the methods. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | Always `GET, HEAD, OPTIONS`. |

## `GET` `/v1/audio/sinks/{name}/audio` {data-method=GET}

Streams the sound that the speakers of the `Sink` are playing now, in the format that the `Accept` header selects. The default is WAV.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, from 6 to 256. When the request has no `bitrate`, `opusenc` chooses one from the sample rate. A request for WAV or FLAC that has a `bitrate` gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | Streams the sound that the speakers of the `Sink` are playing now, in the format that the `Accept` header selects. The default is WAV. |
| 200 | `audio/ogg; codecs=opus` | string (binary) | Streams the sound that the speakers of the `Sink` are playing now, in the format that the `Accept` header selects. The default is WAV. |
| 200 | `audio/wav` | string (binary) | Streams the sound that the speakers of the `Sink` are playing now, in the format that the `Accept` header selects. The default is WAV. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Content-Location` | 200 | The extension route that serves the format that negotiation chose. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `HEAD` `/v1/audio/sinks/{name}/audio` {data-method=HEAD}

The headers that GET returns for this route, with no body. HEAD records no sound and makes no request to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, from 6 to 256. When the request has no `bitrate`, `opusenc` chooses one from the sample rate. A request for WAV or FLAC that has a `bitrate` gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | Streams the sound that the speakers of the `Sink` are playing now, in the format that the `Accept` header selects. The default is WAV. |
| 200 | `audio/ogg; codecs=opus` | string (binary) | Streams the sound that the speakers of the `Sink` are playing now, in the format that the `Accept` header selects. The default is WAV. |
| 200 | `audio/wav` | string (binary) | Streams the sound that the speakers of the `Sink` are playing now, in the format that the `Accept` header selects. The default is WAV. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Content-Location` | 200 | The extension route that serves the format that negotiation chose. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `OPTIONS` `/v1/audio/sinks/{name}/audio` {data-method=OPTIONS}

Returns 204 with an `Allow` header that lists the methods this route accepts (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | No body. The `Allow` header lists the methods. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | Always `GET, HEAD, OPTIONS`. |

## `GET` `/v1/audio/sinks/{name}/audio.flac` {data-method=GET}

Streams the sound that the speakers of the `Sink` are playing now, as FLAC.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | Streams the sound that the speakers of the `Sink` are playing now, as FLAC. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `HEAD` `/v1/audio/sinks/{name}/audio.flac` {data-method=HEAD}

The headers that GET returns for this route, with no body. HEAD records no sound and makes no request to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | Streams the sound that the speakers of the `Sink` are playing now, as FLAC. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `OPTIONS` `/v1/audio/sinks/{name}/audio.flac` {data-method=OPTIONS}

Returns 204 with an `Allow` header that lists the methods this route accepts (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | No body. The `Allow` header lists the methods. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | Always `GET, HEAD, OPTIONS`. |

## `GET` `/v1/audio/sinks/{name}/audio.opus` {data-method=GET}

Streams the sound that the speakers of the `Sink` are playing now, as Ogg Opus.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, from 6 to 256. When the request has no `bitrate`, `opusenc` chooses one from the sample rate. A request for WAV or FLAC that has a `bitrate` gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/ogg; codecs=opus` | string (binary) | Streams the sound that the speakers of the `Sink` are playing now, as Ogg Opus. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `HEAD` `/v1/audio/sinks/{name}/audio.opus` {data-method=HEAD}

The headers that GET returns for this route, with no body. HEAD records no sound and makes no request to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, from 6 to 256. When the request has no `bitrate`, `opusenc` chooses one from the sample rate. A request for WAV or FLAC that has a `bitrate` gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/ogg; codecs=opus` | string (binary) | Streams the sound that the speakers of the `Sink` are playing now, as Ogg Opus. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `OPTIONS` `/v1/audio/sinks/{name}/audio.opus` {data-method=OPTIONS}

Returns 204 with an `Allow` header that lists the methods this route accepts (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | No body. The `Allow` header lists the methods. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | Always `GET, HEAD, OPTIONS`. |

## `GET` `/v1/audio/sinks/{name}/audio.wav` {data-method=GET}

Streams the sound that the speakers of the `Sink` are playing now, as PCM in a RIFF WAVE stream.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/wav` | string (binary) | Streams the sound that the speakers of the `Sink` are playing now, as PCM in a RIFF WAVE stream. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `HEAD` `/v1/audio/sinks/{name}/audio.wav` {data-method=HEAD}

The headers that GET returns for this route, with no body. HEAD records no sound and makes no request to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/wav` | string (binary) | Streams the sound that the speakers of the `Sink` are playing now, as PCM in a RIFF WAVE stream. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `OPTIONS` `/v1/audio/sinks/{name}/audio.wav` {data-method=OPTIONS}

Returns 204 with an `Allow` header that lists the methods this route accepts (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | No body. The `Allow` header lists the methods. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | Always `GET, HEAD, OPTIONS`. |

## `GET` `/v1/audio/sources/{name}` {data-method=GET}

The info document of the `Source`: the node that a tap runs on, the rate and channel count that a tap uses, and the formats that the tap routes return.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | [endpoint](#endpoint) | The info document of the `Source`: the node that a tap runs on, the rate and channel count that a tap uses, and the formats that the tap routes return. |
| 304 | none | | `If-None-Match` matches the `ETag` of this document. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always `no-cache`. A client revalidates with `If-None-Match` and gets a 304. |
| `ETag` | 200 | The version of the build of this API. The route table is compiled in, so the build version identifies the document. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |

## `HEAD` `/v1/audio/sources/{name}` {data-method=HEAD}

The headers that GET returns for this route, with no body. HEAD records no sound and makes no request to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `application/json` | [endpoint](#endpoint) | The info document of the `Source`: the node that a tap runs on, the rate and channel count that a tap uses, and the formats that the tap routes return. |
| 304 | none | | `If-None-Match` matches the `ETag` of this document. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Cache-Control` | 200 | Always `no-cache`. A client revalidates with `If-None-Match` and gets a 304. |
| `ETag` | 200 | The version of the build of this API. The route table is compiled in, so the build version identifies the document. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |

## `OPTIONS` `/v1/audio/sources/{name}` {data-method=OPTIONS}

Returns 204 with an `Allow` header that lists the methods this route accepts (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | No body. The `Allow` header lists the methods. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | Always `GET, HEAD, OPTIONS`. |

## `GET` `/v1/audio/sources/{name}/audio` {data-method=GET}

Streams the sound that the microphone of the `Source` is capturing now, in the format that the `Accept` header selects. The default is WAV.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, from 6 to 256. When the request has no `bitrate`, `opusenc` chooses one from the sample rate. A request for WAV or FLAC that has a `bitrate` gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | Streams the sound that the microphone of the `Source` is capturing now, in the format that the `Accept` header selects. The default is WAV. |
| 200 | `audio/ogg; codecs=opus` | string (binary) | Streams the sound that the microphone of the `Source` is capturing now, in the format that the `Accept` header selects. The default is WAV. |
| 200 | `audio/wav` | string (binary) | Streams the sound that the microphone of the `Source` is capturing now, in the format that the `Accept` header selects. The default is WAV. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Content-Location` | 200 | The extension route that serves the format that negotiation chose. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `HEAD` `/v1/audio/sources/{name}/audio` {data-method=HEAD}

The headers that GET returns for this route, with no body. HEAD records no sound and makes no request to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, from 6 to 256. When the request has no `bitrate`, `opusenc` chooses one from the sample rate. A request for WAV or FLAC that has a `bitrate` gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | Streams the sound that the microphone of the `Source` is capturing now, in the format that the `Accept` header selects. The default is WAV. |
| 200 | `audio/ogg; codecs=opus` | string (binary) | Streams the sound that the microphone of the `Source` is capturing now, in the format that the `Accept` header selects. The default is WAV. |
| 200 | `audio/wav` | string (binary) | Streams the sound that the microphone of the `Source` is capturing now, in the format that the `Accept` header selects. The default is WAV. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Content-Location` | 200 | The extension route that serves the format that negotiation chose. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `OPTIONS` `/v1/audio/sources/{name}/audio` {data-method=OPTIONS}

Returns 204 with an `Allow` header that lists the methods this route accepts (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | No body. The `Allow` header lists the methods. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | Always `GET, HEAD, OPTIONS`. |

## `GET` `/v1/audio/sources/{name}/audio.flac` {data-method=GET}

Streams the sound that the microphone of the `Source` is capturing now, as FLAC.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | Streams the sound that the microphone of the `Source` is capturing now, as FLAC. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `HEAD` `/v1/audio/sources/{name}/audio.flac` {data-method=HEAD}

The headers that GET returns for this route, with no body. HEAD records no sound and makes no request to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/flac` | string (binary) | Streams the sound that the microphone of the `Source` is capturing now, as FLAC. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `OPTIONS` `/v1/audio/sources/{name}/audio.flac` {data-method=OPTIONS}

Returns 204 with an `Allow` header that lists the methods this route accepts (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | No body. The `Allow` header lists the methods. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | Always `GET, HEAD, OPTIONS`. |

## `GET` `/v1/audio/sources/{name}/audio.opus` {data-method=GET}

Streams the sound that the microphone of the `Source` is capturing now, as Ogg Opus.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, from 6 to 256. When the request has no `bitrate`, `opusenc` chooses one from the sample rate. A request for WAV or FLAC that has a `bitrate` gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/ogg; codecs=opus` | string (binary) | Streams the sound that the microphone of the `Source` is capturing now, as Ogg Opus. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `HEAD` `/v1/audio/sources/{name}/audio.opus` {data-method=HEAD}

The headers that GET returns for this route, with no body. HEAD records no sound and makes no request to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |
| `bitrate` | query | no | string | The Opus bitrate in kbit/s per channel, from 6 to 256. When the request has no `bitrate`, `opusenc` chooses one from the sample rate. A request for WAV or FLAC that has a `bitrate` gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/ogg; codecs=opus` | string (binary) | Streams the sound that the microphone of the `Source` is capturing now, as Ogg Opus. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `OPTIONS` `/v1/audio/sources/{name}/audio.opus` {data-method=OPTIONS}

Returns 204 with an `Allow` header that lists the methods this route accepts (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | No body. The `Allow` header lists the methods. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | Always `GET, HEAD, OPTIONS`. |

## `GET` `/v1/audio/sources/{name}/audio.wav` {data-method=GET}

Streams the sound that the microphone of the `Source` is capturing now, as PCM in a RIFF WAVE stream.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/wav` | string (binary) | Streams the sound that the microphone of the `Source` is capturing now, as PCM in a RIFF WAVE stream. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `HEAD` `/v1/audio/sources/{name}/audio.wav` {data-method=HEAD}

The headers that GET returns for this route, with no body. HEAD records no sound and makes no request to the capture container (RFC 9110 section 9.3.2).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |
| `t` | query | no | string | The part of the capture to return, as a W3C Media Fragments time in NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval includes `begin` and excludes `end`. Zero is the moment that the capture container accepts the request. A `begin` over 60 seconds gets a 400. |

**Answers**

| Status | Media type | Schema | Description |
| --- | --- | --- | --- |
| 200 | `audio/wav` | string (binary) | Streams the sound that the microphone of the `Source` is capturing now, as PCM in a RIFF WAVE stream. |
| 400 | `application/problem+json` | [problem](#problem) | The `t=` value does not match the grammar, `t=a,b` has `a` at or after `b`, the begin is over 60 seconds, a dimension is repeated, a query parameter is unknown, or the format does not accept the parameter. |
| 401 | `application/problem+json` | [problem](#problem) | The request has no client certificate and no token, or the `TokenReview` refuses the token. The `WWW-Authenticate` header contains the review's message. |
| 403 | `application/problem+json` | [problem](#problem) | The `SubjectAccessReview` denied the request. The `WWW-Authenticate` header names the scope that the caller needs. |
| 404 | `application/problem+json` | [problem](#problem) | No `Sink` or `Source` has that name, or PipeWire has no node for it. |
| 405 | `application/problem+json` | [problem](#problem) | The method is not GET, HEAD, or OPTIONS. |
| 406 | `application/problem+json` | [problem](#problem) | `Accept` excludes every representation that the route serves. The `acceptable` member lists the representations that it does serve, each with its URI. |
| 409 | `application/problem+json` | [problem](#problem) | The `Sink` or `Source` has no `status.node`. The `detail` says to power on or connect the device. |
| 500 | `application/problem+json` | [problem](#problem) | The tap connected to a node other than the one that the request asked for. |
| 502 | `application/problem+json` | [problem](#problem) | The capture container returned a response that is not HTTP, or an error response that is not a problem document. |
| 503 | `application/problem+json` | [problem](#problem) | The API could not read the `Sink` or `Source` from the API server, the node has no Ready capture container, or the capture container is at its tap limit, refused the connection, has no certificate that this API trusts, or could not read the PipeWire graph, or PipeWire refused `pw-record`. |
| 504 | `application/problem+json` | [problem](#problem) | The capture container sent no response headers within the header timeout. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Accept-Ranges` | 200 | Always `none`, because a live capture has no fixed bytes: byte 44 of two requests comes from two different moments. |
| `Cache-Control` | 200 | Always `no-store`, because a live capture never returns the same bytes twice. |
| `Content-Disposition` | 200 | The file name that a browser saves: `<name>-<time>.<ext>`, where the time is RFC 3339 UTC with the colons replaced by dashes. |
| `Link` | 200 | Every response has the `service-desc` and `service-doc` relations. A route that names an object adds `describedby`. The route with no extension adds `alternate` for each format, and the info document of a `Sink` or `Source` adds `related` for each tap route. |
| `Vary` | 200 | Always `Accept`. The response depends on negotiation, because an `Accept` header can turn the 200 of any route into a 406 (RFC 9110 section 12.5.5). |
| `X-Content-Type-Options` | 200 | Always `nosniff`, so a browser uses the `Content-Type` and does not guess a type from the bytes (Fetch Standard section 3.6). |

## `OPTIONS` `/v1/audio/sources/{name}/audio.wav` {data-method=OPTIONS}

Returns 204 with an `Allow` header that lists the methods this route accepts (RFC 9110 section 10.2.1).

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `name` | path | yes | string | The name of the `Sink` or `Source`. The operator builds the name from the hardware identity of the device. |

**Answers**

| Status | Description |
| --- | --- |
| 204 | No body. The `Allow` header lists the methods. |

**Headers**

| Header | Status | Description |
| --- | --- | --- |
| `Allow` | 204 | Always `GET, HEAD, OPTIONS`. |

## Schemas

### discovery

The discovery document. The audio, display, and media capture APIs use the same shape.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="discovery--openapi"></span>`openapi` | string | yes |  |
| <span id="discovery--resources"></span>`resources` | object | yes |  |

### endpoint

The info document of a `Sink` or `Source`: the node that a tap runs on, the rate and channel count that a tap uses, the format that the node reports now, and the media types and extensions that the capture routes serve.

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

The RFC 9457 problem document that every error response contains. `detail` contains the error text of the part that failed, word for word: the `stderr` of `pw-record` or of an encoder, or the status message of the API server. `instance` is the request path, a `#`, and the request ID, which the log line of the request also has.

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
| `bearerToken` | HTTP bearer, JWT | A `ServiceAccount` token with the audience `audio-api`. This API validates it with a `TokenReview`. |
| `mutualTLS` | Mutual TLS | A client certificate that the cluster's own certificate authority signed. The subject's common name is the user name and its organization values are the groups, as the Kubernetes API server reads a certificate. |

## The document itself

`audio-api` serves the document that this page is generated from at
`/v1/audio/openapi.json`, on the API's own host. This site publishes a
copy at [/v1/audio/openapi.json](/v1/audio/openapi.json), in which the
`servers` member is a placeholder origin. The served copy names the
configured public base URL, or the origin of the request when no base
URL is configured.
