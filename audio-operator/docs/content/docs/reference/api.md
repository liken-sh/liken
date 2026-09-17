---
title: API
weight: 50
toc: true
---

# API

The API is an HTTP door to the sound itself. It taps what a `Sink`
plays and what a `Source` hears, and streams it as WAV, FLAC, or Ogg
Opus, under a grant that RBAC governs and with an audit record on the
object.

`audio-api` is a `Deployment` in `liken-system` that finds the
target's node and forwards the stream, and the `capture` container in
the `audio-operator` pod on that node reads PipeWire. Nothing is
stored on either side.

This is one of three APIs that share a route shape, a vocabulary, and
the same standards. The display and media operators hold the other
two, and the three share no code.

## The routes

The path grammar is
`/v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}]`.
`domain` is the first label of the CRD's API group, so
`audio.liken.sh` gives `audio`. A namespaced kind puts
`namespaces/{ns}` before its plural, as Kubernetes does.

The domain segment is in the path so that one ingress can later mount
every domain's API under one host name with no path clash, and so
that a future `video` domain with sinks and sources of its own fits
beside `audio`. That ingress is not v1 work; in v1 each API is its
own `Service`.

The format is chosen by the extension or by `Accept`, the span by a
W3C Media Fragments `t=` in the query, and a knob that only changes
the representation also goes in the query.

| Route | Answer |
| --- | --- |
| `GET /v1/audio` | the discovery document, `application/json` |
| `GET /v1/audio/openapi.json` | the OpenAPI 3.1 document |
| `GET /v1/audio/sinks/{name}` | the sink's format and the routes that tap it |
| `GET /v1/audio/sinks/{name}/audio` | what the speakers play now, negotiated by `Accept` |
| `GET /v1/audio/sinks/{name}/audio.wav` | PCM in a RIFF WAVE stream |
| `GET /v1/audio/sinks/{name}/audio.flac` | FLAC |
| `GET /v1/audio/sinks/{name}/audio.opus` | Ogg Opus |
| `GET /v1/audio/sinks/{name}/audio.wav?t=0,5` | five seconds, then the stream ends |
| `GET /v1/audio/sinks/{name}/audio.wav?t=5,7` | discard five seconds, then two seconds |
| `GET /v1/audio/sinks/{name}/audio.opus?bitrate=128` | Opus at 128 kbit/s |
| `GET /v1/audio/sources/{name}` | the source's format and the routes that tap it |
| `GET /v1/audio/sources/{name}/audio[.ext]` | what the microphone hears, the same forms |

`HEAD` answers with the `GET`'s headers, takes no sample, and makes no
call to the capture container (RFC 9110 section 9.3.2). A `HEAD` on an
error carries no body (section 15.5).

`OPTIONS` answers `204` with `Allow: GET, HEAD, OPTIONS` (RFC 9110
sections 9.3.7 and 10.2.1). Any other method is `405` with the same
`Allow` (section 15.5.6). No route accepts content, so `415` never
occurs.

## Status codes

The table lists every status this API answers, with the headers each
one carries and the standard each one comes from.

| Status | When | Headers | From |
| --- | --- | --- | --- |
| `200` document | `/v1/audio`, `openapi.json`, an info route | `Content-Type`, `ETag`, `Cache-Control: no-cache`, `Vary: Accept`, `Link` | RFC 9110 sections 8.8.3 and 12.5.5, RFC 9111 section 5.2.2.4 |
| `304` | `If-None-Match` matches a document's `ETag` | `ETag`, `Vary: Accept` | RFC 9110 sections 13.1.2 and 15.4.5 |
| `200` tap | a tap runs | `Content-Type`, `Vary: Accept`, `Cache-Control: no-store`, `Accept-Ranges: none`, `Content-Disposition`, `Link`, `Transfer-Encoding: chunked` on HTTP/1.1, and `Content-Location` on the negotiated route | RFC 9110, RFC 9111 section 5.2.2.5, RFC 9112 section 7.1, RFC 6266, RFC 8288 |
| `204` | `OPTIONS` | `Allow: GET, HEAD, OPTIONS` | RFC 9110 section 10.2.1 |
| `400` | a `t=` the grammar refuses, `t=a,b` with `a >= b`, a begin over `captureBeginMax`, a repeated dimension, an unknown query parameter, a knob the format does not take | `application/problem+json` | RFC 9457; a deliberate departure from Media Fragments, below |
| `401` | no token: `WWW-Authenticate: Bearer realm="audio-api"` alone; a token the `TokenReview` refuses: `error="invalid_token"`, `error_description` with the review's own words | `WWW-Authenticate` | RFC 9110 section 15.5.2, RFC 6750 section 3 |
| `403` | the `SubjectAccessReview` says no | `WWW-Authenticate: Bearer realm="audio-api", error="insufficient_scope", scope="sinks/audio"` | RFC 9110 section 15.5.4, RFC 6750 section 3.1 |
| `404` | no `Sink` or `Source` of that name, or PipeWire holds no node for it | `application/problem+json` | RFC 9110 section 15.5.5 |
| `405` | a method other than the three | `Allow` | RFC 9110 section 15.5.6 |
| `406` | `Accept` excludes every representation the route serves | `application/problem+json` with `acceptable` | RFC 9110 sections 12.5.1 and 15.5.7 |
| `409` | the object is away: no `status.node`; `detail` says to power the device on | `application/problem+json`, type `away` | RFC 9110 section 15.5.10 |
| `500` | the tap's link landed on a node other than the one asked for | `application/problem+json`, type `wrong-target` | RFC 9110 section 15.6.1 |
| `502` | the capture container answered something that is not HTTP or not a problem document | `application/problem+json` | RFC 9110 section 15.6.3 |
| `503` | the container is at its tap limit, refused the connection, is not ready, has no certificate yet, or PipeWire refused `pw-record` | `Retry-After: 5`, `application/problem+json` | RFC 9110 sections 15.6.4 and 10.2.3 |
| `504` | the container sent no headers within the header timeout | `application/problem+json` | RFC 9110 section 15.6.5 |

