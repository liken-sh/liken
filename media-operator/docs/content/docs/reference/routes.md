---
title: Routes
weight: 66
toc: true
---

<!-- Generated from openapi.json by apiref. Do not edit. -->

The routes below are generated from the OpenAPI document `media-api`
serves at `/v1/media/openapi.json`. The API renders that document
from its router, so this page lists every route the program serves.

[The media API](/docs/reference/api/) is the page beside this one. It
says what the API is for, which routes redirect to `display-api` and
`audio-api`, how it composes a screen with its sound, who may capture
a `Player`, and what each error means.

`media-api`, version `v1alpha1`, described in OpenAPI 3.1.0.

media-api serves the HTTP routes for a Player. Its screen routes redirect to display-api, its audio routes redirect to audio-api, and its media routes compose the two into one muxed stream.

## `GET` `/v1/media` {data-method=GET}

The routes this API serves, as RFC 6570 templates.

**Answers**

| Status | Description |
| --- | --- |
| 200 | The discovery document, keyed by plural, with one entry per aspect. |
| 304 | The document matches the If-None-Match field. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 405 | A method other than GET, HEAD, or OPTIONS. |

## `HEAD` `/v1/media` {data-method=HEAD}

The routes this API serves, as RFC 6570 templates. The headers alone, with no body, no capture, and no upstream call.

**Answers**

| Status | Description |
| --- | --- |
| 200 | The discovery document, keyed by plural, with one entry per aspect. |
| 304 | The document matches the If-None-Match field. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 405 | A method other than GET, HEAD, or OPTIONS. |

## `OPTIONS` `/v1/media` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/namespaces/{namespace}/players/{name}` {data-method=GET}

The Player's Display, its Sinks, whether a Play runs, and the stream count.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 200 | The Player's capture facts, with a related link to every capture route. |
| 304 | The document matches the If-None-Match field. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name. |
| 405 | A method other than GET, HEAD, or OPTIONS. |

## `HEAD` `/v1/media/namespaces/{namespace}/players/{name}` {data-method=HEAD}

The Player's Display, its Sinks, whether a Play runs, and the stream count. The headers alone, with no body, no capture, and no upstream call.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 200 | The Player's capture facts, with a related link to every capture route. |
| 304 | The document matches the If-None-Match field. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name. |
| 405 | A method other than GET, HEAD, or OPTIONS. |

## `OPTIONS` `/v1/media/namespaces/{namespace}/players/{name}` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/namespaces/{namespace}/players/{name}/audio` {data-method=GET}

The Player's sound, in the format chosen by Accept.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The audio-api route for this Player's Sink. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | No Play runs on this Player, or it resolves no Sink. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `HEAD` `/v1/media/namespaces/{namespace}/players/{name}/audio` {data-method=HEAD}

The Player's sound, in the format chosen by Accept. The headers alone, with no body, no capture, and no upstream call.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The audio-api route for this Player's Sink. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | No Play runs on this Player, or it resolves no Sink. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `OPTIONS` `/v1/media/namespaces/{namespace}/players/{name}/audio` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/namespaces/{namespace}/players/{name}/audio.flac` {data-method=GET}

The Player's sound, as audio/flac.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The audio-api route for this Player's Sink. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | No Play runs on this Player, or it resolves no Sink. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `HEAD` `/v1/media/namespaces/{namespace}/players/{name}/audio.flac` {data-method=HEAD}

The Player's sound, as audio/flac. The headers alone, with no body, no capture, and no upstream call.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The audio-api route for this Player's Sink. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | No Play runs on this Player, or it resolves no Sink. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `OPTIONS` `/v1/media/namespaces/{namespace}/players/{name}/audio.flac` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/namespaces/{namespace}/players/{name}/audio.opus` {data-method=GET}

The Player's sound, as audio/ogg.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The audio-api route for this Player's Sink. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | No Play runs on this Player, or it resolves no Sink. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `HEAD` `/v1/media/namespaces/{namespace}/players/{name}/audio.opus` {data-method=HEAD}

