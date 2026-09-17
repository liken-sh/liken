---
title: The media API
weight: 65
toc: true
---

# The media API

`media-api` answers HTTPS for a `Player`: what is on the unit now.
It is one Deployment per cluster, in `liken-system`, beside the
operator. Its screen routes redirect to the display operator's
`display-api` and its audio routes redirect to the audio operator's
`audio-api`, because each of those owns its hardware. Its media
routes compose the two into one muxed stream, the screen with its
sound, which no other API answers.

## The everyday use

Four commands capture ten seconds of a `Player`. The first mints a
token for the `media-api` audience under a `ServiceAccount` that
holds the grant. The second reads the API's CA certificate out of its
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

The composed route works through one forward, because `media-api`
fetches the upstreams itself. A redirect does not: its `Location`
names another Service, so the caller opens a second forward to that
Service and repeats the request with a token whose audience is that
API.
`curl -L` drops `Authorization` when the host changes, and
`--location-trusted` keeps it. Neither follows a redirect on its own
here: the sibling refuses a token minted for the `media-api`
audience, so a caller repeats the request by hand with a token for
the sibling's audience.

## Grants

Every request carries a Bearer token. `media-api` validates it with a
`TokenReview` that names the audience `media-api` and checks
`status.audiences`, so a pod's default API-server token does not open
it. A token from an external OIDC issuer must carry that audience
too. It authorizes with a `SubjectAccessReview` for verb `get` on
`players/screen`, `players/audio`, or `players/media` in
`media.liken.sh`, with the `Player`'s namespace from the path.
Discovery and OpenAPI need authentication and no authorization. The
info route needs `get` on `players`.

RBAC does not check that a subresource exists, so an owner grants
capture with the shipped `ClusterRole` `media-capture-viewer` (`get`
on the three subresources and on `players`) bound per namespace, or
one rule with `resourceNames`. The role carries `players` as well as
the three aspects, so one binding covers the info route and the
captures together.

A redirect carries no credentials. The client follows the 307 with
its own token, and `display-api` checks `displays/screen` for that
subject, so a captor of a `Player` also needs the grant on the
`Display` and the `Sink`. The composed route is the exception:
`media-api` calls the siblings under its own ServiceAccount, and the
caller needs `players/media` alone.

`players/media` on a `Player` is a grant on that `Player`'s `Display`
and `Sink`s, and a role with `resources: ["*"]` in `media.liken.sh`,
and `cluster-admin`, gain capture on the day this ships.

## Routes

The shared grammar is
`/v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}]`.
`domain` is the first label of the CRD's API group, so
`media.liken.sh` gives `media`, and a namespaced kind carries
`namespaces/{ns}` before its plural, as the Kubernetes API does.
Discovery is at `/v1/media` and OpenAPI at `/v1/media/openapi.json`.
Below, `.../` stands for `/v1/media/namespaces/{ns}/players/{name}/`.

