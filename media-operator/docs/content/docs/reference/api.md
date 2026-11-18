---
title: The media API
weight: 65
toc: true
---

# The media API

`media-api` lets you capture what a `Player` shows and plays right
now, over HTTPS. The [record guide](/docs/guides/record/) uses it.
It runs as one `Deployment` per cluster, in `liken-system`. Its
screen routes redirect to `display-api` and its audio routes redirect
to `audio-api`, because those operators own the hardware. Its media
routes fetch both and mux them into one stream, the screen with its
sound.

**At a glance**

| Item | Value |
| --- | --- |
| Service | `https://media-api.liken-system.svc` |
| CA `ConfigMap` | `media-api-ca` in `liken-system` |
| Discovery | `/v1/media` |
| OpenAPI | `/v1/media/openapi.json` |
| Shipped `ClusterRole` | `media-capture-viewer` |
| Audit record | a `Captured` `Event` on the `Player` |
| Route reference | [Routes](/docs/reference/routes/) |

The display and audio operators have the same kind of API for a
`Display`, a `Sink`, and a `Source`, with the same path layout and
the same HTTP behavior.

## Authentication

You identify yourself with a client certificate or a Bearer token.
`media-api` checks for a certificate first and then for a token, the
same order that the Kubernetes API server uses.

### Client certificate

If your TLS connection presents a client certificate signed by the
cluster's own certificate authority, you are that certificate's
subject. Your user name is the subject's common name, and your groups
are the subject's organization values. The Kubernetes API server
reads a client certificate the same way, so the credentials in your
kubeconfig identify you here as they do to `kubectl`. `media-api`
reads the authority from the `ConfigMap`
`extension-apiserver-authentication` in `kube-system`, where the API
server publishes it. It watches that `ConfigMap`, so a rotated
authority takes effect with no restart. The TLS handshake fails for a
certificate from any other authority.

### Bearer token

If the connection has no client certificate, `media-api` reads a
Bearer token from the `Authorization` header. It validates the token
with a `TokenReview` and accepts it only when `status.audiences`
includes `media-api`. So a pod's default token for the API server is
refused, and a token from an external OIDC issuer must also have the
audience `media-api`.

### Authorization

After it identifies you, `media-api` creates a
`SubjectAccessReview` for the verb `get` on `players/screen`,
`players/audio`, or `players/media` in the API group
`media.liken.sh`, in the `Player`'s namespace from the path. The
discovery and OpenAPI documents require authentication and no
authorization. The info route requires `get` on `players`.

### Grants

The API server serves no `players/screen`, `players/audio`, or
`players/media` subresource, but RBAC accepts a rule for a
subresource without checking that it exists. So you grant capture in
an ordinary RBAC rule. Bind the shipped `ClusterRole`
`media-capture-viewer` in each namespace, or write your own rule with
`resourceNames` to grant only some `Player`s. The role grants `get`
on the three subresources and on `players`, so one binding covers
the info route and the captures. To bind it to a person, use
`kind: User` with the common name from their certificate, or
`kind: Group` with one of its organization values. A role that
grants `resources: ["*"]` in `media.liken.sh` also grants capture,
and so does `cluster-admin`.

The composed `media` route needs only `players/media`, because
`media-api` fetches the screen and the sound from the sibling APIs
with its own `ServiceAccount`. A redirect is different. You follow
the 307 to the sibling API with your own credentials, and that API
makes its own check, such as `get` on `displays/screen` for a
`Display`. So to capture through a redirect, you also need a grant on
the `Player`'s `Display` or `Sink`.

With a token, you also need a second token, minted for the sibling
API's audience, because the sibling API refuses a token for the
`media-api` audience. So `curl -L` does not work, whether it drops
`Authorization` on the new host (the default) or keeps it
(`--location-trusted`). Read the `Location` header and send the
request there yourself, with the second token.

With a client certificate, you need no second credential, because
the three APIs trust the same authority. You still read the
`Location` header and send the request there yourself, because
recent `curl` releases send no client certificate on the second TLS
handshake when `-L` changes the host.