The Player's sound, as audio/ogg. The headers alone, with no body, no capture, and no upstream call.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The audio-api route for this Player's Sink. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | No Play runs on this Player, or it resolves no Sink. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `OPTIONS` `/v1/media/namespaces/{namespace}/players/{name}/audio.opus` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/namespaces/{namespace}/players/{name}/audio.wav` {data-method=GET}

The Player's sound, as audio/wav.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The audio-api route for this Player's Sink. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | No Play runs on this Player, or it resolves no Sink. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `HEAD` `/v1/media/namespaces/{namespace}/players/{name}/audio.wav` {data-method=HEAD}

The Player's sound, as audio/wav. The headers alone, with no body, no capture, and no upstream call.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The audio-api route for this Player's Sink. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | No Play runs on this Player, or it resolves no Sink. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `OPTIONS` `/v1/media/namespaces/{namespace}/players/{name}/audio.wav` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/namespaces/{namespace}/players/{name}/media` {data-method=GET}

The Player's screen and sound in one stream, in the format chosen by Accept.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 200 | The composed stream: one video track where the Player has a screen, and one audio track per Sink in spec.sinks order. |
| 307 | The stream's route, returned when this Player resolves exactly one stream. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player resolves no stream at all. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `HEAD` `/v1/media/namespaces/{namespace}/players/{name}/media` {data-method=HEAD}

The Player's screen and sound in one stream, in the format chosen by Accept. The headers alone, with no body, no capture, and no upstream call.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 200 | The composed stream: one video track where the Player has a screen, and one audio track per Sink in spec.sinks order. |
| 307 | The stream's route, returned when this Player resolves exactly one stream. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player resolves no stream at all. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `OPTIONS` `/v1/media/namespaces/{namespace}/players/{name}/media` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/namespaces/{namespace}/players/{name}/media.mkv` {data-method=GET}

The Player's screen and sound in one stream, as video/matroska.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 200 | The composed stream: one video track where the Player has a screen, and one audio track per Sink in spec.sinks order. |
| 307 | The stream's route, returned when this Player resolves exactly one stream. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player resolves no stream at all. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `HEAD` `/v1/media/namespaces/{namespace}/players/{name}/media.mkv` {data-method=HEAD}

The Player's screen and sound in one stream, as video/matroska. The headers alone, with no body, no capture, and no upstream call.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 200 | The composed stream: one video track where the Player has a screen, and one audio track per Sink in spec.sinks order. |
| 307 | The stream's route, returned when this Player resolves exactly one stream. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player resolves no stream at all. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `OPTIONS` `/v1/media/namespaces/{namespace}/players/{name}/media.mkv` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/namespaces/{namespace}/players/{name}/media.mp4` {data-method=GET}

The Player's screen and sound in one stream, as video/mp4.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 200 | The composed stream: one video track where the Player has a screen, and one audio track per Sink in spec.sinks order. |
| 307 | The stream's route, returned when this Player resolves exactly one stream. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player resolves no stream at all. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `HEAD` `/v1/media/namespaces/{namespace}/players/{name}/media.mp4` {data-method=HEAD}

The Player's screen and sound in one stream, as video/mp4. The headers alone, with no body, no capture, and no upstream call.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |
| `bitrate` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 200 | The composed stream: one video track where the Player has a screen, and one audio track per Sink in spec.sinks order. |
| 307 | The stream's route, returned when this Player resolves exactly one stream. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player resolves no stream at all. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `OPTIONS` `/v1/media/namespaces/{namespace}/players/{name}/media.mp4` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/namespaces/{namespace}/players/{name}/screen` {data-method=GET}

The Player's screen, in the format chosen by Accept.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The display-api route for this Player's Display. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player has no status.screen. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `HEAD` `/v1/media/namespaces/{namespace}/players/{name}/screen` {data-method=HEAD}

The Player's screen, in the format chosen by Accept. The headers alone, with no body, no capture, and no upstream call.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The display-api route for this Player's Display. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player has no status.screen. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `OPTIONS` `/v1/media/namespaces/{namespace}/players/{name}/screen` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/namespaces/{namespace}/players/{name}/screen.jpg` {data-method=GET}

The Player's screen, as image/jpeg.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The display-api route for this Player's Display. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player has no status.screen. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `HEAD` `/v1/media/namespaces/{namespace}/players/{name}/screen.jpg` {data-method=HEAD}

The Player's screen, as image/jpeg. The headers alone, with no body, no capture, and no upstream call.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The display-api route for this Player's Display. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player has no status.screen. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `OPTIONS` `/v1/media/namespaces/{namespace}/players/{name}/screen.jpg` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/namespaces/{namespace}/players/{name}/screen.mjpeg` {data-method=GET}

