---
title: API
weight: 50
toc: true
---

# API

The API lets you listen to the sound itself over HTTP. It taps what
a `Sink` plays or what a `Source` hears, and streams it to you as
WAV, FLAC, or Ogg Opus. RBAC decides who may listen, and every tap
writes an audit record on the object. Two processes are involved.
`audio-api` is a `Deployment` in `liken-system`. It finds the node of
the `Sink` or `Source` and forwards the stream. The `capture`
container in the `audio-operator` pod on that node reads the sound
from PipeWire. Nothing is stored on either side.

**At a glance**

| Item | Value |
| --- | --- |
| Service | `https://audio-api.liken-system.svc` |
| CA `ConfigMap` | `audio-api-ca` in `liken-system` |
| Discovery | `/v1/audio` |
| OpenAPI | `/v1/audio/openapi.json` |
| Shipped `ClusterRole` | `audio-capture-viewer` |
| Audit record | a `Captured` `Event` on the `Sink` or `Source` |
| Route reference | [Routes](/docs/reference/routes/) |

The display and media operators have the same kind of API for a
`Display` and a `Player`. The three APIs share the same paths, the
same HTTP behavior, and the same standards, but no code.

## Authentication

There are two ways to identify yourself: a client certificate or a
Bearer token. The API checks for a certificate first, then for a
token, in the same order as the Kubernetes API server.

### Client certificate

If your TLS connection presents a client certificate signed by the
cluster's own certificate authority, you are that certificate's
subject. Your user name is the subject's common name, and your groups
are the subject's organization values. This is exactly how the
Kubernetes API server reads a client certificate, so the credentials
in your kubeconfig identify you here the same way they identify you
to `kubectl`. The API reads the authority from the `ConfigMap`
`extension-apiserver-authentication` in `kube-system`, which is where
the API server publishes it, and reads it again every minute. A
rotated authority takes effect with no restart. A certificate from
any other authority ends the TLS handshake.

### Bearer token

If the connection has no client certificate, the API looks for a
Bearer token. It authenticates the token with a `TokenReview` that
requires the audience `audio-api`. That requirement keeps every pod's
ordinary API server token out of this API. It has a real cost for
OIDC users: a kubeconfig's OIDC token has the OIDC audience, so a
person on OIDC also has to mint a `ServiceAccount` token.

### Authorization

After it knows who you are, the API sends a `SubjectAccessReview` for
the verb `get` on `sinks/audio` or `sources/audio` in the API group
`audio.liken.sh`, with the name of the object and an empty namespace,
because both kinds are cluster-scoped. An info route needs `get` on
the plain resource. The discovery and OpenAPI documents need
authentication but no authorization. Every route authorizes before it
reads anything, so a `403` never tells you whether a name exists.

### Grants

The operator ships one `ClusterRole` for a cluster owner to bind:
`audio-capture-viewer`. It grants `get` on `sinks`, `sources`,
`sinks/audio`, and `sources/audio`. That covers every route below the
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

The same `ClusterRole` binds to a person. Use `kind: User` with the
common name from their certificate, or `kind: Group` with one of its
organization values.

Because the taps are subresources, an owner can write a narrower
rule. This one grants the sound of one sink and nothing else, not
even the info document next to it:

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

A role that grants `resources: ["*"]` in `audio.liken.sh` already
includes the taps, and so does `cluster-admin`.

There is one existing side door this API does not close. The PipeWire
socket is delivered through claims, so any pod with any audio claim
on a node can already tap that node's microphone and sinks, with no
RBAC and no record. Until that door is closed, a `Source` grant
controls only this API.

A muted `Sink` still delivers its signal to a tap. A sink's monitor
ports carry what the sink receives, and `spec.mute` is applied after
them. Muting a speaker silences the room and changes nothing on this
route. A muted `Source` does tap as silence, because its mute is
applied before the ports the tap reads. Mute is not a way to keep a
speaker out of this API. The grant is.

Every request that returned bytes writes a `Captured` `Event` on the
`Sink` or `Source`, so `kubectl describe sink` tells you who listened
and when.

## Routes