Every request that returns bytes creates a `Captured` `Event` on the
`Player`, with the subject and the aspect in its message, so
`kubectl describe player` shows who captured what, and when. When
the same subject repeats the same capture within ten minutes, the
API increases the count of the existing `Event` and creates no new
one. The API's log has one line for each request.

## Routes

Every path has the form
`/v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}]`.
`domain` is the first label of the CRD's API group, so
`media.liken.sh` becomes `media`. A namespaced kind has
`namespaces/{ns}` before its plural, the same as the Kubernetes API.
In the table, `.../` stands for
`/v1/media/namespaces/{ns}/players/{name}/`.

| Method | Path | Response | Notes |
| --- | --- | --- | --- |
| GET, HEAD | `/v1/media` | 200 `application/json` | The discovery document |
| GET, HEAD | `/v1/media/openapi.json` | 200 `application/openapi+json` | OpenAPI 3.1 |
| GET, HEAD | `/v1/media/namespaces/{ns}/players/{name}` | 200 `application/json` | The `Player`'s capture facts |
| GET, HEAD | a document route, with a matching `If-None-Match` | 304, no body | Not modified |
| GET, HEAD | `.../screen[.ext]` | 307 | Redirect to the `Display` route on `display-api` |
| GET, HEAD | `.../audio[.ext]` | 307 | Redirect to the `Sink` route on `audio-api` |
| GET, HEAD | `.../media` | 200, negotiated | The composed stream. Default `video/mp4` |
| GET, HEAD | `.../media.mp4` | 200 `video/mp4; codecs="avc1.64001f,Opus"` | The composed stream |
| GET, HEAD | `.../media.mkv` | 200 `video/matroska` | The composed stream |
| OPTIONS | any of the above | 204, no body | `Allow: GET, HEAD, OPTIONS` |

The [Routes](/docs/reference/routes/) page lists every route from the
OpenAPI document, with its parameters, responses, and fields. The
OpenAPI document lists each extension path as its own path item,
with no `{.ext}`. `media-api` sets its `servers` entry to the value of
`MEDIA_API_PUBLIC_BASE` when that variable is set, and otherwise to
the origin of the request that fetched the document. `servers` is
the only member of the document that differs between callers.
The media type `application/openapi+json` is provisional. The draft
`draft-ietf-httpapi-rest-api-mediatypes` defines it, and IANA has not
registered it yet.

## Query parameters

`media-api` passes these parameters to the API that captures the
screen or the sound, both in a redirect and in a composed stream.

| Parameter | Applies to | Values | Default | Rejected with 400 when |
| --- | --- | --- | --- | --- |
| `t` | `screen`, `audio`, `media` | Media Fragments NPT: `t=begin,end`, `t=begin`, or `t=,end`, half-open | none | `t=a,b` with `a >= b`; a `begin` over the 60 s limit |
| `xywh` | `screen`, `media` | `pixel:` (the default) or `percent:`, then `x,y,w,h` | the whole screen | `display-api` rejects the region |
| `width` | `screen`, `media` | the width in pixels, as `display-api` defines it | set by `display-api` | `width=` together with `height=` |
| `height` | `screen`, `media` | the height in pixels, as `display-api` defines it | set by `display-api` | `height=` together with `width=` |
| `framerate` | `screen`, `media` | frames per second, as `display-api` defines it | 15 | `display-api` rejects the value |
| `quality` | `screen`, `media` | the JPEG quality, as `display-api` defines it | set by `display-api` | `display-api` rejects the value |
| `bitrate` | `audio`, `media` | the Opus bitrate, as `audio-api` defines it | set by `audio-api` | `audio-api` rejects the value |

A repeated dimension and an unknown key also return 400. A 400 from
an upstream API keeps that API's `detail` and adds an `upstream`
member.

`t=` and `xywh=` follow W3C Media Fragments 1.0. The parser splits on
`&` and `=` first and percent-decodes second (section 5.1.1), so
`t=10%2C20`, `t=%6ept:10`, and `t=npt%3a10` all parse, as section
6.1.1 requires. `t=` accepts normal play time only, with or without
the `npt:` prefix, in the form `ss`, `mm:ss`, or `hh:mm:ss`. The
interval includes `begin` and excludes `end`. The display capture
container clips a region that runs past the edge of the screen, as
section 6.1.2 describes.

