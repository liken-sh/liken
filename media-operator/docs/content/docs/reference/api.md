---
title: The media API
weight: 65
toc: true
---

# The media API

`media-api` serves HTTPS routes for a `Player`, so you can see and
hear what is on the unit right now. It is one `Deployment` per
cluster, in `liken-system`, next to the operator. Its screen routes
redirect to `display-api` and its audio routes redirect to
`audio-api`, because those operators own the hardware. Its media
routes combine the two into one muxed stream, the screen with its
sound. No other API does that.

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
`Display`, a `Sink`, and a `Source`. The three APIs share the same
paths, the same HTTP behavior, and the same standards, but no code.

## Authentication

There are two ways to identify yourself: a client certificate or a
Bearer token. `media-api` checks for a certificate first, then for a
token, in the same order as the Kubernetes API server.

### Client certificate

If your TLS connection presents a client certificate signed by the
cluster's own certificate authority, you are that certificate's
subject. Your user name is the subject's common name, and your groups
are the subject's organization values. This is exactly how the
Kubernetes API server reads a client certificate, so the credentials
in your kubeconfig identify you here the same way they identify you
to `kubectl`. `media-api` reads the authority from the `ConfigMap`
`extension-apiserver-authentication` in `kube-system`, which is where
the API server publishes it, and reads it again every minute. A
rotated authority takes effect with no restart. A certificate from
any other authority ends the TLS handshake.

### Bearer token

If the connection has no client certificate, `media-api` looks for a
Bearer token. It validates the token with a `TokenReview` for the
audience `media-api` and checks `status.audiences`, so a pod's
default API server token does not work here. A token from an external
OIDC issuer must also have that audience.

### Authorization

After it knows who you are, `media-api` sends a
`SubjectAccessReview` for the verb `get` on `players/screen`,
`players/audio`, or `players/media` in the API group
`media.liken.sh`, with the `Player`'s namespace from the path. The
discovery and OpenAPI documents need authentication but no
authorization. The info route needs `get` on `players`.

### Grants

RBAC does not check that a subresource exists, so a cluster owner
grants capture with the shipped `ClusterRole` `media-capture-viewer`,
bound per namespace, or with one rule that uses `resourceNames`. The
role grants `get` on the three subresources and on `players`, so one
binding covers the info route and the captures together. The same
role binds to a person. Use `kind: User` with the common name from
their certificate, or `kind: Group` with one of its organization
values.

`players/media` on a `Player` is in effect a grant on that `Player`'s
`Display` and `Sink`s. A role that grants `resources: ["*"]` in
`media.liken.sh` includes it, and so does `cluster-admin`.

A redirect carries no credentials. You follow the 307 with your own
credentials, and `display-api` checks `displays/screen` for your
subject. So to capture a `Player`'s screen or audio, you also need
the grant on its `Display` and its `Sink`. The composed route is the
exception: `media-api` calls the sibling APIs under its own
`ServiceAccount`, and you only need `players/media`.

With a token, you mint a second one for the sibling's audience.
`curl -L` drops `Authorization` when the host changes, and
`--location-trusted` keeps it, but neither one works here on its own:
the sibling API refuses a token minted for the `media-api` audience.
Read the `Location` header and repeat the request by hand with a
token for the sibling's audience.

A client certificate works at the sibling API with no second
credential. The three APIs read the same authority, so the same
`--cert` and `--key` identify the same subject at `display-api` and
`audio-api`. Read the `Location` header and repeat the request there.
Recent `curl` releases send no client certificate on the second TLS
handshake when `-L` changes the host.

Every request that produces bytes writes a `Captured` `Event` on the
`Player`. The message names the subject and the aspect, so `kubectl
describe player` tells you who looked and when.

## Routes

Every path has the form
`/v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}]`.
`domain` is the first label of the CRD's API group, so
`media.liken.sh` gives `media`. A namespaced kind has
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
OpenAPI 3.1 document lists each extension path as its own path item,
with no `{.ext}`. `media-api` fills `servers: [{url: ...}]` from the
configured public base URL, or else from the origin of the request
that fetched the document. So `servers` names the host you reached,
and it is the one member that changes with the caller. Its media
type, `application/openapi+json`, is provisional:
`draft-ietf-httpapi-rest-api-mediatypes` registers it, and IANA does
not list it yet.