`409` is used only where the caller can act, and the `detail` says
what clears it.

Every route authorizes before it reads, so a `403` never reveals that
a name exists.

## Problem documents

Every error is an RFC 9457 problem document. `type` is a URL. With
`about:blank`, the `title` is the status phrase (section 4.2.1).

`instance` is the request path plus `#` plus the request id, and the
log line carries the same id.

`detail` carries the source's own words verbatim: `pw-record`'s
stderr, an encoder's stderr, or the API server's `status.message`.

A `406` carries `acceptable`, a list of `{"type", "href"}`. On an
extension route it lists that route's own type and its siblings with
their URIs (RFC 9110 section 15.5.7).

These are the type URIs this API answers with. The first five are
shared with the display and media APIs, and the last one is this
domain's own.

- `https://liken.sh/problems/no-node`
- `https://liken.sh/problems/not-acceptable`
- `https://liken.sh/problems/capture-busy`
- `https://liken.sh/problems/upstream-failed`
- `https://liken.sh/problems/away`
- `https://audio.liken.sh/problems/wrong-target`

## Headers

Documents carry `ETag`, which is the build version because the router
table is compiled in, and `Cache-Control: no-cache`, and they answer
`If-None-Match` with `304`.

Taps carry `Cache-Control: no-store` and `Accept-Ranges: none`,
because a live capture has no byte identity: offset 44 of two
requests is two different moments (RFC 9110 section 14.3).

`Content-Disposition: inline; filename="<name>-<time>.<ext>"` names a
browser's save (RFC 6266). The RFC 3339 UTC time has its colons
replaced by dashes, because a colon is not legal in a file name
everywhere a browser saves. This is the one place the time is not in
the standard form:
`usb-0573-1573-a34004801402-usb-audio-2026-09-16T21-02-16Z.wav`.

`Vary: Accept` is on every response, extension routes included, for
RFC 9110 section 12.5.5's second purpose: it tells the recipient the
response was subject to negotiation, where `Accept` could turn a
`200` into a `406`.

`Content-Location` is on the negotiated route only, as an absolute
path, under RFC 9110 section 8.7's clause that it is "a more specific
identifier for the selected representation". Section 8.7's identity
guarantee, that a `GET` on that URI would return the same
representation, does not hold for a live capture and is not claimed.

Every response carries `rel="service-desc"` to the OpenAPI document
and `rel="service-doc"` to this page (RFC 8631), and
`rel="describedby"` absolute to the resource's own API object path,
because a relative reference would resolve against the wrong server
(RFC 8288 section 3.1).

The extensionless route adds `rel="alternate"` for each fixed form,
and an info document adds `rel="related"` (RFC 4287, registered) to
its capture routes.

`type` carries no media type parameters (RFC 8288 section 3.4.1).
Only registered relations are used; an extension relation would go
under `https://liken.sh/rel/`.

```
Link: <https://kubernetes.default.svc/apis/audio.liken.sh/v1alpha1/sinks/kitchen>; rel="describedby",
      </v1/audio/sinks/kitchen/audio.flac>; rel="alternate"; type="audio/flac",
      </v1/audio/sinks/kitchen/audio.opus>; rel="alternate"; type="audio/ogg"
```

(wrapped for reading; one field line on the wire)

## The negotiation