Media Fragments tells a user agent to ignore an invalid, unknown, or
non-existent dimension (sections 6.2, 6.2.1, 6.3.1). This API returns
400 instead, because the query asks for a different resource
(sections 3.1 and 7.4), and a client that asked for a region must not
get the whole screen without being told.

Media Fragments puts the zero of normal play time at the start of
the source media (section 6.1.1). A live capture has no start, so
the zero is the instant the capture container accepts the request.
The container sends the headers and starts its pipeline at once, and
discards frames or samples until `begin` on its own clock. So `t=5,7`
discards five seconds and records two.

## Content negotiation

An extension selects one fixed representation. With no extension,
the API chooses the representation from the `Accept` header and its
q-values. With no `Accept` header, it uses the aspect's default,
which is `video/mp4` for `media`. The negotiated route returns
`Content-Location` as an absolute path, for example
`/v1/media/namespaces/{ns}/players/{name}/media.mp4`. The `Location`
of a redirect includes the extension that the negotiation chose, so
the sibling API does not negotiate again. `Accept: video/x-matroska`,
the deprecated alias of `video/matroska`, also selects Matroska.

| Request | Response |
| --- | --- |
| `GET .../media`, no `Accept` | 200 `video/mp4`, `Content-Location: .../media.mp4` |
| `GET .../media`, `Accept: video/matroska` | 200 `video/matroska`, `Content-Location: .../media.mkv` |
| `GET .../media`, `Accept: video/*;q=0.5, video/matroska` | 200 `video/matroska` |
| `GET .../media.mp4`, `Accept: image/png` | 406, `acceptable: [{type: video/mp4, href: .../media.mp4}, {type: video/matroska, href: .../media.mkv}]` |
| `GET .../screen`, `Accept: image/jpeg` | 307 to `/v1/display/displays/{d}/screen.jpg` |
| `GET .../screen`, no `Accept` | 307 to `/v1/display/displays/{d}/screen.png` |
| `GET .../audio`, `Accept: audio/ogg` | 307 to `/v1/audio/sinks/{s}/audio.opus` |

## Response headers

Every response has these headers:

| Header | Value | Standard |
| --- | --- | --- |
| `Content-Type` | the media type of the body | RFC 9110 |
| `Vary` | `Accept`, on every response, extension routes included | RFC 9110 section 12.5.5 |
| `Link` | `</v1/media/openapi.json>; rel="service-desc"` | RFC 8631 |
| `Link` | `<https://liken.sh/media/docs/reference/api/>; rel="service-doc"` | RFC 8631 |
| `Link` | on a `Player` route: `<https://kubernetes.default.svc/apis/media.liken.sh/v1alpha1/namespaces/{ns}/players/{name}>; rel="describedby"` | RFC 8288 |

An extension route also sends `Vary: Accept`, because it reads
`Accept` to decide on a 406, and RFC 9110 section 12.5.5 uses `Vary`
for every request header that the server reads.

A document route (discovery, OpenAPI, or the info route) has an
`ETag` and `Cache-Control: no-cache`, and a 304 has the `ETag` and
`Vary: Accept`. The info document adds one `Link` `related` for each
capture route. A document route has none of the capture headers: no
`Content-Disposition`, no `Accept-Ranges`, and no chunked body.

A composed response has these headers instead:

| Header | Value | Standard |
| --- | --- | --- |
| `Cache-Control` | `no-store` | RFC 9111 section 5.2.2.5 |
| `Content-Disposition` | `inline`, with a file name such as `media-studio-2026-09-16T21-02-16Z.mp4` | RFC 6266 |
| `Accept-Ranges` | `none` | RFC 9110 section 14.3 |
| `Transfer-Encoding` | `chunked`, on HTTP/1.1 | RFC 9112 section 7.1 |
| `Content-Location` | on `media` only: the absolute path of the extension route that was served | RFC 9110 section 8.7 |
| `Link` | `rel="https://liken.sh/rel/screen"`, and one `rel="https://liken.sh/rel/audio"` per track | RFC 8288 |
| `Link` | on `media` only: `rel="alternate"` to `media.mp4` and to `media.mkv` | RFC 8288 |