## Query parameters

`media-api` forwards these parameters to the API that owns the
capture, on a redirect and on a composition alike.

| Parameter | Applies to | Values | Default | Rejected with 400 when |
| --- | --- | --- | --- | --- |
| `t` | `screen`, `audio`, `media` | Media Fragments NPT: `t=begin,end`, `t=begin`, or `t=,end`, half-open | none | `t=a,b` with `a >= b`; a `begin` over the 60 s limit |
| `xywh` | `screen`, `media` | `pixel:` (the default) or `percent:`, then `x,y,w,h` | the whole screen | `display-api` rejects the region |
| `width` | `screen`, `media` | the width in pixels, as `display-api` defines it | set by `display-api` | `width=` together with `height=` |
| `height` | `screen`, `media` | the height in pixels, as `display-api` defines it | set by `display-api` | `height=` together with `width=` |
| `framerate` | `screen`, `media` | frames per second, as `display-api` defines it | 15 | `display-api` rejects the value |
| `quality` | `screen`, `media` | the JPEG quality, as `display-api` defines it | set by `display-api` | `display-api` rejects the value |
| `bitrate` | `audio`, `media` | the Opus bitrate, as `audio-api` defines it | set by `audio-api` | `audio-api` rejects the value |

A repeated dimension and an unknown key are 400 as well. A 400 that
an upstream raised carries that API's own `detail` and an `upstream`
member.

`t=` and `xywh=` follow W3C Media Fragments 1.0. The parser follows
section 5.1.1: it splits on `&` and `=` first and percent-decodes
second, so `t=10%2C20`, `t=%6ept:10`, and `t=npt%3a10` all parse, as
section 6.1.1 requires. `t=begin,end` is half-open: "the begin time
is considered part of the interval whereas the end time is considered
to be the first time point that is not part of the interval". A
region that runs off the edge is clipped, per section 6.1.2, by the
display capture container that owns the frame.

Media Fragments tells a user agent to ignore an invalid, unknown, or
non-existent dimension (sections 6.2, 6.2.1, 6.3.1). This API returns
400 instead. A query produces a new resource (sections 3.1 and 7.4),
so a client that asked for a region must not silently get the whole
screen.

Media Fragments puts NPT's zero at the start of the source media
(section 6.1.1). A live tap has no start, so this API puts the zero
at the instant the capture container accepts the request. The
`clock:` format is in the advanced Media Fragments document, not in
version 1.0. The capture container sends the headers at once, starts
its pipeline at once, and discards frames or samples until `begin` on
its own clock. So `t=5,7` discards five seconds and records two.

## Content negotiation

An extension names one fixed representation. With no extension, the
API negotiates on `Accept` with q-values. No `Accept` header means the
aspect's default, which is `video/mp4` for `media`. The negotiated
route returns `Content-Location` as an absolute path,
`/v1/media/namespaces/{ns}/players/{name}/media.mp4`. A redirect
includes the extension that the negotiation chose, so the sibling API
does not negotiate again. `video/x-matroska` is the deprecated alias
of `video/matroska` and also matches.

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
| `Link` | `<https://media.liken.sh/docs/reference/api/>; rel="service-doc"` | RFC 8631 |
| `Link` | on a `Player` route: `<https://kubernetes.default.svc/apis/media.liken.sh/v1alpha1/namespaces/{ns}/players/{name}>; rel="describedby"` | RFC 8288 |

RFC 9110 section 12.5.5 gives `Vary` a second purpose: it tells a
cache which request headers the server reads, whether or not they
changed the response.

A document route (discovery, OpenAPI, or the info route) has an
`ETag` and `Cache-Control: no-cache`, and a 304 carries the `ETag`
and `Vary: Accept`. The info document adds one `Link` `related` per
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

A redirect links to the composed form as `related`, because the
screen with its sound muxed in is a different aspect of the `Player`,
not a substitute for the screen alone. The two extension relations
are not registered. Registration needs a specification and expert
review, and a URI already identifies the relation. `describedby` is
absolute because a relative reference would resolve against
`media-api`, which does not serve the `Player`.