| Route | Answer | Status | Headers |
| --- | --- | --- | --- |
| `GET /v1/media` | discovery document | 200 | `application/json`, `ETag`, `Cache-Control: no-cache`, `Vary: Accept` |
| `GET /v1/media/openapi.json` | OpenAPI 3.1 | 200 | `application/openapi+json`, `ETag`, `Cache-Control: no-cache` |
| `GET /v1/media/namespaces/{ns}/players/{name}` | the Player's capture facts | 200 | `application/json`, `ETag`, `Cache-Control: no-cache`, `Link` `related` to each capture route |
| document route with a matching `If-None-Match` | not modified | 304 | `ETag`, `Vary: Accept` |
| `GET .../screen[.ext]` | redirect to the Display route | 307 | `Location`, `Link` (`related` to `media.mp4`), `Cache-Control: no-store`, `Vary: Accept` |
| `GET .../audio[.ext]` | redirect to the Sink route | 307 | as above |
| `GET .../media` | the composed stream, negotiated | 200 | as the next row, plus `Content-Location` and `Link` `alternate` to `media.mp4` and `media.mkv` |
| `GET .../media.mp4`, `.../media.mkv` | the composed stream | 200 | `Content-Type`, `Content-Disposition`, `Cache-Control: no-store`, `Vary: Accept`, `Accept-Ranges: none`, `Link` (`https://liken.sh/rel/screen`, one `https://liken.sh/rel/audio` per track), `Transfer-Encoding: chunked` on HTTP/1.1 |
| `HEAD` on any route | the `GET`'s headers | as the `GET` | no body, no upstream call, no `codecs` parameter |
| `OPTIONS` on any route | the methods | 204 | `Allow: GET, HEAD, OPTIONS` |
| any other method | refused | 405 | `Allow`, problem document |
| no token | refused | 401 | `WWW-Authenticate: Bearer realm="media-api"` |
| the TokenReview refuses the token | refused | 401 | `WWW-Authenticate: Bearer realm="media-api", error="invalid_token", error_description="<the TokenReview's words>"` |
| the SubjectAccessReview denies | refused | 403 | `WWW-Authenticate: Bearer realm="media-api", error="insufficient_scope", scope="players/media"` |
| no such Player, or an upstream 404 | refused | 404 | problem document, `upstream` member on a relayed one |
| no `status.screen`, or no running Play for an audio aspect | refused | 409 | problem document whose `detail` names the clearing action |
| a query the grammar refuses, or an upstream 400 | refused | 400 | problem document, `upstream` member on a relayed one |
| the `Accept` excludes the extension's type, or nothing offered is acceptable | refused | 406 | problem document with `acceptable` |
| an upstream 503, a refused connection, or this API's own composition limit | refused | 503 | `Retry-After: 5` (an upstream's value relayed), problem document |
| an upstream answer that is not HTTP or not a problem document | refused | 502 | problem document `upstream-failed` |
| upstream headers take over 10 s plus `begin` | refused | 504 | problem document |
| `/healthz`, `/readyz` | liveness, readiness | 200 or 503 | `text/plain` |
| `/metrics` on `:9200` | Prometheus | 200 | `text/plain; version=0.0.4` |

Every response carries `Link: </v1/media/openapi.json>;
rel="service-desc"` and `Link: <https://media.liken.sh/docs/reference/api/>;
rel="service-doc"` (RFC 8631), and every `Player` route carries
`Link: <https://kubernetes.default.svc/apis/media.liken.sh/v1alpha1/namespaces/{ns}/players/{name}>;
rel="describedby"`. Every response carries `Vary: Accept`, on the
extension routes too, because RFC 9110 section 12.5.5 gives the field
a second purpose: it tells a cache which request fields the server
reads, whether or not they changed the answer. A document route
carries none of the capture headers: no `Content-Disposition`, no
`Accept-Ranges`, and no chunked body. A `HEAD` on an error carries no
body, per RFC 9110 section 15.5.

## Media Fragments

`t=` and `xywh=` are W3C Media Fragments 1.0. The parser follows
section 5.1.1: split on `&` and `=` first, percent-decode second, so
`t=10%2C20`, `t=%6ept:10`, and `t=npt%3a10` parse as the spec's
section 6.1.1 says they must. `t=begin,end` is half-open, "the begin
time is considered part of the interval whereas the end time is
considered to be the first time point that is not part of the
interval". `xywh=` takes `pixel:` (default) or `percent:`; an
out-of-range region is clipped per section 6.1.2, by the display
sidecar that owns the frame.

The spec tells a user agent to ignore an invalid, unknown, or
non-existent dimension (sections 6.2, 6.2.1, 6.3.1). This API answers
400 instead, because a query produces a new resource (sections 3.1
and 7.4), and a client that asked for a region must not silently get
the whole screen. `t=a,b` with `a >= b`, a repeated dimension, an
unknown key, `width=` with `height=`, and a `begin` over the
60 s limit are all 400.