The Player's screen, as multipart/x-mixed-replace.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The display-api route for this Player's Display. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player has no status.screen. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `HEAD` `/v1/media/namespaces/{namespace}/players/{name}/screen.mjpeg` {data-method=HEAD}

The Player's screen, as multipart/x-mixed-replace. The headers alone, with no body, no capture, and no upstream call.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The display-api route for this Player's Display. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player has no status.screen. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `OPTIONS` `/v1/media/namespaces/{namespace}/players/{name}/screen.mjpeg` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/namespaces/{namespace}/players/{name}/screen.mp4` {data-method=GET}

The Player's screen, as video/mp4.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The display-api route for this Player's Display. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player has no status.screen. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `HEAD` `/v1/media/namespaces/{namespace}/players/{name}/screen.mp4` {data-method=HEAD}

The Player's screen, as video/mp4. The headers alone, with no body, no capture, and no upstream call.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The display-api route for this Player's Display. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player has no status.screen. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `OPTIONS` `/v1/media/namespaces/{namespace}/players/{name}/screen.mp4` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/namespaces/{namespace}/players/{name}/screen.png` {data-method=GET}

The Player's screen, as image/png.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The display-api route for this Player's Display. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player has no status.screen. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `HEAD` `/v1/media/namespaces/{namespace}/players/{name}/screen.png` {data-method=HEAD}

The Player's screen, as image/png. The headers alone, with no body, no capture, and no upstream call.

**Parameters**

| Parameter | In | Required | Type | Description |
| --- | --- | --- | --- | --- |
| `namespace` | path | yes | string |  |
| `name` | path | yes | string |  |
| `t` | query | no | string |  |
| `xywh` | query | no | string |  |
| `width` | query | no | string |  |
| `height` | query | no | string |  |
| `framerate` | query | no | string |  |
| `quality` | query | no | string |  |

**Answers**

| Status | Description |
| --- | --- |
| 307 | The display-api route for this Player's Display. |
| 400 | A query the grammar refuses, or an upstream 400. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 404 | No Player of that name, or an upstream 404. |
| 405 | A method other than GET, HEAD, or OPTIONS. |
| 406 | Nothing this route serves is acceptable. |
| 409 | The Player has no status.screen. |
| 502 | An upstream answer this API cannot relay. |
| 503 | An upstream is busy, or this API is at its composition limit. |
| 504 | An upstream sent no headers within ten seconds plus the t= begin. |

## `OPTIONS` `/v1/media/namespaces/{namespace}/players/{name}/screen.png` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## `GET` `/v1/media/openapi.json` {data-method=GET}

This OpenAPI document.

**Answers**

| Status | Description |
| --- | --- |
| 200 | This document, with the servers entry naming the origin the request reached. |
| 304 | The document matches the If-None-Match field. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 405 | A method other than GET, HEAD, or OPTIONS. |

## `HEAD` `/v1/media/openapi.json` {data-method=HEAD}

This OpenAPI document. The headers alone, with no body, no capture, and no upstream call.

**Answers**

| Status | Description |
| --- | --- |
| 200 | This document, with the servers entry naming the origin the request reached. |
| 304 | The document matches the If-None-Match field. |
| 401 | No client certificate and no token, or a token the TokenReview refused. |
| 403 | The SubjectAccessReview denied the subject. |
| 405 | A method other than GET, HEAD, or OPTIONS. |

## `OPTIONS` `/v1/media/openapi.json` {data-method=OPTIONS}

The methods this route allows.

**Answers**

| Status | Description |
| --- | --- |
| 204 | Allow names GET, HEAD, and OPTIONS. |

## Security

Every route requires `mutualTLS`, or `bearer`, unless its own section states otherwise.

| Scheme | Type | Description |
| --- | --- | --- |
| `bearer` | HTTP bearer, JWT | A Kubernetes ServiceAccount token minted with the audience media-api, which this API checks with a TokenReview. |
| `mutualTLS` | Mutual TLS | A client certificate the cluster's own authority signed. The subject's common name is the user and its organization values are the groups, which is how the API server reads one. |

## The document itself

`media-api` serves the document this page is made from at
`/v1/media/openapi.json`. The repository holds the same copy at
`openapi.json`, and a test fails when the router and that copy
differ.