## Errors

Every error body is an RFC 9457 problem document,
`application/problem+json`, with five members. `type` is a URI that
names the kind of problem. `title` is a short phrase for it. `status`
repeats the HTTP status. `detail` says what went wrong in this
request, in the source's own words: a rejected query names the part
that was rejected, and a relayed upstream problem has the sibling's
own `detail`. `instance` is the request path, then `#`, then the
request id, which the API's log line also has.

| Status | When | Extra header or member |
| --- | --- | --- |
| 400 | a query the grammar rejects, or an upstream 400 | an `upstream` member when relayed |
| 401 | no client certificate and no token | `WWW-Authenticate: Bearer realm="media-api"` |
| 401 | the `TokenReview` refuses the token | `WWW-Authenticate: Bearer realm="media-api", error="invalid_token", error_description="<the TokenReview's words>"` |
| 403 | the `SubjectAccessReview` denies | `WWW-Authenticate: Bearer realm="media-api", error="insufficient_scope", scope="players/media"` |
| 404 | no such `Player`, or an upstream 404 | an `upstream` member when relayed |
| 405 | a method other than GET, HEAD, or OPTIONS | `Allow` |
| 406 | `Accept` excludes the extension's type, or nothing offered is acceptable | an `acceptable` member |
| 409 | no `status.screen`, or no running `Play` for an audio aspect | a `detail` that says what to do |
| 502 | an upstream answer that is not HTTP or not a problem document | type `upstream-failed` |
| 503 | an upstream 503, a refused connection, or this API's own composition limit | `Retry-After: 5`, or the upstream's own value relayed |
| 504 | upstream headers take more than 10 s plus `begin` | |

A 406's `acceptable` member is a list of `{"type", "href"}` pairs. On
an extension route it lists the route's own type and its siblings
(RFC 9110 section 15.5.7). The extension members `upstream` and
`acceptable` are allowed by RFC 9457 section 3.2, and a client
ignores members it does not know. With `about:blank`, `title` is the
status phrase (section 4.2.1).

The problem types are shared with the display and audio APIs.

| Type URI | Meaning | Status |
| --- | --- | --- |
| `https://liken.sh/problems/no-node` | the `Player` has no `status.screen` | 409 |
| `https://liken.sh/problems/not-playing` | no `Play` is running for the aspect you asked for | 409 |
| `https://liken.sh/problems/not-acceptable` | nothing the route serves is acceptable | 406 |
| `https://liken.sh/problems/capture-busy` | an upstream capture is busy, or this API is composing all it can at once | 503 |
| `https://liken.sh/problems/upstream-failed` | an upstream answered something this API cannot relay | 502 |
| `https://liken.sh/problems/away` | a sibling API reports the object away. A client meets it after a redirect | 409 |

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

The `screen` and `audio` entries are copied from the sibling APIs'
documents at request time.

## Composition

`media.mp4` is H.264 in fragmented MP4 with Opus audio. The container
follows "Encapsulation of Opus in ISO Base Media File Format",
version 0.6.8 of 28 April 2016: an `OpusSampleEntry` with the coding
name `Opus` and a `dOps` box, which ffmpeg's `movenc.c` writes. That
document has no `codecs` section. The string comes from RFC 6381
section 3.3, where the first element is the sample entry's
four-character code and "values are case sensitive":
`video/mp4; codecs="avc1.64001f,Opus"`. The `avc1` part is copied
from the display upstream. Browsers use lowercase `opus`, from the
MSE byte stream convention.

MDN's audio codec guide says "Safari supports Opus in the `<audio>`
element only when packaged in a CAF file", and caniuse.com marks
Safari as partial through version 27. Chrome, Firefox, mpv, and VLC
play it. AAC is not in v1: `audio-api` does not serve it, and an
encode on the API node would break the rule that the public API never
encodes.

`media.mkv` is the same streams through the `matroska` muxer with the
`live` option, "Write files assuming it is a live stream", served as
`video/matroska`. WebM is not offered, because it allows only VP8,
VP9, and AV1 video.