An extension names one fixed representation. With no extension,
`Accept` chooses by RFC 9110 section 12.5.1 with q-values, ties
broken by the server's preference order (WAV, FLAC, Opus), and no
`Accept` means `audio/wav`.

WAV is sent as `audio/wav`. The IANA registry's only RIFF WAVE name
is `audio/vnd.wave` from RFC 2361, informational, 1998, which no
browser, ffmpeg, or mpv sends, and the WHATWG MIME Sniffing Standard
names the RIFF signature `audio/wave` (section 6.2). `audio/wav` is
what clients use, so it is the name sent, and `wav`, `wave`, `x-wav`,
and `vnd.wave` match as one representation.

FLAC is `audio/flac`, RFC 9639 section 12.1. `audio/x-flac` is its
deprecated alias and matches too.

Ogg Opus is `audio/ogg; codecs=opus`, because RFC 5334 registers
`audio/ogg` with the `codecs` parameter and RFC 7845 section 9 adds
`opus` and the `.opus` extension. A plain `audio/ogg` matches it.

The table is the answer on `GET /v1/audio/sinks/kitchen/audio`.

| `Accept` | Answer |
| --- | --- |
| absent, `*/*`, or `audio/*` | `200`, `audio/wav`, `Content-Location: /v1/audio/sinks/kitchen/audio.wav` |
| `audio/ogg; codecs=opus, audio/flac;q=0.5` | Opus, the higher q |
| `audio/flac;q=0.5, audio/ogg;q=0.5` | FLAC, the server's order at equal q |
| `audio/wav;q=0, */*` | FLAC: `q=0` excludes WAV, the wildcard admits the rest |
| `audio/mpeg` | `406`; `acceptable` lists the three with their `href`s |

On `audio.wav`, `Accept: audio/flac` is `406` listing `audio.wav`,
`audio.flac`, and `audio.opus`, and `Accept: audio/vnd.wave` is WAV.

## Time

`t=` is the W3C Media Fragments temporal dimension in NPT (section
4.2.1), in the forms `t=begin,end`, `t=begin`, or `t=,end`. The
interval is half-open, `[begin, end)`.

The query form is Media Fragments section 3.1, "a URI query produces
a new resource", and section 7.4, which says a query approach may
change the media type.

The parser follows section 5.1.1: it splits on `&` and `=` first and
percent-decodes second, so `t=10%2C20`, `t=%6ept:10`, and
`t=npt%3a10` parse (section 6.1.1).

Media Fragments fixes NPT's zero at the start of the source media
(section 6.1.1). A live tap has no start, so this API defines the
source media's zero as the instant the capture container accepts the
request. The `clock:` format that would say this directly is in the
advanced document, not in 1.0.

Media Fragments tells a user agent to ignore an invalid, unknown, or
non-existent dimension (sections 6.2, 6.2.1, 6.3.1). This API answers
`400` instead, because a query produces a new resource and a client
that asked for a span must not silently get something else. A
repeated dimension is `400`, and `t=a,b` with `a >= b` is `400`.

The origin is the instant the container accepts the request. It sends
the headers at once, starts `pw-record` at once, discards samples
until `begin` on its own clock, and stops the encoder at `end`, so
sample zero of the body is origin plus `begin` whenever the pipeline
started within `begin`. `t=5,7` discards five seconds, then records
two.

The largest `begin` is 60 seconds, the `captureBeginMax` the status
table names, and a larger `begin` is `400`. An absent `end`, or an
absent `t=`, means until the client closes.

## Discovery and OpenAPI

`GET /v1/audio` answers the shape the three APIs share.

The templates are RFC 6570: `{name}` is simple expansion (section
3.2.2), `{.ext}` label expansion (3.2.5), and `{?t,bitrate}`
form-style query expansion (3.2.8), which percent-encodes the comma
in `t=5,7`, a form the parsing order accepts.

```json
{
  "resources": {
    "sinks": {
      "self": "/v1/audio/sinks/{name}",
      "aspects": {
        "audio": {
          "template": "/v1/audio/sinks/{name}/audio{.ext}{?t,bitrate}",
          "mediaTypes": ["audio/wav", "audio/flac", "audio/ogg"],
          "extensions": ["wav", "flac", "opus"],
          "redirects": false
        }
      }
    },
    "sources": {
      "self": "/v1/audio/sources/{name}",
      "aspects": {
        "audio": {
          "template": "/v1/audio/sources/{name}/audio{.ext}{?t,bitrate}",
          "mediaTypes": ["audio/wav", "audio/flac", "audio/ogg"],
          "extensions": ["wav", "flac", "opus"],
          "redirects": false
        }
      }
    }
  },
  "openapi": "/v1/audio/openapi.json"
}
```