Every path has the form
`/v1/{domain}/[namespaces/{ns}/]{plural}/{name}/{aspect}[.{ext}]`.
`domain` is the first label of the CRD's API group, so
`audio.liken.sh` gives `audio`. A namespaced kind has
`namespaces/{ns}` before its plural, the same as Kubernetes. The
domain segment is in the path so that one ingress can later serve
every domain's API under one host name without a path clash, and so
that a future `video` domain with its own sinks and sources fits next
to `audio`. That ingress is not part of v1. In v1, each API is its
own `Service`.

You choose the format with the extension or with `Accept`. You choose
the span with a W3C Media Fragments `t=` in the query. Any other
parameter that only changes the representation, such as the bitrate,
also goes in the query. No route accepts a request body, so `415`
never occurs.

| Method | Path | Response | Notes |
| --- | --- | --- | --- |
| GET, HEAD | `/v1/audio` | 200 `application/json` | The discovery document |
| GET, HEAD | `/v1/audio/openapi.json` | 200 `application/openapi+json` | OpenAPI 3.1 |
| GET, HEAD | `/v1/audio/sinks/{name}` | 200 `application/json` | The sink's format and the routes that tap it |
| GET, HEAD | `/v1/audio/sinks/{name}/audio` | 200, negotiated | What the speakers play now. Default `audio/wav` |
| GET, HEAD | `/v1/audio/sinks/{name}/audio.wav` | 200 `audio/wav` | PCM in a RIFF WAVE stream |
| GET, HEAD | `/v1/audio/sinks/{name}/audio.flac` | 200 `audio/flac` | FLAC |
| GET, HEAD | `/v1/audio/sinks/{name}/audio.opus` | 200 `audio/ogg; codecs=opus` | Ogg Opus |
| GET, HEAD | `/v1/audio/sources/{name}` | 200 `application/json` | The source's format and the routes that tap it |
| GET, HEAD | `/v1/audio/sources/{name}/audio[.ext]` | as the sink routes above | What the microphone hears, in the same forms |
| OPTIONS | any of the above | 204, no body | `Allow: GET, HEAD, OPTIONS` (RFC 9110 sections 9.3.7 and 10.2.1) |

The [Routes](/docs/reference/routes/) page lists every route from the
OpenAPI document, with its parameters, responses, and fields. The
OpenAPI 3.1 document is generated from the router table, so the
routes, media types, query parameters, and problem types on this page
and in that document come from one source. It is at
[`/v1/audio/openapi.json`](/v1/audio/openapi.json). Its media type,
`application/openapi+json`, is registered by
`draft-ietf-httpapi-rest-api-mediatypes` and is provisional until
that draft is published.

## Query parameters

| Parameter | Applies to | Values | Default | Rejected with `400` when |
| --- | --- | --- | --- | --- |
| `t` | every tap route | Media Fragments NPT: `t=begin,end`, `t=begin`, or `t=,end`. The interval is half-open, `[begin, end)` | none: the tap runs until you close the connection | a form the grammar rejects; `t=a,b` with `a >= b`; a begin over 60 s (`captureBeginMax`); a repeated dimension |
| `bitrate` | `audio` and `audio.opus` | the Opus bitrate in kbit/s per channel, 6 to 256 | `opusenc` chooses one from the sample rate | WAV or FLAC; a value outside 6 to 256 |

`t=0,5` gives five seconds and then ends the stream. `t=5,7` discards
five seconds and then records two. With no `end`, or with no `t=` at
all, the tap runs until you close the connection.
`audio.opus?bitrate=128` gives Opus at 128 kbit/s.

`t=` goes in the query because of Media Fragments section 3.1, "a URI
query produces a new resource", and section 7.4, which says a query
may change the media type. The parser follows section 5.1.1: it
splits on `&` and `=` first and percent-decodes second, so
`t=10%2C20`, `t=%6ept:10`, and `t=npt%3a10` all parse (section
6.1.1).

Media Fragments puts NPT's zero at the start of the source media
(section 6.1.1). A live tap has no start, so this API puts the zero
at the instant the capture container accepts the request. The
`clock:` format that would say this directly is in the advanced Media
Fragments document, not in version 1.0. The capture container sends
the headers at once, starts `pw-record` at once, discards samples
until `begin` on its own clock, and stops the encoder at `end`. So
sample zero of the body is the request time plus `begin`, as long as
the pipeline started within `begin`.

Media Fragments tells a user agent to ignore an invalid, unknown, or
non-existent dimension (sections 6.2, 6.2.1, 6.3.1). This API returns
`400` instead. A query produces a new resource, so a client that
asked for a span must not silently get something else.