Each capture container's `t=` zero is the instant it accepts the
request, so the two streams' timestamp 0 are two different instants.
To line them up, `media-api` composes with a lead-in of 1 s. It asks
both upstreams for `t=begin+1,end+1`, so the composed stream is one
second behind "now". The discovery document reports this as
`"leadIn": "1s"`. Both capture containers then have a running
pipeline before their zero, and the correction is the difference
between the two accept instants. `media-api` measures those as the
instants the headers arrive on its own clock: it opens both requests
at once, records each instant, and starts ffmpeg with `-itsoffset` on
the audio input equal to `headersAt[audio] - headersAt[video]`.

The first byte of a composition arrives after the lead-in plus the
slower sibling's first keyframe. `media-api` holds nothing back: it
opens both upstreams at once and writes what the muxer writes. The
muxer writes nothing until it has a keyframe from the video and a
packet from each sink. A screen capture at the default 15 frames per
second has a keyframe every second, so a second of lead-in and a
second of keyframe wait are the floor, and the muxer's first fragment
adds the rest. In our tests, `media.mp4?t=0,10` took 4.09 to 4.34 s
to first byte over ten runs, and `media.mkv` took 4.18 s, with
upstream headers arriving at 1.12 to 1.38 s. Those numbers are from
before `display-api` sent its headers at the accept instant, so they
are a ceiling, not the current cost.

## Examples

Four commands capture ten seconds of a `Player`. The first mints a
token for the `media-api` audience under a `ServiceAccount` that has
the grant. The second reads the API's CA certificate from its
`ConfigMap`, so `curl` can verify the server. The third opens a
port-forward to the `Service`. The fourth captures the composed
stream from the first second to the tenth and saves it.

```sh
TOKEN=$(kubectl create token media-api-client -n media --audience media-api --duration 10m)
kubectl get configmap media-api-ca -n liken-system -o jsonpath='{.data.ca\.crt}' > media-api-ca.crt
kubectl port-forward -n liken-system svc/media-api 8443:443 &
curl --cacert media-api-ca.crt -H "Authorization: Bearer $TOKEN" \
  --resolve media-api.liken-system.svc:8443:127.0.0.1 \
  "https://media-api.liken-system.svc:8443/v1/media/namespaces/media/players/studio/media.mp4?t=0,10" \
  -o studio.mp4
```

The composed route works through one port-forward, because
`media-api` fetches the upstream streams itself. A redirect does not.
Its `Location` names another `Service`, so you open a second
port-forward to that `Service` and repeat the request there, with the
credentials [Authentication](#grants) describes.

If your kubeconfig has a client certificate, send that instead. You
need no `ServiceAccount` and no token. The port-forward is a TCP
tunnel, so the TLS handshake runs end to end and the certificate
reaches the API unchanged.

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

From a pod on the cluster network, the same two files work against
`https://media-api.liken-system.svc/v1/media/...` with no
port-forward and no `--resolve`. Through the port-forward above, the
same `curl` with `/v1/media/openapi.json` in place of the capture
path fetches the OpenAPI document, with the same token and CA.

One composed request and its answer:

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
Link: <https://media.liken.sh/docs/reference/api/>; rel="service-doc"
```

The `Link` headers are wrapped here for reading. Each is one header
line on the wire. The upstream `t=` is the caller's `t=` plus the
lead-in. The same request for `screen.png` returns 307 with an
absolute
`Location: https://display-api.liken-system.svc/v1/display/displays/boe-1080/screen.png`,
because the client reaches that server with its own credentials, and
a relative `Link: </v1/media/namespaces/media/players/studio/media.mp4>;
rel="related"; type="video/mp4"`. A busy upstream returns 503 with
its `Retry-After` relayed and a `capture-busy` problem document whose
`detail` is the upstream's own and whose `upstream` member is its
URL.

## Notes

**The info route.** It returns the `Display` name and node, each
`Sink` name, whether a `Play` is running, and the number of streams,
with `related` links to every capture route. So a client can find out
first whether `media.mp4` will compose a stream or answer 409.

**Health and metrics.** `/healthz` and `/readyz` answer 200 or 503 as
`text/plain`. `/metrics` on port 9200 answers 200 as
`text/plain; version=0.0.4` for Prometheus.

**A failure after the first byte.** A failure after the stream began
ends the response. The client sees a truncated body, and the
request's log line has the fault.