`GET /v1/audio/sinks/{name}` answers `node`, `connectionType`, the
`rate` and `channels` a tap would use, `status.format` as reported,
the formats served, and `Link: rel="related"` to each capture route,
from one API server read and one graph read.

The OpenAPI 3.1 document is generated from the router table, so the
routes, media types, query parameters, and problem types on this page
and in that document are one source.

The `application/openapi+json` type is registered by
`draft-ietf-httpapi-rest-api-mediatypes` and is provisional until
that draft publishes.

- [`/v1/audio/openapi.json`](/v1/audio/openapi.json), `application/openapi+json`

## Grants

A request is authenticated with a `TokenReview` that requires the
audience `audio-api`. It is authorized with a `SubjectAccessReview`
for verb `get` on `sinks/audio` or `sources/audio` in group
`audio.liken.sh`, with the resource's name and an empty namespace,
because both kinds are cluster-scoped.

An info route needs `get` on the ordinary resource. The discovery and
OpenAPI documents need authentication and no authorization.

The operator ships one `ClusterRole` for an owner to bind,
`audio-capture-viewer`. It grants `get` on `sinks`, `sources`,
`sinks/audio` and `sources/audio`, which is every route below the
discovery document: the two plain resources for the info routes and
the two subresources for the taps.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: audio-capture-viewer
rules:
  - apiGroups: [audio.liken.sh]
    resources: [sinks, sources, sinks/audio, sources/audio]
    verbs: [get]
```

The subresource shape lets an owner write a narrower rule instead.
This one grants the sound of one sink and nothing else, not even the
information document beside it.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kitchen-listener
rules:
  - apiGroups: [audio.liken.sh]
    resources: [sinks/audio]
    verbs: [get]
    resourceNames: [kitchen-pci-0000-00-1f-3-hdmi-0]
```

A role with `resources: ["*"]` in `audio.liken.sh`, and
`cluster-admin`, gain capture on the day this ships.

The claim-delivered PipeWire socket is an existing side door this API
does not close. Any pod that holds any audio claim on a node can
already tap that node's microphone and sinks, with no RBAC and no
record, so a `Source` grant is a courtesy until that door closes.

Every request that produces bytes writes a `Captured` `Event` on the
`Sink` or `Source`, so `kubectl describe sink` answers who listened
and when.

## Calling it

There is no `kubectl` plugin in v1. The recipe below runs through a
port-forward or the owner's ingress.

```sh
kubectl -n liken-system create serviceaccount listener
kubectl create clusterrolebinding listener --clusterrole audio-capture-viewer \
    --serviceaccount liken-system:listener
kubectl -n liken-system port-forward svc/audio-api 8443:443 &
kubectl -n liken-system get configmap audio-api-ca -o jsonpath='{.data.ca\.crt}' > ca.crt
curl --cacert ca.crt --resolve audio-api.liken-system.svc:8443:127.0.0.1 \
    -H "Authorization: Bearer $(kubectl -n liken-system create token listener --audience audio-api --duration 10m)" \
    "https://audio-api.liken-system.svc:8443/v1/audio/sinks/kitchen-pci-0000-00-1f-3-hdmi-0/audio.wav?t=0,5" \
    -o kitchen.wav
```

The audience keeps every pod's ordinary API-server token out of this
API. The cost is real for OIDC: a kubeconfig's own OIDC token carries
the OIDC audience, so a person on OIDC mints a `ServiceAccount` token
too.

`mpv` in place of `curl -o` listens live.

v1 has no CORS, so a browser reaches the API only on the same origin
through a port-forward.

## Metrics

Both processes serve `liken_build_info`, `/healthz`, `/readyz`, and
`/metrics`: the API on 9200 in its own pod, and the capture container
on 9201. The `route` label is the RFC 6570 template, never the
concrete path, so no endpoint name enters Prometheus.

| Process | Metric | Type |
| --- | --- | --- |
| audio-api | `audio_api_requests_total{route,method,status}` | counter |
| audio-api | `audio_api_request_seconds{route}` | histogram, header time |
| audio-api | `audio_api_streams_active{aspect}` | gauge |
| audio-api | `audio_api_certificate_expiry_seconds` | gauge |
| audio-capture | `audio_capture_ready` | gauge |
| audio-capture | `audio_capture_bytes_total{aspect,format}` | counter |
| audio-capture | `audio_capture_seconds_total{aspect,format}` | counter, stream time |
| audio-capture | `audio_captures_active{aspect}` | gauge |
| audio-capture | `audio_capture_failures_total{reason}` | counter: `connect`, `target`, `wrong-target`, `encoder`, `limit`, `certificate` |

The capture counters are emitted by the container only, so nothing
double-counts.