## Content negotiation

An extension names one fixed representation. With no extension, the
API chooses by `Accept` with q-values, per RFC 9110 section 12.5.1.
Ties resolve in the server's order: WAV, FLAC, Opus. No `Accept`
header means `audio/wav`.

| Request | Response |
| --- | --- |
| `Accept` absent, `*/*`, or `audio/*` | `200`, `audio/wav`, `Content-Location: /v1/audio/sinks/kitchen/audio.wav` |
| `Accept: audio/ogg; codecs=opus, audio/flac;q=0.5` | Opus, the higher q |
| `Accept: audio/flac;q=0.5, audio/ogg;q=0.5` | FLAC, the server's order at equal q |
| `Accept: audio/wav;q=0, */*` | FLAC: `q=0` excludes WAV, and the wildcard admits the rest |
| `Accept: audio/mpeg` | `406`, and `acceptable` lists the three types with their `href`s |

That table is the response to `GET /v1/audio/sinks/kitchen/audio`. On
`audio.wav`, `Accept: audio/flac` is a `406` that lists `audio.wav`,
`audio.flac`, and `audio.opus`. `Accept: audio/vnd.wave` gets WAV.

| Format | Media type | Aliases | Standard |
| --- | --- | --- | --- |
| RIFF WAVE | `audio/wav` | `audio/wave`, `audio/x-wav`, `audio/vnd.wave` | RFC 2361 for `audio/vnd.wave`, WHATWG MIME Sniffing section 6.2 for `audio/wave` |
| FLAC | `audio/flac` | `audio/x-flac`, deprecated | RFC 9639 section 12.1 |
| Ogg Opus | `audio/ogg; codecs=opus` | `audio/ogg` | RFC 5334 for `audio/ogg` and its `codecs` parameter, RFC 7845 section 9 for `opus` and the `.opus` extension |

The only RIFF WAVE name in the IANA registry is `audio/vnd.wave` from
RFC 2361, an informational RFC from 1998 that no browser, ffmpeg, or
mpv sends. The WHATWG MIME Sniffing Standard calls the RIFF signature
`audio/wave`. Clients use `audio/wav`, so that is what the API sends.

## Response headers

Every response has these headers:

| Header | Value | Standard |
| --- | --- | --- |
| `Content-Type` | the media type of the body | RFC 9110 |
| `Vary` | `Accept`, on every response, extension routes included | RFC 9110 section 12.5.5 |
| `Link` | `rel="service-desc"` to the OpenAPI document, and `rel="service-doc"` to this page | RFC 8631 |
| `Link` | on a response about one object: `rel="describedby"` with the absolute URL of the object in the Kubernetes API | RFC 8288 section 3.1 |

A document (discovery, OpenAPI, or an info route) has an `ETag` and
`Cache-Control: no-cache`, and answers `If-None-Match` with a `304`
(RFC 9110 section 8.8.3, RFC 9111 section 5.2.2.4). The `ETag` is the
build version, because the router table is compiled into the binary.
An info document adds `rel="related"` (RFC 4287, registered) to each
of its capture routes. A tap has these headers instead:

| Header | Value | Standard |
| --- | --- | --- |
| `Cache-Control` | `no-store` | RFC 9111 section 5.2.2.5 |
| `Accept-Ranges` | `none` | RFC 9110 section 14.3 |
| `Content-Disposition` | `inline; filename="<name>-<time>.<ext>"` | RFC 6266 |
| `Transfer-Encoding` | `chunked`, on HTTP/1.1 | RFC 9112 section 7.1 |
| `Content-Location` | on the negotiated route only: the absolute path of the extension route that was served | RFC 9110 section 8.7 |
| `Link` | on the route with no extension: one `rel="alternate"` per fixed format | RFC 8288 |

`Content-Location` is only on the negotiated route. RFC 9110 section
8.7 calls it "a more specific identifier for the selected
representation". The same section says a `GET` on that URL returns
the same representation. That is not true for a live capture, and the
API does not claim it. `Accept-Ranges: none` says the same thing
about bytes: a live capture has no byte identity, and byte 44 of two
requests is two different moments.

`Vary: Accept` is on the extension routes as well, where `Accept` can
turn a `200` into a `406`. RFC 9110 section 12.5.5 gives a second
reason for `Vary`: it tells the recipient that the response was
subject to negotiation. The `describedby` URL is absolute because a
relative reference would resolve against the wrong server.