A 307 has `Location`, `Cache-Control: no-store`, and a `Link`
`related` to `media.mp4`. A `HEAD` returns the `GET`'s status and
headers. It calls no upstream, and its `Content-Type` has no `codecs`
parameter. A `HEAD` on an error has no body (RFC 9110 section 15.5).

| Relation | Registered | On | Points to |
| --- | --- | --- | --- |
| `alternate` | IANA, from HTML: "Refers to a substitute for this context" | the `media` route with no extension | `media.mp4` and `media.mkv`, each with a bare `type` |
| `related` | IANA, RFC 4287: "Identifies a related resource" | a `screen.*` or `audio.*` 307, and the info document | the composed `media.mp4` with the same `t=`; each capture route |
| `describedby` | IANA, from W3C POWDER | every `Player` route | the `Player` object on the API server |
| `service-desc`, `service-doc` | IANA, RFC 8631 | every response | the OpenAPI document; this page |
| `https://liken.sh/rel/screen` | an extension relation, a URI per RFC 8288 section 2.1.2 | a composed response | the `display-api` route the video came from |
| `https://liken.sh/rel/audio` | an extension relation | a composed response, once per audio track | the `audio-api` route each track came from |

A redirect links to the composed stream as `related`, not
`alternate`, because the screen with its sound is a different
aspect of the `Player`, not a substitute for the screen alone. The
`https://liken.sh/rel/` relations are URIs, so they need no IANA
registration. `describedby` is an absolute URL because a relative one
would resolve against `media-api`, which does not serve the `Player`
object.

## Errors

Every error body is an RFC 9457 problem document,
`application/problem+json`, with five members. `type` is a URI that
identifies the kind of problem. `title` is a short phrase for it.
`status` repeats the HTTP status. `detail` describes what went wrong
in this request: for a rejected query it names the rejected part, and
for a relayed upstream problem it is the sibling API's own `detail`.
`instance` is the request path, then `#`, then the request id, which
is also in the API's log line for the request.

| Status | When | Extra header or member |
| --- | --- | --- |
| 400 | a query the grammar rejects, or an upstream 400 | an `upstream` member when relayed |
| 401 | no client certificate and no token | `WWW-Authenticate: Bearer realm="media-api"` |
| 401 | the `TokenReview` refuses the token | `WWW-Authenticate: Bearer realm="media-api", error="invalid_token", error_description="<the TokenReview's words>"` |
| 403 | the `SubjectAccessReview` denies | `WWW-Authenticate: Bearer realm="media-api", error="insufficient_scope", scope="players/media"` |
| 404 | no such `Player`, or an upstream 404 | an `upstream` member when relayed |
| 405 | a method other than GET, HEAD, or OPTIONS | `Allow` |
| 406 | `Accept` excludes the extension's type, or nothing offered is acceptable | an `acceptable` member |
| 409 | no `status.screen`, no running `Play` for an audio aspect, or an upstream 409 on a composed stream | a `detail` that says what to do, and an `upstream` member when relayed |
| 502 | an upstream response that is not HTTP or not a problem document | type `upstream-failed` |
| 503 | an upstream 503, a refused connection, or more composed streams at once than `MEDIA_API_MAX_COMPOSITIONS` allows (default 4) | `Retry-After: 5`, or the upstream's own value relayed |
| 504 | upstream headers take more than 10 s plus `begin` | |

A 406's `acceptable` member is a list of `{"type", "href"}` pairs. On
an extension route it lists the route's own type and the types of the
other extensions (RFC 9110 section 15.5.7). RFC 9457 section 3.2
allows the extension members `upstream` and `acceptable`, and a
client ignores members it does not know. When `type` is `about:blank`,
`title` is the status phrase (section 4.2.1).

The display and audio APIs use the same problem types.

| Type URI | Meaning | Status |
| --- | --- | --- |
| `https://liken.sh/problems/no-node` | the `Player` has no `status.screen` | 409 |
| `https://liken.sh/problems/not-playing` | no `Play` is running for the aspect you asked for | 409 |
| `https://liken.sh/problems/not-acceptable` | nothing the route serves is acceptable | 406 |
| `https://liken.sh/problems/capture-busy` | an upstream capture is busy, or this API is already running its limit of compositions | 503 |
| `https://liken.sh/problems/upstream-failed` | an upstream sent a response that this API cannot relay | 502 |
| `https://liken.sh/problems/away` | a sibling API reports that the object is not there, for example a `Display` with no panel on its connector. After a redirect, the client receives the sibling's own problem. On a composed stream, it receives this type with the sibling's `detail` | 409 |