Media Fragments fixes NPT's zero at the start of the source media
(section 6.1.1). A live tap has no start, so this API defines the
source media's zero as the instant the sidecar accepts the request.
`clock:` is in the advanced document, not in 1.0. The sidecar sends
headers at once, starts its pipeline at once, and discards frames or
samples until `begin` on its own clock, so `t=5,7` discards five
seconds and records two.

## Negotiation

An extension names one fixed representation. No extension negotiates
on `Accept` with q-values; no `Accept` means the aspect's default,
`video/mp4` for `media`. The negotiated route answers
`Content-Location` as an absolute path, `/v1/media/namespaces/{ns}/players/{name}/media.mp4`.
A redirect carries the extension the negotiation chose, so the
sibling negotiates nothing again. `video/x-matroska` is the deprecated
alias of `video/matroska` and matches too.

| Request | Answer |
| --- | --- |
| `GET .../media`, no `Accept` | 200 `video/mp4`, `Content-Location: .../media.mp4` |
| `GET .../media`, `Accept: video/matroska` | 200 `video/matroska`, `Content-Location: .../media.mkv` |
| `GET .../media`, `Accept: video/*;q=0.5, video/matroska` | 200 `video/matroska` |
| `GET .../media.mp4`, `Accept: image/png` | 406, `acceptable: [{type: video/mp4, href: .../media.mp4}, {type: video/matroska, href: .../media.mkv}]` |
| `GET .../screen`, `Accept: image/jpeg` | 307 to `/v1/display/displays/{d}/screen.jpg` |
| `GET .../screen`, no `Accept` | 307 to `/v1/display/displays/{d}/screen.png` |
| `GET .../audio`, `Accept: audio/ogg` | 307 to `/v1/audio/sinks/{s}/audio.opus` |

A 406's `acceptable` member lists `{"type", "href"}` pairs, and on an
extension route it lists the route's own type plus its siblings (RFC
9110 section 15.5.7).

## Link relations

| Relation | Registered | On | Points to |
| --- | --- | --- | --- |
| `alternate` | IANA, referenced to HTML: "Refers to a substitute for this context" | the extensionless `media` route | `media.mp4` and `media.mkv`, each with a bare `type` |
| `related` | IANA, RFC 4287: "Identifies a related resource" | a `screen.*` or `audio.*` 307; the info document | the composed `media.mp4` with the same `t=`; each capture route |
| `describedby` | IANA, referenced to W3C POWDER | every `Player` route | the `Player` object on the API server |
| `service-desc`, `service-doc` | IANA, RFC 8631 | every response | the OpenAPI document; the manual's API page |
| `https://liken.sh/rel/screen` | extension relation, a URI per RFC 8288 section 2.1.2 | a composed response | the display-api route the video came from |
| `https://liken.sh/rel/audio` | extension relation | a composed response, once per audio track | the audio-api route each track came from |

A redirect links the composed form as `related`, because the screen
with sound muxed in is a different aspect of the `Player` and no
substitute for the screen alone. The two extension relations stay
unregistered: registration needs a specification and expert review,
and a URI already identifies the relation. `describedby` is absolute
because a relative reference would resolve against `media-api`, which
does not serve the `Player`.

## The codecs string and what plays it

`media.mp4` is H.264 in fragmented MP4 with Opus audio. The container
follows "Encapsulation of Opus in ISO Base Media File Format",
version 0.6.8 of 28 April 2016: an `OpusSampleEntry` with codingname
`Opus` and a `dOps` box, which `movenc.c` writes. That document has
no `codecs` section. The string comes from RFC 6381 section 3.3,
where the first element is the sample entry's four-character code and
"values are case sensitive": `video/mp4; codecs="avc1.64001f,Opus"`,
the `avc1` bytes copied from the display upstream. Browsers took
lowercase `opus` from the MSE byte-stream convention.

MDN's audio codec guide says "Safari supports Opus in the `<audio>`
element only when packaged in a CAF file", and caniuse.com marks
Safari partial through version 27. Chrome, Firefox, mpv, and VLC play
it. AAC is not in v1: `audio-api` serves none, and an encode on the
API node breaks the rule that the public face never encodes.