`Content-Disposition` gives a browser the file name to save as. The
time in it is RFC 3339 UTC with the colons replaced by dashes, for
example `usb-0573-1573-a34004801402-usb-audio-2026-09-16T21-02-16Z.wav`.
A colon is not a legal file name character on every system a browser
saves to. This is the only place the API writes a time in a
non-standard form.

The `type` attribute of a link has no media type parameters (RFC 8288
section 3.4.1). The API only uses registered relations. An extension
relation would go under `https://liken.sh/rel/`.

```
Link: <https://kubernetes.default.svc/apis/audio.liken.sh/v1alpha1/sinks/kitchen>; rel="describedby",
      </v1/audio/sinks/kitchen/audio.flac>; rel="alternate"; type="audio/flac",
      </v1/audio/sinks/kitchen/audio.opus>; rel="alternate"; type="audio/ogg"
```

(wrapped here for reading; it is one header line on the wire)

`HEAD` returns the same headers as `GET`. It takes no sample and does
not call the capture container (RFC 9110 section 9.3.2). A `HEAD` on
an error has no body (section 15.5).

## Errors

Every error is an RFC 9457 problem document,
`application/problem+json`. `type` is a URL. When it is `about:blank`,
the `title` is the status phrase (section 4.2.1). `instance` is the
request path, then `#`, then the request id. The API's log line for
the request has the same id. `detail` quotes the source of the error
verbatim: the stderr of `pw-record`, the stderr of an encoder, or the
`status.message` from the API server. A `406` has an `acceptable`
list of `{"type", "href"}` objects. On an extension route it lists
that route's own type and its siblings, with their URLs (RFC 9110
section 15.5.7).

This table lists every status this API returns, the headers that come
with it, and the standard it comes from.