## Discovery

`GET /v1/media` returns the shared document shape, keyed by plural
name:

```json
{
  "resources": {
    "players": {
      "self": "/v1/media/namespaces/{namespace}/players/{name}",
      "aspects": {
        "screen": {"template": "/v1/media/namespaces/{namespace}/players/{name}/screen{.ext}{?t,xywh,width,height,framerate,quality}",
                   "mediaTypes": ["image/png", "image/jpeg", "video/mp4", "multipart/x-mixed-replace"],
                   "extensions": ["png", "jpg", "mp4", "mjpeg"], "redirects": true},
        "audio":  {"template": "/v1/media/namespaces/{namespace}/players/{name}/audio{.ext}{?t,bitrate}",
                   "mediaTypes": ["audio/wav", "audio/flac", "audio/ogg"],
                   "extensions": ["wav", "flac", "opus"], "redirects": true},
        "media":  {"template": "/v1/media/namespaces/{namespace}/players/{name}/media{.ext}{?t,xywh,width,height,framerate,quality,bitrate}",
                   "mediaTypes": ["video/mp4", "video/matroska"],
                   "extensions": ["mp4", "mkv"], "redirects": false, "leadIn": "1s"}
      }
    }
  },
  "openapi": "/v1/media/openapi.json"
}
```

`media-api` copies the `screen` and `audio` entries from the sibling
APIs' discovery documents when it receives the request.

## Composition

`media.mp4` is H.264 video and Opus audio in fragmented MP4, so a
client can play it while it arrives. `media-api` copies both streams
from the sibling APIs and encodes nothing, so there is no AAC
variant: `audio-api` does not serve AAC. The Opus track follows
"Encapsulation of Opus in ISO Base Media File Format", version 0.6.8.
That document defines no `codecs` value, so the `Content-Type` uses
the sample entry's four-character code, `Opus`, as RFC 6381 section
3.3 describes: `video/mp4; codecs="avc1.64001f,Opus"`. The `avc1`
value is the one that `display-api` sends. Browsers spell the Opus
codec `opus` in lowercase.

Chrome, Firefox, mpv, and VLC play `media.mp4`. MDN's audio codec
guide says "Safari supports Opus in the `<audio>` element only when
packaged in a CAF file", so Safari can fail to play the sound.

`media.mkv` has the same streams in Matroska, written by ffmpeg's
`matroska` muxer with its `live` option, and served as
`video/matroska`. WebM is not offered, because WebM allows only VP8,
VP9, and AV1 video.

Each capture container sets its `t=` zero at the instant it accepts
the request, so the screen and the sound start at two different
instants. To line them up, `media-api` asks both sibling APIs for
`t=begin+1,end+1`, one second later than you asked, and the discovery
document reports this lead-in as `"leadIn": "1s"`. So the composed
stream is one second behind the screen. `media-api` opens both
requests at once, records when the headers of each arrive, and
shifts each audio track by the time between its headers and the
video's.

`media-api` sends the muxer's output to the client as ffmpeg writes
it. ffmpeg writes nothing until it has a keyframe from the video and
a packet from each audio track, so the first byte arrives after the
lead-in and the first keyframe of the screen capture.

## Examples

These commands capture ten seconds of the `Player` `studio` in the
namespace `media`, with a `ServiceAccount` token. First create the
`ServiceAccount` and give it the grant:

```sh
kubectl create serviceaccount media-api-client -n media
kubectl create rolebinding media-api-client -n media \
  --clusterrole media-capture-viewer --serviceaccount media:media-api-client
```

Then create a token for the `media-api` audience, save the API's CA
certificate so `curl` can verify the server, forward a local port to
the `Service`, and request `t=0,10` of the composed stream:

```sh
TOKEN=$(kubectl create token media-api-client -n media --audience media-api --duration 10m)
kubectl get configmap media-api-ca -n liken-system -o jsonpath='{.data.ca\.crt}' > media-api-ca.crt
kubectl port-forward -n liken-system svc/media-api 8443:443 &
curl --cacert media-api-ca.crt -H "Authorization: Bearer $TOKEN" \
  --resolve media-api.liken-system.svc:8443:127.0.0.1 \
  "https://media-api.liken-system.svc:8443/v1/media/namespaces/media/players/studio/media.mp4?t=0,10" \
  -o studio.mp4
```

The composed route needs only this one port-forward, because
`media-api` fetches the screen and the sound itself. A redirect's
`Location` is another `Service`, so forward a second port to that
`Service` and send the request there, with the credentials that
[Grants](#grants) describes.

If your kubeconfig has a client certificate, you can send it in place
of the token, with no `ServiceAccount`. The port-forward is a TCP
tunnel, so the TLS handshake runs between `curl` and the API, and the
API receives your certificate.

```sh
kubectl config view --raw --minify \
  -o jsonpath='{.users[0].user.client-certificate-data}' | base64 -d > client.crt
kubectl config view --raw --minify \
  -o jsonpath='{.users[0].user.client-key-data}' | base64 -d > client.key
curl --cacert media-api-ca.crt --cert client.crt --key client.key \
  --resolve media-api.liken-system.svc:8443:127.0.0.1 \
  "https://media-api.liken-system.svc:8443/v1/media/namespaces/media/players/studio/media.mp4?t=0,10" \
  -o studio.mp4
```

From a pod on the cluster network, send the same requests to
`https://media-api.liken-system.svc/v1/media/...`, with no
port-forward and no `--resolve`. To fetch the OpenAPI document, use
the path `/v1/media/openapi.json` in place of the capture path.

One composed request and its response:

```
GET /v1/media/namespaces/media/players/studio/media.mp4?t=0,10&width=960 HTTP/1.1
Host: media-api.liken-system.svc
Authorization: Bearer eyJ...

HTTP/1.1 200 OK
Content-Type: video/mp4; codecs="avc1.64001f,Opus"
Content-Disposition: inline; filename="media-studio-2026-09-16T21-02-16Z.mp4"
Cache-Control: no-store
Vary: Accept
Accept-Ranges: none
Transfer-Encoding: chunked
Link: <https://display-api.liken-system.svc/v1/display/displays/boe-1080/screen.mp4?t=1,11&width=960>;
      rel="https://liken.sh/rel/screen"
Link: <https://audio-api.liken-system.svc/v1/audio/sinks/hdmi-0-pch/audio.opus?t=1,11>;
      rel="https://liken.sh/rel/audio"
Link: <https://kubernetes.default.svc/apis/media.liken.sh/v1alpha1/namespaces/media/players/studio>;
      rel="describedby"
Link: </v1/media/openapi.json>; rel="service-desc"
Link: <https://liken.sh/media/docs/reference/api/>; rel="service-doc"
```

The `Link` headers are wrapped here for reading. Each is one header
line on the wire. The upstream `t=1,11` is the request's `t=0,10`
plus the lead-in. The same request for `screen.png` returns 307 with
the absolute
`Location: https://display-api.liken-system.svc/v1/display/displays/boe-1080/screen.png`,
because the client connects to that server itself. It also has the
relative `Link: </v1/media/namespaces/media/players/studio/media.mp4>;
rel="related"; type="video/mp4"`. When an upstream capture is busy,
the response is 503 with the upstream's `Retry-After` and a
`capture-busy` problem document, whose `detail` is the upstream's own
`detail` and whose `upstream` member is the upstream's URL.

## Notes

**The info route.** It returns the `Display` name and node, the name
of each `Sink`, whether a `Play` is running, and the number of
streams, with `related` links to every capture route. Read it before
a capture to find out whether `media.mp4` will return a stream or a
409.

**Health and metrics.** `/healthz` and `/readyz` return 200 or 503 as
`text/plain`. `/metrics` on port 9200 returns 200 as
`text/plain; version=0.0.4` for Prometheus.

**A failure after the first byte.** The status line is already sent,
so a failure during the stream ends the response early. The client
receives a truncated body, and the request's log line records the
error.