`media.mkv` is the same streams through the `matroska` muxer with
`live` set, "Write files assuming it is a live stream", as
`video/matroska`. WebM is not offered: it allows only VP8, VP9, and
AV1 video.

Each sidecar's `t=` origin is the instant it accepts the request, so
the two streams' timestamp 0 are two instants. So `media-api`
composes with a lead-in `L` of 1 s. It asks both upstreams for
`t=begin+L,end+L`, and the composed stream is one second behind
"now", which the discovery document states as `"leadIn": "1s"`. Both
sidecars then have a running pipeline before their zero, and the
correction is the difference of the two accept instants. `media-api`
measures those as the header-arrival instants on its own clock: it
opens both requests at once, records each instant, and starts ffmpeg
with `-itsoffset` on the audio input equal to
`headersAt[audio] - headersAt[video]`.

The first byte of a composition arrives after the lead-in plus the
slower sibling's first keyframe. `media-api` holds nothing back of
its own: it opens both upstreams at once and writes what the muxer
writes, and the muxer writes nothing until it has a keyframe from the
video and a packet from each sink. A screen capture at the default
15 frames per second has a keyframe every second, so a second of
lead-in and a second of keyframe wait are the floor, and the muxer's
first fragment adds the rest. The drill on `liken-1` measured 4.09 to
4.34 s over ten runs of `media.mp4?t=0,10`, and 4.18 s for
`media.mkv`, against upstream headers that arrived at 1.12 to 1.38 s.
Those numbers are from before `display-api` sent its headers at the
accept instant, so they are the ceiling and not the current cost.

## Discovery

`GET /v1/media` answers the shared document shape, keyed by plural:

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

The `screen` and `audio` lists are copied from the siblings'
documents at request time. The info route answers the `Display` name
and node, each `Sink` name, whether a `Play` runs, and the stream
count, with `related` links to every capture route, so a client
learns first whether `media.mp4` will compose or redirect.

## The OpenAPI document

The OpenAPI 3.1 document enumerates each extension path as its own
path item, with no `{.ext}`.
`media-api` fills `servers: [{url: ...}]` from the configured public
base, or else from the origin of the request that fetched the
document, so the `servers` entry names the host the reader reached
and is the one member that changes with the caller.
Its type, `application/openapi+json`, is
provisional: `draft-ietf-httpapi-rest-api-mediatypes` registers it
and IANA does not list it yet.

Through the port-forward the everyday use opens, the same `curl`
with `/v1/media/openapi.json` in place of the capture path fetches
the document the API serves, with the same token and CA.

## Problem documents

Every error body is an RFC 9457 problem document,
`application/problem+json`, with five members. `type` is a URI that
names the kind of problem. `title` is a short phrase for it.
`status` repeats the HTTP status. `detail` says what went wrong in
this request, in the source's own words: a refused query names the
part it refused, and a relayed upstream problem carries the sibling's
own `detail`. `instance` is the request path plus `#` plus the
request id, which the API's log line carries too.

Shared types are `https://liken.sh/problems/no-node`,
`not-acceptable`, `capture-busy`, `upstream-failed`, `not-playing`,
and `away`; with `about:blank`, `title` is the status phrase (4.2.1).
Extension members (`upstream`, `acceptable`) are allowed by 3.2 and
unknown ones are ignored.

## A worked example

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

The `Link` fields are wrapped for reading; each is one field line on
the wire. The upstream `t=` is the caller's plus the lead-in. The
same request for `screen.png` answers 307 with an absolute
`Location: https://display-api.liken-system.svc/v1/display/displays/boe-1080/screen.png`,
because the client reaches that server with its own token, and a
relative `Link: </v1/media/namespaces/media/players/studio/media.mp4>;
rel="related"; type="video/mp4"`. A busy upstream answers 503 with
its `Retry-After` relayed and a problem document `capture-busy` whose
`detail` is the upstream's own and whose `upstream` member is its
URL. A failure after the stream began ends the response; the fault
reaches the client as a truncated body and the request log line.