| Status | When | Headers | From |
| --- | --- | --- | --- |
| `200` document | `/v1/audio`, `openapi.json`, or an info route | see [Response headers](#response-headers) | RFC 9110 sections 8.8.3 and 12.5.5, RFC 9111 section 5.2.2.4 |
| `304` | `If-None-Match` matches a document's `ETag` | `ETag`, `Vary: Accept` | RFC 9110 sections 13.1.2 and 15.4.5 |
| `200` tap | a tap is running | see [Response headers](#response-headers) | RFC 9110, RFC 9111 section 5.2.2.5, RFC 9112 section 7.1, RFC 6266, RFC 8288 |
| `204` | `OPTIONS` | `Allow: GET, HEAD, OPTIONS` | RFC 9110 section 10.2.1 |
| `400` | a `t=` the grammar rejects, `t=a,b` with `a >= b`, a begin over `captureBeginMax`, a repeated dimension, an unknown query parameter, or a parameter the format does not take | `application/problem+json` | RFC 9457. This is a deliberate departure from Media Fragments, explained under [Query parameters](#query-parameters) |
| `401` | no client certificate and no token: `WWW-Authenticate: Bearer realm="audio-api"`. A token the `TokenReview` refuses: the same header plus `error="invalid_token"` and an `error_description` with the review's own words | `WWW-Authenticate` | RFC 9110 section 15.5.2, RFC 6750 section 3 |
| `403` | the `SubjectAccessReview` said no | `WWW-Authenticate: Bearer realm="audio-api", error="insufficient_scope", scope="sinks/audio"` | RFC 9110 section 15.5.4, RFC 6750 section 3.1 |
| `404` | no `Sink` or `Source` has that name, or PipeWire has no node for it | `application/problem+json` | RFC 9110 section 15.5.5 |
| `405` | a method other than GET, HEAD, or OPTIONS | `Allow` | RFC 9110 section 15.5.6 |
| `406` | `Accept` excludes every representation the route can serve | `application/problem+json` with `acceptable` | RFC 9110 sections 12.5.1 and 15.5.7 |
| `409` | the object is away: it has no `status.node`. `detail` tells you to power the device on | `application/problem+json`, type `away` | RFC 9110 section 15.5.10 |
| `500` | the tap connected to a node other than the one you asked for | `application/problem+json`, type `wrong-target` | RFC 9110 section 15.6.1 |
| `502` | the capture container answered with something that is not HTTP, or not a problem document | `application/problem+json` | RFC 9110 section 15.6.3 |
| `503` | the capture container is at its tap limit, refused the connection, is not ready, has no certificate yet, or PipeWire refused `pw-record` | `Retry-After: 5`, `application/problem+json` | RFC 9110 sections 15.6.4 and 10.2.3 |
| `504` | the capture container sent no headers within the header timeout | `application/problem+json` | RFC 9110 section 15.6.5 |

The API uses `409` only where you can do something about it, and the
`detail` says what.

These are the problem types this API uses. The first five are shared
with the display and media APIs. The last one belongs to this API.

| Type URI | Meaning | Status |
| --- | --- | --- |
| `https://liken.sh/problems/no-node` | no `Sink` or `Source` has that name, or PipeWire has no node for it | `404` |
| `https://liken.sh/problems/not-acceptable` | `Accept` excludes every representation the route can serve | `406` |
| `https://liken.sh/problems/capture-busy` | the capture container is at its tap limit | `503` |
| `https://liken.sh/problems/upstream-failed` | the capture container answered something this API cannot relay, or sent no headers in time | `502`, `504` |
| `https://liken.sh/problems/away` | the object is away: it has no `status.node` | `409` |
| `https://audio.liken.sh/problems/wrong-target` | the tap connected to a node other than the one you asked for | `500` |

## Discovery

`GET /v1/audio` returns the discovery document. All three capture
APIs use the same shape. The templates are RFC 6570. `{name}` is
simple expansion (section 3.2.2), `{.ext}` is label expansion
(3.2.5), and `{?t,bitrate}` is form-style query expansion (3.2.8).
Form-style expansion percent-encodes the comma in `t=5,7`, and the
parsing order above accepts that form.

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

## Examples

There is no `kubectl` plugin in v1. The recipe below works through a
port-forward or through an ingress the cluster owner sets up.

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

If your kubeconfig has a client certificate, send that instead. You
need no `ServiceAccount` and no token. The port-forward is a TCP
tunnel, so the TLS handshake runs end to end and the certificate
reaches the API unchanged.

```sh
kubectl config view --raw --minify \
    -o jsonpath='{.users[0].user.client-certificate-data}' | base64 -d > client.crt
kubectl config view --raw --minify \
    -o jsonpath='{.users[0].user.client-key-data}' | base64 -d > client.key
curl --cacert ca.crt --cert client.crt --key client.key \
    --resolve audio-api.liken-system.svc:8443:127.0.0.1 \
    "https://audio-api.liken-system.svc:8443/v1/audio/sinks/kitchen-pci-0000-00-1f-3-hdmi-0/audio.wav?t=0,5" \
    -o kitchen.wav
```

From a pod on the cluster network, the same two files work against
`https://audio-api.liken-system.svc/v1/audio/...` with no
port-forward and no `--resolve`.

To listen live, use `mpv` with the URL in place of `curl -o`.

v1 has no CORS support, so a browser can only reach the API on the
same origin through a port-forward.

## Notes

**The info route.** `GET /v1/audio/sinks/{name}` returns the `node`,
the `connectionType`, the `rate` and `channels` a tap would use, the
`status.format` as reported, the formats served, and a
`Link: rel="related"` to each capture route. It costs one read from
the API server and one read of the PipeWire graph.

## Metrics

Both processes serve `liken_build_info`, `/healthz`, `/readyz`, and
`/metrics`: the API on port 9200 in its own pod, and the capture
container on port 9201. The `route` label is the RFC 6570 template,
never the concrete path, so no endpoint name reaches Prometheus.

| Process | Metric | Type |
| --- | --- | --- |
| audio-api | `audio_api_requests_total{route,method,status}` | counter |
| audio-api | `audio_api_request_seconds{route}` | histogram, time to headers |
| audio-api | `audio_api_streams_active{aspect}` | gauge |
| audio-api | `audio_api_certificate_expiry_seconds` | gauge |
| audio-capture | `audio_capture_ready` | gauge |
| audio-capture | `audio_capture_bytes_total{aspect,format}` | counter |
| audio-capture | `audio_capture_seconds_total{aspect,format}` | counter, stream time |
| audio-capture | `audio_captures_active{aspect}` | gauge |
| audio-capture | `audio_capture_failures_total{reason}` | counter: `connect`, `target`, `wrong-target`, `encoder`, `limit`, `certificate` |

Only the capture container emits the capture counters, so nothing is
counted twice.
